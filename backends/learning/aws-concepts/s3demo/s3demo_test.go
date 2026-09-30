package s3demo

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/aws-concepts/awstest"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

// bucket creates a bucket for one test and removes it afterwards.
func bucket(t *testing.T) (*s3.Client, string) {
	t.Helper()

	cfg := awstest.Config(t)
	client := New(cfg)
	name := awstest.Name(t, "s3")

	ctx := context.Background()

	created, err := CreateBucket(ctx, client, name)
	require.NoError(t, err)
	require.True(t, created, "the bucket name should be unique to this test")

	t.Cleanup(func() {
		if err := DeleteBucket(context.Background(), client, name); err != nil {
			t.Errorf("clean up bucket %s: %v", name, err)
		}
	})

	return client, name
}

// TestCreateBucketTwiceIsNotAnError shows that setup code can run twice.
//
// Setup code runs twice all the time: a retried CI job, a developer running a test after an interrupted one. A
// create that fails on the second run is a create that needs a wrapper, and this is the wrapper.
//
// What the assertion below is NOT is `require.False(t, created)`. That is what I wrote first, and it failed. In
// us-east-1 a repeat CreateBucket returns a plain 200 OK, so there is no error to inspect and the SDK reports a
// successful creation of a bucket that already existed. Every other region returns BucketAlreadyOwnedByYou and
// the wrapper's false branch is the one that runs.
func TestCreateBucketTwiceIsNotAnError(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	created, err := CreateBucket(ctx, client, name)
	require.NoError(t, err, "creating a bucket you already own is a condition, not a failure")

	require.True(t, created,
		"us-east-1 answers a repeat CreateBucket with 200 OK, so `created` cannot be trusted in this region")
	require.Equal(t, "us-east-1", awstest.Region, "the assertion above holds only for the legacy region")
}

// TestPutGetRoundTrip is the whole of S3 in eight lines, plus the etag claim.
func TestPutGetRoundTrip(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	body := []byte("the quick brown fox")

	etag, err := Put(ctx, client, name, "animals/fox.txt", body, "text/plain")
	require.NoError(t, err)

	got, err := Get(ctx, client, name, "animals/fox.txt")
	require.NoError(t, err)
	require.Equal(t, body, got)

	// The etag of a single-part upload is the MD5 in hex, quoted. Asserting it here is the point: it is the
	// claim in the Put doc comment, and a claim in a comment that nothing checks is a guess.
	sum := md5.Sum(body)
	require.Equal(t, `"`+hex.EncodeToString(sum[:])+`"`, etag)
}

// TestKeysWithSlashesAreNotDirectories is the flat-map claim, made concrete.
//
// There is no mkdir, no "a/" object is created, and deleting the only key under a prefix makes the prefix
// vanish. A console that shows folders is computing them from the delimiter on every request.
func TestKeysWithSlashesAreNotDirectories(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	for _, key := range []string{"reports/2024/q1.csv", "reports/2024/q2.csv", "reports/2025/q1.csv", "notes.txt"} {
		_, err := Put(ctx, client, name, key, []byte("x"), "text/csv")
		require.NoError(t, err)
	}

	// No delimiter: every key, prefixes and all, as one flat list.
	flat, err := List(ctx, client, name, "", "", 0)
	require.NoError(t, err)
	require.Len(t, flat, 1, "four keys fit in one page")
	require.Len(t, flat[0].Keys, 4)
	require.Empty(t, flat[0].CommonPrefixes, "without a delimiter S3 has nothing to fold")

	// With a delimiter: the folder illusion. Keys at the top level come back as keys, and anything deeper is
	// folded into a common prefix. The prefixes are computed per request, not stored.
	folded, err := List(ctx, client, name, "", "/", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"notes.txt"}, folded[0].Keys)
	require.Equal(t, []string{"reports/"}, folded[0].CommonPrefixes)

	// And one level down, the same call with a longer prefix.
	inner, err := List(ctx, client, name, "reports/", "/", 0)
	require.NoError(t, err)
	require.Empty(t, inner[0].Keys, "no key is exactly reports/<something> without another slash")
	require.Equal(t, []string{"reports/2024/", "reports/2025/"}, inner[0].CommonPrefixes)
}

