package filen

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fetchFirst lists a fake share and returns the one file the test cares about.
func fetchFirst(t *testing.T, client *Client, key string) File {
	t.Helper()

	files, err := client.Files(t.Context(), Link{Kind: FolderLink, UUID: "share", Key: key})
	if err != nil {
		t.Fatalf("Files returned error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("Files returned nothing")
	}
	return files[0]
}

func TestDownload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data []byte
	}{
		{name: "smaller than one chunk", data: []byte("tiny")},
		{name: "exactly one chunk", data: bytes.Repeat([]byte("a"), testChunkSize)},
		{name: "several chunks with a partial last one", data: bytes.Repeat([]byte("xy"), testChunkSize*2+3)},
		{name: "empty file", data: []byte{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := newShareServer(t, testFolderKey, []testFile{
				{name: "payload.bin", key: testFileKey, data: tc.data},
			})
			client := newTestClient(server)
			file := fetchFirst(t, client, testFolderKey)

			target := filepath.Join(t.TempDir(), "payload.bin")
			if err := client.Download(t.Context(), file, target); err != nil {
				t.Fatalf("Download returned error: %v", err)
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("read downloaded file: %v", err)
			}
			if !bytes.Equal(got, tc.data) {
				t.Errorf("downloaded %q, want %q", got, tc.data)
			}
			assertNoPartFile(t, target)
		})
	}
}

func TestDownloadRejectsUnsupportedDataVersion(t *testing.T) {
	t.Parallel()
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "payload.bin", key: testFileKey, data: []byte("contents")},
	})
	client := newTestClient(server)
	file := fetchFirst(t, client, testFolderKey)
	file.Version = 3

	target := filepath.Join(t.TempDir(), "payload.bin")
	if err := client.Download(t.Context(), file, target); !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("Download error = %v, want ErrUnsupportedVersion", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("Download wrote a file for a version it does not understand")
	}
}

func TestDownloadLeavesNothingBehindOnFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		corrupt func(f *File)
		want    string
	}{
		{
			name:    "a chunk the egest does not have",
			corrupt: func(f *File) { f.Chunks++ },
			want:    "404",
		},
		{
			name:    "the wrong file key",
			corrupt: func(f *File) { f.Key = strings.Repeat("z", keyLength) },
			want:    "decrypt",
		},
		{
			name:    "a chunk that comes back short",
			corrupt: func(f *File) { f.Size-- },
			want:    "expected",
		},
		{
			name:    "more chunks than the size could fill",
			corrupt: func(f *File) { f.Size += 100 },
			want:    "cannot hold",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := newShareServer(t, testFolderKey, []testFile{
				{name: "payload.bin", key: testFileKey, data: bytes.Repeat([]byte("z"), testChunkSize*2)},
			})
			client := newTestClient(server)
			file := fetchFirst(t, client, testFolderKey)
			tc.corrupt(&file)

			target := filepath.Join(t.TempDir(), "payload.bin")
			err := client.Download(t.Context(), file, target)
			if err == nil {
				t.Fatal("Download returned nil error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
			// Nothing at the destination and no leftover .part: a later run must
			// not mistake a half-written file for a complete one.
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Error("a failed download left a file at the destination")
			}
			assertNoPartFile(t, target)
		})
	}
}

func TestDownloadReportsChunkFailure(t *testing.T) {
	t.Parallel()
	// An egest that answers the first chunk and loses the rest, which is where
	// a half-written .part would otherwise survive. Which chunk is refused is
	// keyed off the path rather than a counter, because chunks are fetched
	// concurrently and a counter would decide it by whichever arrived first.
	first := encryptChunk(t, bytes.Repeat([]byte("a"), testChunkSize), testFileKey)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{region}/{bucket}/{uuid}/{index}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("index") != "0" {
			http.NotFound(w, r)
			return
		}
		w.Write(first)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := newTestClient(server)
	file := File{
		Name: "payload.bin", Size: int64(testChunkSize * 2), Key: testFileKey,
		UUID: "u", Region: testRegion, Bucket: testBucket, Chunks: 2, Version: dataVersion,
	}
	target := filepath.Join(t.TempDir(), "payload.bin")
	if err := client.Download(t.Context(), file, target); err == nil {
		t.Fatal("Download returned nil error")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("a failed download left a file at the destination")
	}
	assertNoPartFile(t, target)
}

// assertNoPartFile fails when the in-progress file survived the download.
func assertNoPartFile(t *testing.T, target string) {
	t.Helper()

	if _, err := os.Stat(target + partSuffix); !os.IsNotExist(err) {
		t.Errorf("%q was left behind", target+partSuffix)
	}
}

func TestDownloadReportsRenameFailure(t *testing.T) {
	t.Parallel()
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "payload.bin", key: testFileKey, data: []byte("contents")},
	})
	client := newTestClient(server)
	file := fetchFirst(t, client, testFolderKey)

	// A non-empty directory sitting where the file should land, so the failure
	// falls on the rename, after every chunk has already been written.
	target := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.MkdirAll(filepath.Join(target, "occupied"), 0o755); err != nil {
		t.Fatalf("set up the blocked destination: %v", err)
	}
	if err := client.Download(t.Context(), file, target); err == nil {
		t.Fatal("Download returned nil error")
	}
	assertNoPartFile(t, target)
}

