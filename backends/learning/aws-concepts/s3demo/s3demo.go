// Package s3demo is object storage: buckets, objects, presigned URLs and the conditional writes that make S3
// safe to use from more than one process.
//
// # The one thing to carry away
//
// S3 is not a filesystem. It has no directories, no rename, no append, and until 2020 no read-after-write
// consistency for overwrites. What it has is a flat map from key to bytes, with a strongly consistent
// single-key read and a conditional write. Every "folder" in a console is a prefix and a delimiter, computed
// per request.
//
// Once that lands, the rest of the API stops being surprising. ListObjectsV2 pages because the map can be
// enormous. CopyObject exists because there is no rename. Multipart exists because a single PUT has to hold the
// whole object. And the etag is the lever for every concurrency problem, because it is the only thing S3 will
// compare against.
package s3demo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// New builds an S3 client for LocalStack.
//
// # UsePathStyle is not optional here
//
// The modern S3 addressing is virtual-hosted: the bucket becomes a subdomain, so an object lands at
// https://my-bucket.s3.us-east-1.amazonaws.com/key. Point that at LocalStack and the client asks DNS for
// my-bucket.localhost, which does not resolve on most machines.
//
// Path style puts the bucket in the path instead: http://localhost:4566/my-bucket/key. AWS deprecated it for
// real S3 and kept it working; every S3-compatible server (LocalStack, MinIO, Ceph) needs it.
func New(cfg aws.Config) *s3.Client {
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})
}

// CreateBucket makes a bucket and reports whether it was new.
//
// # Why the error is swallowed and re-inspected
//
// CreateBucket on a bucket you already own returns BucketAlreadyOwnedByYou, which is an error the SDK surfaces
// and a condition most callers do not care about. Treating it as success is right for setup code and wrong for
// anything that needs to know it lost a race, so this returns the fact rather than hiding it.
//
// The other one, BucketAlreadyExists, means someone ELSE owns it. Bucket names are global across every AWS
// account on earth, which is the single most surprising fact about S3 and the reason production bucket names
// have a company prefix.
//
// # The us-east-1 exception, which the test found
//
// In us-east-1 ONLY, re-creating a bucket you already own returns 200 OK. Everywhere else it returns
// BucketAlreadyOwnedByYou. This is a compatibility wart from before the error existed and it has never been
// fixed, so `created` is truthful in every region but the one this module runs in. Code that genuinely needs to
// know whether it made the bucket has to call HeadBucket first, and then it has a race.
func CreateBucket(ctx context.Context, client *s3.Client, bucket string) (created bool, err error) {
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return true, nil
	}

	var owned *types.BucketAlreadyOwnedByYou
	if errors.As(err, &owned) {
		return false, nil
	}

	return false, err
}

// Put writes an object and returns its etag.
//
// # What the etag is
//
// For a single-part upload of an unencrypted object it is the MD5 of the body, in hex, in quotes. That is not
// documented as a guarantee and multipart breaks it (see MultipartETag), so treat it as an opaque version token.
// The useful part is that it CHANGES when the object changes, which is what the conditional writes below need.
func Put(ctx context.Context, client *s3.Client, bucket, key string, body []byte, contentType string) (etag string, err error) {
	out, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", err
	}

	return aws.ToString(out.ETag), nil
}

// Get reads an object whole.
//
// GetObject returns a body that is an open HTTP response. Not closing it leaks a connection, and the SDK will
// not close it for you, because it cannot know whether you wanted to stream. This is the most common S3 bug in
// Go code and it shows up as a connection pool that stops handing out connections under load.
func Get(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}

	defer func() { _ = out.Body.Close() }()

	return io.ReadAll(out.Body)
}

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("s3demo: object not found")

// GetIfNoneMatch reads an object only if its etag has changed since the one you saw.
//
// # The read half of optimistic concurrency
//
// A client that caches an object wants "give it to me only if it changed" or "only if it has not". IfNoneMatch
// with a cached etag returns 304 and no body, which is what a CDN does. IfMatch is the other direction and
// returns 412 when the object moved on.
//
// The SDK models 304 as an error, not as a response with a status. That is consistent (any non-2xx is an error)
// and catches people out, because a 304 is the SUCCESS case for a cache.
func GetIfNoneMatch(ctx context.Context, client *s3.Client, bucket, key, etag string) (body []byte, unchanged bool, err error) {
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		IfNoneMatch: aws.String(etag),
	})
	if err != nil {
		if StatusCode(err) == 304 {
			return nil, true, nil
		}

		return nil, false, err
	}

	defer func() { _ = out.Body.Close() }()

	b, err := io.ReadAll(out.Body)

	return b, false, err
}