// TestListPagesAtMaxKeys counts the requests a listing takes.
//
// That count is the machine-independent number: 7 keys at 3 per page is 3 requests everywhere. It is also the
// number that surprises people, because a bucket with a million objects is a thousand round trips and a
// listing-driven design is slow for that reason and no other.
func TestListPagesAtMaxKeys(t *testing.T) {
	cfg := awstest.Config(t)
	cfg, counter := awstest.WithCounter(cfg)

	client := New(cfg)
	name := awstest.Name(t, "s3")

	ctx := context.Background()

	_, err := CreateBucket(ctx, client, name)
	require.NoError(t, err)

	t.Cleanup(func() { _ = DeleteBucket(context.Background(), client, name) })

	for i := range 7 {
		_, err := Put(ctx, client, name, fmt.Sprintf("k/%02d", i), []byte("x"), "text/plain")
		require.NoError(t, err)
	}

	counter.Reset()

	start := time.Now()

	pages, err := List(ctx, client, name, "k/", "", 3)
	require.NoError(t, err)

	elapsed := time.Since(start)

	require.Len(t, pages, 3, "7 keys at 3 per page")
	require.Len(t, pages[0].Keys, 3)
	require.Len(t, pages[1].Keys, 3)
	require.Len(t, pages[2].Keys, 1)

	// The assertion is the request count. The duration is context.
	require.Equal(t, 3, counter.Total(), "one HTTP request per page, no more: %v", counter.Operations())
	t.Logf("3 pages over 7 keys in %v", elapsed)
}

// TestPresignedURLWorksWithoutCredentials is the point of presigning.
//
// A bare http.Client, no SDK, no credentials, gets the object. That is what you hand to a browser.
func TestPresignedURLWorksWithoutCredentials(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	body := []byte("signed and delivered")
	_, err := Put(ctx, client, name, "secret.txt", body, "text/plain")
	require.NoError(t, err)

	url, err := PresignGet(ctx, client, name, "secret.txt", 5*time.Minute)
	require.NoError(t, err)

	require.Contains(t, url, "X-Amz-Signature=", "the signature rides in the query string")
	require.Contains(t, url, "X-Amz-Expires=300", "the expiry is in the URL, so it cannot be changed without breaking the signature")

	resp, err := http.Get(url) //nolint:noctx // a two-line demo of the URL working with no SDK at all
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, body, got)
}

// TestPresigningMakesNoNetworkCall is the fact that makes presigned URLs cheap.
//
// Signing is HMAC over a canonical request. Nothing is registered, so nothing is asked. A server can mint one
// per request without a round trip, which is the whole reason the pattern scales.
func TestPresigningMakesNoNetworkCall(t *testing.T) {
	cfg := awstest.Config(t)
	cfg, counter := awstest.WithCounter(cfg)

	client := New(cfg)

	counter.Reset()

	url, err := PresignGet(context.Background(), client, "any-bucket", "any-key", time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, url)

	require.Equal(t, 0, counter.Total(),
		"presigning is local HMAC; the bucket does not even have to exist: %v", counter.Operations())
}

// TestPresignedPutUploadsFromNothing is the browser-upload half.
func TestPresignedPutUploadsFromNothing(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	url, err := PresignPut(ctx, client, name, "from-browser.txt", 5*time.Minute)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader("uploaded directly"))
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	got, err := Get(ctx, client, name, "from-browser.txt")
	require.NoError(t, err)
	require.Equal(t, "uploaded directly", string(got))
}

// TestPresignedURLExpires shows that the expiry is enforced by S3, not by the client.
func TestPresignedURLExpires(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	_, err := Put(ctx, client, name, "short-lived.txt", []byte("x"), "text/plain")
	require.NoError(t, err)

	// One second, then wait it out. This is the one place in this module where a sleep is the subject rather
	// than a smell: the assertion is about wall-clock expiry.
	url, err := PresignGet(ctx, client, name, "short-lived.txt", 1*time.Second)
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	resp, err := http.Get(url) //nolint:noctx // no SDK on purpose
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusForbidden, resp.StatusCode, "an expired signature is a 403, not a 404")

	// The code is AccessDenied, the same code as a permissions failure. Only the message distinguishes them,
	// which is why a support ticket about a presigned URL always starts with someone checking IAM policies for
	// an hour. The expiry and the server's clock are both in the body.
	body, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(body), "<Code>AccessDenied</Code>")
	require.Contains(t, string(body), "Request has expired", "S3 says which check failed: %s", body)
}

