// Package filen downloads and decrypts the files behind a Filen public share
// link. Filen shares are end-to-end encrypted and the key travels in the link's
// URL fragment, so the server never sees it and the decryption happens here.
//
// The wire format follows Filen's own Rust SDK, FilenCloudDienste/filen-rs.
// Only version 2 metadata and file data are implemented, which is what that SDK
// describes for the links in circulation. Version 3 exists there and is
// rejected by name rather than guessed at.
package filen