// PutIfAbsent writes an object only if the key does not already exist.
//
// # S3 as a lock
//
// This is the newest useful thing in S3 (2024) and it removes a whole category of workaround. Before it, "create
// this object only if nobody else has" needed DynamoDB or a lease, because a PUT always won. `If-None-Match: *`
// makes the write conditional on the key being absent and returns 412 PreconditionFailed otherwise.
//
// That is enough to build a lock, a leader election, or an idempotent writer, all with no second service.
func PutIfAbsent(ctx context.Context, client *s3.Client, bucket, key string, body []byte) (wrote bool, err error) {
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		IfNoneMatch: aws.String("*"),
	})
	if err == nil {
		return true, nil
	}

	if StatusCode(err) == 412 {
		return false, nil
	}

	return false, err
}

// StatusCode digs the HTTP status out of an SDK error, or returns 0.
//
// # Why this helper has to exist
//
// The SDK wraps errors several layers deep: an operation error around a response error around a generic HTTP
// response error. Type-asserting the top one gets you nothing. errors.As walks the chain, and the interface to
// look for is smithy.HTTPError, which every service's response error implements.
//
// This matters because half of S3's interesting behaviour is expressed as a status code and not as a typed
// error. 304 and 412 have no Go type in the s3 package at all.
func StatusCode(err error) int {
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) {
		return httpErr.HTTPStatusCode()
	}

	return 0
}

// APIErrorCode returns the AWS error code string, or "".
//
// Codes like "NoSuchKey" and "PreconditionFailed" are the stable contract. The message is not: it is prose and
// it changes. Matching on a message is how a test starts failing on an SDK upgrade that changed nothing.
func APIErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}

	return ""
}

// ListPage is one page of a listing.
type ListPage struct {
	Keys           []string
	CommonPrefixes []string
}

// List walks every object under a prefix and returns the pages it took.
//
// # Why a paginator and not a loop
//
// ListObjectsV2 returns at most 1000 keys and a continuation token. Writing that loop by hand is four lines and
// one of them is wrong the first time, because the exit condition is IsTruncated and not "the token is empty"
// in the V1 API, and because reassigning the input between calls is easy to forget.
//
// The paginator is the SDK's answer and every paging API has one. The pages are returned here so a test can
// assert HOW MANY requests a listing took, which is the number that changes when MaxKeys changes.
func List(ctx context.Context, client *s3.Client, bucket, prefix, delimiter string, maxKeys int32) (pages []ListPage, err error) {
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	}

	if delimiter != "" {
		input.Delimiter = aws.String(delimiter)
	}

	if maxKeys > 0 {
		input.MaxKeys = aws.Int32(maxKeys)
	}

	p := s3.NewListObjectsV2Paginator(client, input)

	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		page := ListPage{}

		for _, o := range out.Contents {
			page.Keys = append(page.Keys, aws.ToString(o.Key))
		}

		for _, cp := range out.CommonPrefixes {
			page.CommonPrefixes = append(page.CommonPrefixes, aws.ToString(cp.Prefix))
		}

		pages = append(pages, page)
	}

	return pages, nil
}

// PresignGet produces a URL that downloads an object with no credentials.
//
// # What a presigned URL is
//
// It is a normal GET with the signature in the query string instead of the Authorization header. Nothing is
// registered server side. The URL carries the signing key's identity, the expiry, and a signature over the
// request, and S3 recomputes the signature when the URL is used.
//
// Three consequences that catch people:
//
//   - It cannot be revoked. Shortening the expiry is the only control, so an hour is not a default, it is a
//     decision.
//   - It inherits the permissions of whoever signed it. Signing with an admin role hands out admin's read
//     access to that one object.
//   - Generating one makes NO network call. The test for this asserts exactly that, because it is the fact that
//     makes presigned URLs cheap enough to mint per request.
func PresignGet(ctx context.Context, client *s3.Client, bucket, key string, expires time.Duration) (string, error) {
	ps := s3.NewPresignClient(client, func(o *s3.PresignOptions) {
		o.Expires = expires
	})

	req, err := ps.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}

	return req.URL, nil
}

// PresignPut produces a URL that uploads an object with no credentials.
//
// This is how a browser uploads straight to S3 without the bytes passing through your server. The server signs
// a URL, the browser PUTs to it. Your bandwidth bill is one small JSON response instead of the file.
func PresignPut(ctx context.Context, client *s3.Client, bucket, key string, expires time.Duration) (string, error) {
	ps := s3.NewPresignClient(client, func(o *s3.PresignOptions) {
		o.Expires = expires
	})

	req, err := ps.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}

	return req.URL, nil
}

