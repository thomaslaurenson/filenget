package filen

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestDecryptMetadataRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		plaintext string
		key       string
	}{
		{name: "a file name", plaintext: "patch.mpq", key: testFolderKey},
		{name: "a size", plaintext: "8720536", key: testFolderKey},
		{name: "a json blob", plaintext: `{"name":"a.bin","size":3,"key":"k"}`, key: "another-key"},
		{name: "empty", plaintext: "", key: testFolderKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := decryptMetadata(encryptMetadata(t, tc.plaintext, tc.key), tc.key)
			if err != nil {
				t.Fatalf("decryptMetadata returned error: %v", err)
			}
			if string(got) != tc.plaintext {
				t.Errorf("decryptMetadata = %q, want %q", got, tc.plaintext)
			}
		})
	}
}

func TestDecryptMetadataWrongKey(t *testing.T) {
	t.Parallel()
	// GCM authenticates before returning, so the wrong key fails outright
	// rather than handing back bytes that merely look wrong.
	if _, err := decryptMetadata(encryptMetadata(t, "patch.mpq", testFolderKey), "wrong"); err == nil {
		t.Error("decryptMetadata with the wrong key returned nil error")
	}
}

func TestDecryptMetadataRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		metadata        string
		wantUnsupported bool
	}{
		{name: "version 3, which the SDK defines and this does not", metadata: "003abcdefghijkl", wantUnsupported: true},
		{name: "version 1", metadata: "001abcdefghijkl", wantUnsupported: true},
		{name: "too short for a version marker", metadata: "00"},
		{name: "too short for a nonce", metadata: "002short"},
		{name: "ciphertext is not base64", metadata: "002" + testNonce + "not base64!"},
		{name: "empty", metadata: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := decryptMetadata(tc.metadata, testFolderKey)
			if err == nil {
				t.Fatalf("decryptMetadata(%q) returned nil error", tc.metadata)
			}
			if got := errors.Is(err, ErrUnsupportedVersion); got != tc.wantUnsupported {
				t.Errorf("errors.Is(err, ErrUnsupportedVersion) = %v, want %v (err %v)",
					got, tc.wantUnsupported, err)
			}
		})
	}
}

func TestDecryptChunkRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data []byte
	}{
		{name: "a full chunk", data: bytes.Repeat([]byte("a"), testChunkSize)},
		{name: "a partial chunk", data: []byte("tail")},
		{name: "empty", data: []byte{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := decryptChunk(encryptChunk(t, tc.data, testFileKey), testFileKey)
			if err != nil {
				t.Fatalf("decryptChunk returned error: %v", err)
			}
			if !bytes.Equal(got, tc.data) {
				t.Errorf("decryptChunk = %q, want %q", got, tc.data)
			}
		})
	}
}

func TestDecryptChunkRejects(t *testing.T) {
	t.Parallel()
	whole := encryptChunk(t, []byte("payload"), testFileKey)
	tests := []struct {
		name  string
		chunk []byte
		key   string
	}{
		{name: "too short for a nonce and tag", chunk: []byte("short"), key: testFileKey},
		{name: "truncated ciphertext fails the tag", chunk: whole[:len(whole)-1], key: testFileKey},
		{name: "wrong key", chunk: whole, key: strings.Repeat("z", keyLength)},
		{name: "key of the wrong length", chunk: whole, key: "short"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decryptChunk(tc.chunk, tc.key); err == nil {
				t.Error("decryptChunk returned nil error")
			}
		})
	}
}
