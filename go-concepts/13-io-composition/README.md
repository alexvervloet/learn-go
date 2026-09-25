# io composition

> 📚 [go-concepts](../README.md) · **Step 13 of 18** · [⬅ 12-struct-tags-and-reflection](../12-struct-tags-and-reflection/) · Next: [14-embed-and-build-tags](../14-embed-and-build-tags/) ➡

## What is this?

Two one-method interfaces, and everything built on them.

```go
type Reader interface { Read(p []byte) (n int, err error) }
type Writer interface { Write(p []byte) (n int, err error) }
```

Lesson 03 showed why one method matters: a file, a socket, an HTTP body, a
gzip stream, a hash and a byte buffer all satisfy these, so a function taking
`io.Reader` works with all of them. This lesson is about what you build once
everything speaks the same two interfaces.

Python has file-like objects and the same idea, informally. Go's version is
enforced by the compiler and the standard library composes aggressively around
it.

## The Read contract, and the bug everyone writes

`Read` fills `p` and returns how many bytes it wrote. The part people get wrong:

> **Read can return `n > 0` and `err == io.EOF` at the same time.**

So this loses the last chunk of every file that ends without a trailing read:

```go
for {
    n, err := r.Read(buf)
    if err != nil {
        break          // WRONG: drops the n bytes just read
    }
    process(buf[:n])
}
```

The rule is **process `n` bytes first, then check the error**:

```go
for {
    n, err := r.Read(buf)
    if n > 0 {
        process(buf[:n])
    }
    if err != nil {
        if errors.Is(err, io.EOF) {
            break      // not a failure: the reader finished
        }
        return err
    }
}
```

Two more contract details worth knowing. A `Read` returning `0, nil` is legal
and means nothing happened; callers must not treat it as EOF, and implementers
should avoid returning it. And `io.EOF` is **not an error condition**: it is how
a reader says it is done. Wrapping it in "read failed" context is a beginner
bug that makes logs lie.

## The Write contract

`Write` must write all of `p` or return an error. A short write with a nil error
breaks the contract, and `io.Copy` reports `io.ErrShortWrite` when it sees one.
That is why `countingWriter` in lesson 03 returns the byte count it was given
rather than the count it forwarded.

## `io.Copy` and its fast paths

```go
io.Copy(dst, src)
```

This is not a naive loop. It asks two questions first:

1. Does `src` implement `WriterTo`? If so, call `src.WriteTo(dst)`.
2. Does `dst` implement `ReaderFrom`? If so, call `dst.ReadFrom(src)`.

Only if both fail does it allocate a 32KB buffer and loop. This is why copying
a `*os.File` to a `net.TCPConn` on Linux can become a `sendfile` syscall with no
bytes crossing into user space at all, and why `io.Copy` is almost always faster
than a hand-written loop.

`io.CopyBuffer` lets you supply the buffer, and is worth it only when you are
copying many times and the 32KB allocation shows up in a profile.

Measured on 64KB of data, M2 Max:

| | ns/op | B/op |
|---|---|---|
| fast path (`strings.Reader` has `WriteTo`) | 8618 | 131128 |
| slow path (same reader, wrapped so `WriteTo` is hidden) | 11063 | 147528 |
| slow path with `io.CopyBuffer` and a reused buffer | 8949 | 114760 |

The fast path is 1.3x quicker and the reused buffer recovers most of that, which
is the shape to expect: this is an in-memory copy, so the gap is the buffer
management. The dramatic case is a file to a socket on Linux, where the fast
path becomes a `sendfile` syscall and the bytes never enter user space at all.

## The composition toolkit

| Function | What it gives you |
|---|---|
| `io.MultiReader(a, b, c)` | One reader that reads a, then b, then c |
| `io.MultiWriter(a, b, c)` | One writer that writes to all three |
| `io.TeeReader(r, w)` | A reader that also writes everything it reads to `w` |
| `io.LimitReader(r, n)` | Stops after n bytes, then reports EOF |
| `io.SectionReader(r, off, n)` | A window over a `ReaderAt` |
| `io.Pipe()` | An in-memory pipe: a `Writer` feeding a `Reader` |
| `io.Discard` | A `Writer` that throws everything away |
| `io.NopCloser(r)` | Adds a no-op `Close` to a `Reader` |

`TeeReader` is the one worth internalising: hashing a stream while uploading it,
or logging a request body while decoding it, is one line and no extra pass.

## `bufio`, and when it matters

`bufio.Reader` and `bufio.Writer` batch small operations into large ones. For a
file or socket where each `Read` is a syscall, wrapping in `bufio` is the
difference between one syscall per byte and one per 4KB.

Reading 4096 bytes one at a time: **4097 `Read` calls unbuffered, 2 buffered.**
Writing 4096 single bytes: **4096 `Write` calls unbuffered, 1 buffered.**

The wall-clock benchmark here shows only 1.8x, and that number understates the
real benefit considerably, on purpose: the reader in `buffered.go` is a plain
Go function, not a syscall. A real `read(2)` costs a kernel transition, so the
gap on a file or socket is orders of magnitude rather than a factor of two. The
call counts are the honest measurement; the timing is a lower bound.

`bufio.Scanner` is the convenient line reader, with one sharp edge: it has a
**maximum token size, 64KB by default**, and a line longer than that stops the
scan with `bufio.ErrTooLong`. Code that ignores `scanner.Err()` silently
processes half a file. Always check it.

## `io.Pipe`

`io.Pipe` connects a writer to a reader in memory, with no buffer: every `Write`
blocks until a `Read` consumes it. It is how you feed a function that wants an
`io.Reader` from code that produces output by writing.

Because it is unbuffered and synchronous, one side must run in its own
goroutine, and the writer **must** be closed or the reader blocks forever.
`CloseWithError` propagates a failure to the reader, which is how the consumer
learns the producer gave up rather than finished.

## Reading untrusted input

`io.ReadAll` on a request body is an unbounded memory allocation controlled by
whoever is sending it. Use `http.MaxBytesReader` on a server, or
`io.LimitReader` anywhere else, and treat the limit as a requirement rather
than a nicety.

## What the files cover

| File | What it teaches |
|---|---|
| `contract.go` | The Read and Write contracts, the n-before-err bug, short writes |
| `copy.go` | `io.Copy`'s fast paths, `WriterTo`, `ReaderFrom`, `CopyBuffer` |
| `compose.go` | MultiReader, MultiWriter, TeeReader, LimitReader, SectionReader |
| `buffered.go` | `bufio` Reader/Writer/Scanner, the 64KB token limit |
| `pipes.go` | `io.Pipe`, closing, `CloseWithError`, feeding a Reader from a Writer |
| `custom.go` | Writing your own Reader and Writer, and the contracts to honour |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests including the lost-last-chunk bug, asserted |

## How to run

```bash
go run ./13-io-composition
go test ./13-io-composition
go test -bench . -benchmem -run '^$' ./13-io-composition
```