// ErrBadPartSize is returned for a part size that is not positive.
var ErrBadPartSize = errors.New("s3demo: part size must be positive")

// Multipart uploads a body in parts and returns the etag.
//
// # When this is required and when it is a choice
//
// Required above 5 GiB, because that is the hard limit on a single PutObject. A choice below that, and the
// reasons are retry granularity (a failed part is retried, not the whole object) and parallelism (parts go up
// at once).
//
// The part size floor is 5 MiB for every part except the last, which is why a naive "split into 100 pieces"
// fails on a small file. This function takes the size so a test can show the rule.
//
// # The checksum that the SDK now sends without being asked
//
// Since the 2025 "data integrity protections" change, the SDK computes a CRC32 for every upload and sends it.
// For a multipart upload that has to be declared UP FRONT, on CreateMultipartUpload, because the server decides
// then how it will combine the parts' checksums. Leaving it off and letting the parts carry one is the mismatch
// `Checksum Type mismatch occurred, expected checksum Type: null, actual checksum Type: crc32`, which is what
// this code did before the test found it.
//
// So the algorithm is declared at the start, and each part's checksum is carried into CompletedPart. Real S3 is
// more forgiving than LocalStack here, which makes this exactly the kind of thing that passes in a test suite
// against AWS and fails against an emulator, or the reverse.
//
// # The abort that everyone forgets
//
// A multipart upload that is started and never completed leaves the parts in the bucket, invisible to
// ListObjectsV2, and billed. Every production bucket wants a lifecycle rule that aborts incomplete uploads after
// a few days. The deferred abort here handles the process-crash case badly (it does not run) and the
// error-return case well, which is the honest state of affairs.
//
// The abort runs on context.WithoutCancel(ctx), not ctx. The commonest reason to abandon an upload is that ctx
// was cancelled, a client hung up or a deadline passed, and a request sent on a cancelled ctx fails before it
// leaves the process. The abort would fail in exactly the case it exists for.
func Multipart(ctx context.Context, client *s3.Client, bucket, key string, body []byte, partSize int) (etag string, parts int, err error) {
	// A part size of zero never moves the offset, so the loop below would upload empty parts forever.
	if partSize <= 0 {
		return "", 0, fmt.Errorf("%w: %d", ErrBadPartSize, partSize)
	}

	start, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(key),
		ChecksumAlgorithm: types.ChecksumAlgorithmCrc32,
	})
	if err != nil {
		return "", 0, err
	}

	uploadID := start.UploadId

	completed := false

	defer func() {
		if completed {
			return
		}

		abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()

		if _, abortErr := client.AbortMultipartUpload(abortCtx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: uploadID,
		}); abortErr != nil {
			err = errors.Join(err, fmt.Errorf("abort upload %s: %w", aws.ToString(uploadID), abortErr))
		}
	}()

	var finished []types.CompletedPart

	for offset, number := 0, int32(1); offset < len(body); number++ {
		end := min(offset+partSize, len(body))

		out, err := client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   uploadID,
			PartNumber: aws.Int32(number),
			Body:       bytes.NewReader(body[offset:end]),
		})
		if err != nil {
			return "", 0, fmt.Errorf("part %d: %w", number, err)
		}

		finished = append(finished, types.CompletedPart{
			ETag:          out.ETag,
			PartNumber:    aws.Int32(number),
			ChecksumCRC32: out.ChecksumCRC32,
		})

		offset = end
	}

	done, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		UploadId:        uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: finished},
	})
	if err != nil {
		return "", 0, err
	}

	completed = true

	return aws.ToString(done.ETag), len(finished), nil
}

// DeleteBucket empties a bucket and removes it.
//
// # There is no recursive delete
//
// DeleteBucket fails with BucketNotEmpty if anything is in it, and S3 offers no "delete everything under this
// prefix". You list, then you delete in batches of up to 1000. Every "rm -rf" for S3 is this loop, including the
// one in the AWS CLI.
func DeleteBucket(ctx context.Context, client *s3.Client, bucket string) error {
	p := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})

	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return err
		}

		if len(out.Contents) == 0 {
			continue
		}

		ids := make([]types.ObjectIdentifier, 0, len(out.Contents))
		for _, o := range out.Contents {
			ids = append(ids, types.ObjectIdentifier{Key: o.Key})
		}

		// Quiet suppresses the per-key success list in the response, which for 1000 keys is most of the
		// payload. The errors still come back.
		if _, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		}); err != nil {
			return err
		}
	}

	_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})

	return err
}