// TestPutIfAbsentIsALock is S3 as a mutual exclusion primitive.
func TestPutIfAbsentIsALock(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	first, err := PutIfAbsent(ctx, client, name, "lock", []byte("holder-a"))
	require.NoError(t, err)
	require.True(t, first, "the first writer takes the lock")

	second, err := PutIfAbsent(ctx, client, name, "lock", []byte("holder-b"))
	require.NoError(t, err)
	require.False(t, second, "the second writer is refused rather than overwriting")

	// And the refusal left the original alone. A plain PutObject would have replaced it.
	got, err := Get(ctx, client, name, "lock")
	require.NoError(t, err)
	require.Equal(t, "holder-a", string(got))
}

// TestPlainPutOverwritesSilently is the contrast that makes the previous test mean something.
func TestPlainPutOverwritesSilently(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	_, err := Put(ctx, client, name, "doc", []byte("version one"), "text/plain")
	require.NoError(t, err)

	_, err = Put(ctx, client, name, "doc", []byte("version two"), "text/plain")
	require.NoError(t, err, "S3 does not warn, does not version, and does not ask")

	got, err := Get(ctx, client, name, "doc")
	require.NoError(t, err)
	require.Equal(t, "version two", string(got))
}

// TestIfNoneMatchIsACacheRevalidation shows the 304 path and the etag change.
func TestIfNoneMatchIsACacheRevalidation(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	etag, err := Put(ctx, client, name, "cached.json", []byte(`{"v":1}`), "application/json")
	require.NoError(t, err)

	body, unchanged, err := GetIfNoneMatch(ctx, client, name, "cached.json", etag)
	require.NoError(t, err)
	require.True(t, unchanged, "the etag still matches, so S3 sends no body")
	require.Nil(t, body)

	// Change the object. The etag changes with it, which is what makes it usable as a version.
	newETag, err := Put(ctx, client, name, "cached.json", []byte(`{"v":2}`), "application/json")
	require.NoError(t, err)
	require.NotEqual(t, etag, newETag)

	body, unchanged, err = GetIfNoneMatch(ctx, client, name, "cached.json", etag)
	require.NoError(t, err)
	require.False(t, unchanged)
	require.JSONEq(t, `{"v":2}`, string(body))
}

// TestMissingKeyHasATypedCode pins the error contract.
func TestMissingKeyHasATypedCode(t *testing.T) {
	client, name := bucket(t)

	_, err := Get(context.Background(), client, name, "never-written")
	require.Error(t, err)

	require.Equal(t, "NoSuchKey", APIErrorCode(err), "match the code, never the message")
	require.Equal(t, 404, StatusCode(err))
}

// TestMultipartETagIsNotTheMD5 is the etag caveat, demonstrated.
//
// A multipart etag is the MD5 of the concatenated part MD5s, then a hyphen and the part count. So
// "-3" at the end tells you the object went up in three parts, and comparing it to the MD5 of the file gives
// the wrong answer for a file that is byte-identical.
func TestMultipartETagIsNotTheMD5(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	// 5 MiB is the minimum size for every part but the last, so a three-part upload needs at least 10 MiB
	// plus a remainder. This is the rule that makes "just split it into ten pieces" fail on a small file.
	const partSize = 5 << 20

	body := make([]byte, partSize*2+1024)
	for i := range body {
		body[i] = byte(i % 251)
	}

	etag, parts, err := Multipart(ctx, client, name, "big.bin", body, partSize)
	require.NoError(t, err)
	require.Equal(t, 3, parts)

	require.True(t, strings.HasSuffix(etag, `-3"`), "the part count is appended to the etag: %s", etag)

	sum := md5.Sum(body)
	require.NotContains(t, etag, hex.EncodeToString(sum[:]),
		"a multipart etag is not the object's MD5, so it cannot be used as a content hash")

	// The object is whole and correct regardless.
	got, err := Get(ctx, client, name, "big.bin")
	require.NoError(t, err)
	require.Equal(t, len(body), len(got))
	require.Equal(t, body, got)
}

// TestPartTooSmallIsRejected is the 5 MiB floor.
func TestPartTooSmallIsRejected(t *testing.T) {
	client, name := bucket(t)

	body := make([]byte, 3000)

	_, _, err := Multipart(context.Background(), client, name, "too-small.bin", body, 1000)
	require.Error(t, err)
	require.Equal(t, "EntityTooSmall", APIErrorCode(err),
		"every part but the last must be at least 5 MiB, and the check happens at Complete")
}

