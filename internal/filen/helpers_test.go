package filen

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

const (
	// testChunkSize keeps a multi-chunk file small enough to read in a failure
	// message while still exercising the loop that reassembles one.
	testChunkSize = 16
	// testNonce is twelve characters, the length a version 2 nonce must be.
	// One nonce for every chunk is fine here and would not be in production.
	testNonce = "nonce1234567"
	// testFileKey is a version 2 file key: exactly 32 bytes, used as raw AES-256
	// key material rather than stretched.
	testFileKey   = "0123456789abcdef0123456789abcdef"
	testFolderKey = "folder-link-key"
	testRegion    = "de-1"
	testBucket    = "filen-1"
)

// testFile is one file a fake share holds.
type testFile struct {
	name string
	key  string
	data []byte
}

func (f testFile) chunkCount() int {
	return (len(f.data) + testChunkSize - 1) / testChunkSize
}

// seal encrypts plaintext with AES-256-GCM in the layout gcmOpen reads: the tag
// appended to the ciphertext.
func seal(t *testing.T, key []byte, nonce string, plaintext []byte) []byte {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("build cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("build gcm: %v", err)
	}
	return gcm.Seal(nil, []byte(nonce), plaintext, nil)
}

// encryptMetadata builds the version 2 metadata string decryptMetadata reads,
// so a test can serve a reply the client accepts.
func encryptMetadata(t *testing.T, plaintext, key string) string {
	t.Helper()

	derived, err := pbkdf2.Key(sha512.New, key, []byte(key), 1, keyLength)
	if err != nil {
		t.Fatalf("derive metadata key: %v", err)
	}
	sealed := seal(t, derived, testNonce, []byte(plaintext))
	return metadataVersion + testNonce + base64.StdEncoding.EncodeToString(sealed)
}

// encryptChunk builds a chunk the way Filen serves one: nonce, ciphertext, tag.
func encryptChunk(t *testing.T, data []byte, key string) []byte {
	t.Helper()
	return append([]byte(testNonce), seal(t, []byte(key), testNonce, data)...)
}

// newShareServer serves both the gateway and the egest endpoints for a fake
// share, so a test drives the whole protocol with no network.
//
// Every reply is built here rather than inside a handler: a handler runs on the
// server's goroutine, where t.Fatal is not allowed and would be reported as a
// hang rather than a failure.
func newShareServer(t *testing.T, folderKey string, files []testFile) *httptest.Server {
	t.Helper()

	records := make([]map[string]any, 0, len(files))
	chunks := make(map[string][]byte)
	for i, f := range files {
		blob, err := json.Marshal(map[string]any{"name": f.name, "size": len(f.data), "key": f.key})
		if err != nil {
			t.Fatalf("encode metadata for %q: %v", f.name, err)
		}
		// Number the uuid rather than deriving it from the name: a share is free
		// to hold a name with a slash in it, which would not survive a URL path.
		uuid := fmt.Sprintf("uuid-%d", i)
		records = append(records, map[string]any{
			"uuid":     uuid,
			"region":   testRegion,
			"bucket":   testBucket,
			"chunks":   f.chunkCount(),
			"version":  dataVersion,
			"metadata": encryptMetadata(t, string(blob), folderKey),
		})
		for index := range f.chunkCount() {
			end := min((index+1)*testChunkSize, len(f.data))
			key := fmt.Sprintf("%s/%d", uuid, index)
			chunks[key] = encryptChunk(t, f.data[index*testChunkSize:end], f.key)
		}
	}

	dirContent := envelope(t, map[string]any{"files": records})
	dirInfo := envelope(t, map[string]string{"parent": "parent-uuid"})

	first := files[0]
	fileInfo := envelope(t, map[string]any{
		"uuid":    "uuid-0",
		"region":  testRegion,
		"bucket":  testBucket,
		"chunks":  first.chunkCount(),
		"version": dataVersion,
		"name":    encryptMetadata(t, first.name, first.key),
		"size":    encryptMetadata(t, strconv.Itoa(len(first.data)), first.key),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+dirLinkInfoPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(dirInfo)
	})
	mux.HandleFunc("POST "+dirLinkContentPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(dirContent)
	})
	mux.HandleFunc("POST "+fileLinkInfoPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(fileInfo)
	})
	mux.HandleFunc("GET /{region}/{bucket}/{uuid}/{index}", func(w http.ResponseWriter, r *http.Request) {
		chunk, ok := chunks[r.PathValue("uuid")+"/"+r.PathValue("index")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(chunk)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// envelope wraps data in the {status, message, data} reply the gateway sends.
func envelope(t *testing.T, data any) []byte {
	t.Helper()

	encoded, err := json.Marshal(map[string]any{"status": true, "data": data})
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return encoded
}

// newTestClient points a Client at server for both the gateway and the egest,
// which is unambiguous because the two use disjoint paths.
func newTestClient(server *httptest.Server) *Client {
	client := New(server.Client(), server.URL, server.URL)
	// Teach it the fixtures' chunk size, so the check that a chunk count can
	// hold a size agrees with the shares the helpers above build.
	client.chunkSize = testChunkSize
	return client
}
