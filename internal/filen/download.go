package filen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	partSuffix = ".part"

	// protocolChunkSize is the plaintext size of every chunk but the last, as
	// Filen's SDK defines it.
	protocolChunkSize = 1024 * 1024

	// chunkConcurrency is how many chunks are fetched at once. Chunks are
	// written in the order the file needs them whatever order they arrive in,
	// so this decides only how far ahead the fetching may run.
	chunkConcurrency = 8

	// chunkAttempts is how many times one chunk is fetched before the download
	// gives up, so a dropped connection does not cost every chunk already read.
	chunkAttempts = 3

	// retryBackoff is the wait before a second attempt, doubled for each after.
	retryBackoff = 500 * time.Millisecond

	// maxChunkBody bounds one encrypted chunk: a full chunk of plaintext plus
	// the nonce and tag wrapped around it. Reading a body without a bound would
	// let a server hand back gigabytes for what the protocol says is a megabyte.
	maxChunkBody = protocolChunkSize + nonceLength + tagLength
)

// Download writes f's decrypted bytes to path, through a ".part" file renamed
// only once every chunk has arrived and the byte count matches the size the
// metadata promised. An interrupted run therefore leaves nothing at path that a
// later run would mistake for a complete file.
func (c *Client) Download(ctx context.Context, f File, path string) error {
	if f.Version != dataVersion {
		return fmt.Errorf("%q: file data version %d: %w", f.Name, f.Version, ErrUnsupportedVersion)
	}
	if !chunksHoldSize(f.Chunks, f.Size, c.chunkSize) {
		return fmt.Errorf("%q: %d chunk(s) cannot hold %d bytes", f.Name, f.Chunks, f.Size)
	}
	part := path + partSuffix
	if err := c.writeChunks(ctx, f, part); err != nil {
		os.Remove(part)
		return err
	}
	if err := os.Rename(part, path); err != nil {
		os.Remove(part)
		return fmt.Errorf("rename %q: %w", part, err)
	}
	return nil
}

// chunksHoldSize reports whether a chunk count could have produced a size:
// every chunk but the last is full, so the last holds the remainder and cannot
// overflow a chunk of its own. The count drives the download loop, so a reply
// failing this is rejected before a byte is fetched rather than after.
func chunksHoldSize(chunks int, size, chunkSize int64) bool {
	if chunks < 0 || size < 0 {
		return false
	}
	if chunks == 0 {
		return size == 0
	}
	// Divide rather than multiplying (chunks-1)*chunkSize out, which overflows
	// for an absurd chunk count instead of rejecting it.
	full := int64(chunks - 1)
	if size/chunkSize < full {
		return false
	}
	return size-full*chunkSize <= chunkSize
}

// writeChunks fetches every chunk of f, appends them to path in order, and
// fails if the decrypted total is not the size the metadata promised.
//
// Chunks are fetched several at a time and written in the order the file needs
// them, so a slow chunk delays only the writing rather than the fetches queued
// behind it. Each fetch in flight owns a one-slot channel, which is what bounds
// how much decrypted data is held at once.
func (c *Client) writeChunks(ctx context.Context, f File, path string) (err error) {
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %q: %w", path, cerr)
		}
	}()

	// Cancelling on the way out is what releases the producer when a chunk
	// fails: it is otherwise parked on a send to a queue nothing is draining.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type fetched struct {
		data []byte
		err  error
	}
	queue := make(chan chan fetched, chunkConcurrency)
	go func() {
		defer close(queue)
		for index := range f.Chunks {
			slot := make(chan fetched, 1)
			select {
			case queue <- slot:
			case <-ctx.Done():
				return
			}
			go func() {
				// The slot is buffered, so this send always completes and the
				// goroutine exits even when nothing reads the result.
				data, err := c.chunk(ctx, f, index)
				slot <- fetched{data: data, err: err}
			}()
		}
	}()

	var written int64
	for slot := range queue {
		result := <-slot
		if result.err != nil {
			return result.err
		}
		n, werr := out.Write(result.data)
		if werr != nil {
			return fmt.Errorf("write %q: %w", path, werr)
		}
		written += int64(n)
	}
	if written != f.Size {
		// A cancelled run stops queueing chunks, so ask why the bytes ran out
		// before blaming the file: a deadline reported as a size mismatch
		// would name the wrong fault entirely.
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("%q: %w", f.Name, cerr)
		}
		return fmt.Errorf("%q: decrypted %d bytes, expected %d", f.Name, written, f.Size)
	}
	return nil
}

// chunk fetches one encrypted chunk and decrypts it under the file's own key,
// retrying a failure that a further attempt could plausibly clear.
func (c *Client) chunk(ctx context.Context, f File, index int) ([]byte, error) {
	backoff := retryBackoff
	var lastErr error
	for attempt := range chunkAttempts {
		body, retryable, err := c.fetchChunk(ctx, f, index)
		if err == nil {
			plaintext, derr := decryptChunk(body, f.Key)
			if derr != nil {
				return nil, fmt.Errorf("%q: chunk %d: %w", f.Name, index, derr)
			}
			return plaintext, nil
		}
		if !retryable {
			return nil, err
		}
		lastErr = err
		if attempt == chunkAttempts-1 {
			break
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("%q: chunk %d: %w", f.Name, index, ctx.Err())
		case <-timer.C:
		}
		backoff *= 2
	}
	return nil, lastErr
}

// fetchChunk makes one attempt at retrieving an encrypted chunk, reporting
// whether the failure is one a further attempt could clear. A dropped
// connection or a server-side error is; a 404 or a body that cannot be a chunk
// is not, since another attempt cannot change the answer.
func (c *Client) fetchChunk(ctx context.Context, f File, index int) ([]byte, bool, error) {
	// Escape the path elements: region, bucket and uuid all come from the
	// gateway's reply rather than from the link the user typed.
	chunkURL := fmt.Sprintf("%s/%s/%s/%s/%d", c.egest,
		url.PathEscape(f.Region), url.PathEscape(f.Bucket), url.PathEscape(f.UUID), index)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chunkURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("%q: chunk %d: %w", f.Name, index, err)
	}
	response, err := c.http.Do(req)
	if err != nil {
		// A cancelled run is not a transient failure: retrying it would wait
		// out the backoff only to fail the same way.
		return nil, ctx.Err() == nil, fmt.Errorf("%q: chunk %d: %w", f.Name, index, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		retryable := response.StatusCode >= http.StatusInternalServerError ||
			response.StatusCode == http.StatusTooManyRequests
		return nil, retryable, fmt.Errorf("%q: chunk %d: http %s", f.Name, index, response.Status)
	}
	// GCM authenticates a chunk as a whole, so it has to be held in full before
	// any of it can be trusted. Read one byte past the limit, so a body too
	// large to be a chunk is caught rather than quietly truncated to fit.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxChunkBody+1))
	if err != nil {
		return nil, ctx.Err() == nil, fmt.Errorf("%q: chunk %d: %w", f.Name, index, err)
	}
	if len(body) > maxChunkBody {
		return nil, false, fmt.Errorf("%q: chunk %d: larger than a chunk can be", f.Name, index)
	}
	return body, false, nil
}