// TestMultipartCountsRequests shows what multipart costs in round trips.
//
// Four parts is 4 UploadPart calls plus Create and Complete, so six. That is the trade: more requests, smaller
// retries. It is the reason multipart is wrong for small objects and right for large ones.
func TestMultipartCountsRequests(t *testing.T) {
	cfg := awstest.Config(t)
	cfg, counter := awstest.WithCounter(cfg)

	client := New(cfg)
	name := awstest.Name(t, "s3")

	ctx := context.Background()

	_, err := CreateBucket(ctx, client, name)
	require.NoError(t, err)

	t.Cleanup(func() { _ = DeleteBucket(context.Background(), client, name) })

	const partSize = 5 << 20

	body := make([]byte, partSize*3+10)

	counter.Reset()

	_, parts, err := Multipart(ctx, client, name, "four.bin", body, partSize)
	require.NoError(t, err)
	require.Equal(t, 4, parts)

	require.Equal(t, 6, counter.Total(),
		"CreateMultipartUpload + 4 UploadPart + CompleteMultipartUpload: %v", counter.Operations())
}

// TestCopyIsTheOnlyRename shows that moving an object is a copy plus a delete.
func TestCopyIsTheOnlyRename(t *testing.T) {
	client, name := bucket(t)
	ctx := context.Background()

	_, err := Put(ctx, client, name, "old/name.txt", []byte("contents"), "text/plain")
	require.NoError(t, err)

	// The source is "bucket/key" in one string and it has to be URL-escaped. A key with a space or a plus in
	// it breaks this if you build the string by hand, which is the classic CopyObject bug.
	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(name),
		Key:        aws.String("new/name.txt"),
		CopySource: aws.String(name + "/old/name.txt"),
	})
	require.NoError(t, err)

	// Both exist now. There is no rename, so the old one is still there until you delete it, and a "move" that
	// crashes in between leaves two copies.
	oldBody, err := Get(ctx, client, name, "old/name.txt")
	require.NoError(t, err)

	newBody, err := Get(ctx, client, name, "new/name.txt")
	require.NoError(t, err)
	require.Equal(t, oldBody, newBody)
}

// cancelAfterPart cancels a context as soon as the response to part 1 of a multipart upload arrives, which is
// the shape of a client disconnecting halfway through an upload.
type cancelAfterPart struct {
	inner  aws.HTTPClient
	cancel context.CancelFunc
}

func (c cancelAfterPart) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.inner.Do(req)
	if req.URL.Query().Get("partNumber") == "1" {
		c.cancel()
	}

	return resp, err
}

// TestACancelledUploadIsStillAborted is the abort that has to outlive the context. The upload stops because
// ctx was cancelled; an abort sent on that same ctx fails before it leaves the process, and the parts stay in
// the bucket, invisible and billed.
func TestACancelledUploadIsStillAborted(t *testing.T) {
	client, name := bucket(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := awstest.Config(t)
	cfg.HTTPClient = cancelAfterPart{inner: &http.Client{Timeout: 30 * time.Second}, cancel: cancel}

	const partSize = 5 << 20

	_, _, err := Multipart(ctx, New(cfg), name, "abandoned.bin", make([]byte, partSize*2), partSize)
	require.ErrorIs(t, err, context.Canceled)

	out, err := client.ListMultipartUploads(context.Background(), &s3.ListMultipartUploadsInput{Bucket: aws.String(name)})
	require.NoError(t, err)
	require.Empty(t, out.Uploads, "an upload left open keeps its parts, and S3 bills for them")
}

// TestPartSizeMustBePositive is the loop that never advanced: a part of zero bytes moves the offset by zero.
func TestPartSizeMustBePositive(t *testing.T) {
	client, name := bucket(t)

	for _, size := range []int{0, -1} {
		_, _, err := Multipart(context.Background(), client, name, "x.bin", []byte("abc"), size)
		require.ErrorIs(t, err, ErrBadPartSize)
	}

	out, err := client.ListMultipartUploads(context.Background(), &s3.ListMultipartUploadsInput{Bucket: aws.String(name)})
	require.NoError(t, err)
	require.Empty(t, out.Uploads, "the size is checked before an upload is started")
}
