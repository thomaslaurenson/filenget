package filen

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	// metadataVersion is the marker a version 2 metadata string opens with.
	metadataVersion = "002"
	// dataVersion is the file data version whose chunk layout this package reads.
	dataVersion = 2
	// nonceLength is the GCM nonce, twelve characters of metadata or twelve
	// bytes at the head of a chunk.
	nonceLength = 12
	// tagLength is the GCM authentication tag, carried at the end of the ciphertext.
	tagLength = 16
	// keyLength is the AES-256 key PBKDF2 stretches a metadata key to.
	keyLength = 32

	metadataHeaderLength = len(metadataVersion) + nonceLength
)

// ErrUnsupportedVersion reports metadata or file data in a format this package
// does not implement. Filen's SDK defines a version 3 with hex keys; guessing
// at it would produce plausible rubbish rather than an answer, so a share that
// uses it fails by name instead.
var ErrUnsupportedVersion = errors.New("unsupported version")

// decryptMetadata decodes a version 2 metadata string: the marker "002", a
// twelve-character nonce, then base64 of the ciphertext with its tag appended.
//
// The key is stretched by PBKDF2-SHA512 with the key itself as the salt, for a
// single iteration. That is what Filen's SDK does, so it is the interoperable
// behaviour rather than a weakness to be corrected here.
func decryptMetadata(metadata, key string) ([]byte, error) {
	if len(metadata) < len(metadataVersion) {
		return nil, fmt.Errorf("metadata is %d bytes, too short for a version marker", len(metadata))
	}
	if version := metadata[:len(metadataVersion)]; version != metadataVersion {
		return nil, fmt.Errorf("metadata version %q: %w", version, ErrUnsupportedVersion)
	}
	if len(metadata) < metadataHeaderLength {
		return nil, fmt.Errorf("metadata is %d bytes, too short for a nonce", len(metadata))
	}
	derived, err := pbkdf2.Key(sha512.New, key, []byte(key), 1, keyLength)
	if err != nil {
		return nil, fmt.Errorf("derive metadata key: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(metadata[metadataHeaderLength:])
	if err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	return gcmOpen(derived, []byte(metadata[len(metadataVersion):metadataHeaderLength]), ciphertext)
}

// decryptChunk decodes a version 2 data chunk: a twelve-byte nonce, the
// ciphertext, then a sixteen-byte tag. The file key is used as raw bytes and is
// not stretched, which is the difference from metadata rather than an omission.
func decryptChunk(chunk []byte, key string) ([]byte, error) {
	if len(chunk) < nonceLength+tagLength {
		return nil, fmt.Errorf("chunk is %d bytes, too short for a nonce and tag", len(chunk))
	}
	return gcmOpen([]byte(key), chunk[:nonceLength], chunk[nonceLength:])
}

// gcmOpen decrypts AES-256-GCM ciphertext that carries its tag at the end.
func gcmOpen(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("build cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// GCM authenticates before it returns anything, so a wrong key fails
		// here rather than handing back bytes that look decrypted.
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}