func TestChunksHoldSize(t *testing.T) {
	t.Parallel()
	const size = 16
	tests := []struct {
		name   string
		chunks int
		bytes  int64
		want   bool
	}{
		{name: "an empty file in no chunks", chunks: 0, bytes: 0, want: true},
		{name: "an empty file claiming a chunk", chunks: 1, bytes: 0, want: true},
		{name: "a chunk that is not there", chunks: 0, bytes: 1, want: false},
		{name: "one partial chunk", chunks: 1, bytes: 5, want: true},
		{name: "one full chunk", chunks: 1, bytes: size, want: true},
		{name: "one chunk overflowing", chunks: 1, bytes: size + 1, want: false},
		{name: "two chunks, the last partial", chunks: 2, bytes: size + 5, want: true},
		{name: "a trailing empty chunk", chunks: 3, bytes: size * 2, want: true},
		{name: "more chunks than bytes", chunks: 4, bytes: size * 2, want: false},
		{name: "a chunk count that would overflow the multiply", chunks: math.MaxInt, bytes: size, want: false},
		{name: "a negative size", chunks: 1, bytes: -1, want: false},
		{name: "a negative chunk count", chunks: -1, bytes: 0, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := chunksHoldSize(tc.chunks, tc.bytes, size); got != tc.want {
				t.Errorf("chunksHoldSize(%d, %d, %d) = %v, want %v", tc.chunks, tc.bytes, size, got, tc.want)
			}
		})
	}
}

func TestDownloadRejectsChunksThatCannotHoldTheSize(t *testing.T) {
	t.Parallel()
	client := New(http.DefaultClient, "http://invalid.invalid", "http://invalid.invalid")
	client.chunkSize = testChunkSize
	file := File{
		Name: "payload.bin", Size: testChunkSize * 10, Key: testFileKey,
		UUID: "u", Region: testRegion, Bucket: testBucket, Chunks: 1, Version: dataVersion,
	}

	// The egest is unreachable, so reaching it at all would fail differently.
	target := filepath.Join(t.TempDir(), "payload.bin")
	err := client.Download(t.Context(), file, target)
	if err == nil {
		t.Fatal("Download returned nil error")
	}
	if !strings.Contains(err.Error(), "cannot hold") {
		t.Errorf("error = %v, want it to report the impossible chunk count", err)
	}
	assertNoPartFile(t, target)
}

func TestDownloadRetriesATransientChunkFailure(t *testing.T) {
	t.Parallel()
	data := bytes.Repeat([]byte("a"), testChunkSize)
	chunk := encryptChunk(t, data, testFileKey)

	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{region}/{bucket}/{uuid}/{index}", func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write(chunk)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := newTestClient(server)
	file := File{
		Name: "payload.bin", Size: testChunkSize, Key: testFileKey,
		UUID: "u", Region: testRegion, Bucket: testBucket, Chunks: 1, Version: dataVersion,
	}
	target := filepath.Join(t.TempDir(), "payload.bin")
	if err := client.Download(t.Context(), file, target); err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("downloaded %q, want %q", got, data)
	}
	if n := attempts.Load(); n != 2 {
		t.Errorf("egest was asked %d times, want 2", n)
	}
}

func TestDownloadGivesUpOnAPersistentChunkFailure(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{region}/{bucket}/{uuid}/{index}", func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := newTestClient(server)
	file := File{
		Name: "payload.bin", Size: testChunkSize, Key: testFileKey,
		UUID: "u", Region: testRegion, Bucket: testBucket, Chunks: 1, Version: dataVersion,
	}
	target := filepath.Join(t.TempDir(), "payload.bin")
	if err := client.Download(t.Context(), file, target); err == nil {
		t.Fatal("Download returned nil error")
	}
	if n := attempts.Load(); n != chunkAttempts {
		t.Errorf("egest was asked %d times, want %d", n, chunkAttempts)
	}
	assertNoPartFile(t, target)
}

func TestDownloadRejectsAnOversizedChunk(t *testing.T) {
	t.Parallel()
	// More than a chunk can hold, which an unbounded read would pull into
	// memory in full before finding out it was never a chunk.
	oversized := bytes.Repeat([]byte("a"), maxChunkBody+1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{region}/{bucket}/{uuid}/{index}", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(oversized)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := newTestClient(server)
	file := File{
		Name: "payload.bin", Size: testChunkSize, Key: testFileKey,
		UUID: "u", Region: testRegion, Bucket: testBucket, Chunks: 1, Version: dataVersion,
	}
	target := filepath.Join(t.TempDir(), "payload.bin")
	err := client.Download(t.Context(), file, target)
	if err == nil {
		t.Fatal("Download returned nil error")
	}
	if !strings.Contains(err.Error(), "larger than a chunk") {
		t.Errorf("error = %v, want it to report the oversized body", err)
	}
	assertNoPartFile(t, target)
}

func TestDownloadHonoursCancellation(t *testing.T) {
	t.Parallel()
	// A file of many chunks, so a cancelled run has to unwind the fetches in
	// flight rather than simply running out of work.
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "payload.bin", key: testFileKey, data: bytes.Repeat([]byte("a"), testChunkSize*20)},
	})
	client := newTestClient(server)
	file := fetchFirst(t, client, testFolderKey)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	target := filepath.Join(t.TempDir(), "payload.bin")
	if err := client.Download(ctx, file, target); !errors.Is(err, context.Canceled) {
		t.Errorf("Download error = %v, want context.Canceled", err)
	}
	assertNoPartFile(t, target)
}
