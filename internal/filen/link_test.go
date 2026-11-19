package filen

import (
	"errors"
	"strings"
	"testing"
)

// hexKey is "thekey" hex-encoded, as Filen's current apps write a key.
const hexKey = "7468656b6579"

// rawHexKey is a raw key that happens to be hex, as a version 3 key is. It
// decodes to bytes no key is made of, so it must be kept as it is.
var rawHexKey = strings.Repeat("00ff", 16)

func TestParseLink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want Link
	}{
		{
			name: "file link",
			raw:  "https://drive.filen.io/d/abc-123#thekey",
			want: Link{Kind: FileLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "folder link",
			raw:  "https://drive.filen.io/f/abc-123#thekey",
			want: Link{Kind: FolderLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "trailing slash on the path",
			raw:  "https://drive.filen.io/f/abc-123/#thekey",
			want: Link{Kind: FolderLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "host case is ignored",
			raw:  "https://DRIVE.FILEN.IO/f/abc-123#thekey",
			want: Link{Kind: FolderLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "bare filen.io host",
			raw:  "https://filen.io/d/abc-123#thekey",
			want: Link{Kind: FileLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "hash-routed file link",
			raw:  "https://app.filen.io/#/d/abc-123%23" + hexKey,
			want: Link{Kind: FileLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "hash-routed folder link",
			raw:  "https://app.filen.io/#/f/abc-123%23" + hexKey,
			want: Link{Kind: FolderLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "hash-routed link with a literal second hash",
			raw:  "https://app.filen.io/#/d/abc-123#" + hexKey,
			want: Link{Kind: FileLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "hash-routed link with its key as it is",
			raw:  "https://app.filen.io/#/f/abc-123%23thekey",
			want: Link{Kind: FolderLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "hash-routed link with a raw key that looks like hex",
			raw:  "https://app.filen.io/#/d/abc-123%23" + rawHexKey,
			want: Link{Kind: FileLink, UUID: "abc-123", Key: rawHexKey},
		},
		{
			name: "path-routed file link with a hex key swaps the letters",
			raw:  "https://app.filen.io/f/abc-123#" + hexKey,
			want: Link{Kind: FileLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "path-routed folder link with a hex key swaps the letters",
			raw:  "https://app.filen.io/d/abc-123#" + hexKey,
			want: Link{Kind: FolderLink, UUID: "abc-123", Key: "thekey"},
		},
		{
			name: "path-routed link with a raw key that looks like hex keeps the letters",
			raw:  "https://drive.filen.io/d/abc-123#" + rawHexKey,
			want: Link{Kind: FileLink, UUID: "abc-123", Key: rawHexKey},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLink(tc.raw)
			if err != nil {
				t.Fatalf("ParseLink(%q) returned error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("ParseLink(%q) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseLinkRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "no fragment, which is where the key lives", raw: "https://drive.filen.io/f/abc-123"},
		{name: "empty fragment", raw: "https://drive.filen.io/f/abc-123#"},
		{name: "unknown link kind", raw: "https://drive.filen.io/x/abc-123#thekey"},
		{name: "no uuid", raw: "https://drive.filen.io/f/#thekey"},
		{name: "no kind or uuid", raw: "https://drive.filen.io/#thekey"},
		{name: "hash route with no key", raw: "https://app.filen.io/#/d/abc-123"},
		{name: "hash route with an empty key", raw: "https://app.filen.io/#/d/abc-123%23"},
		{name: "hash route that is not a link", raw: "https://app.filen.io/#/drive"},
		{name: "hash route of an unknown kind", raw: "https://app.filen.io/#/x/abc-123%23" + hexKey},
		{name: "another host entirely", raw: "https://example.com/f/abc-123#thekey"},
		{name: "a host merely ending in the domain", raw: "https://notfilen.io/f/abc-123#thekey"},
		{name: "not a URL at all", raw: "nonsense"},
		{name: "empty", raw: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseLink(tc.raw); !errors.Is(err, ErrNotALink) {
				t.Errorf("ParseLink(%q) error = %v, want ErrNotALink", tc.raw, err)
			}
		})
	}
}

func TestParseLinkReportsAnUnparseableURL(t *testing.T) {
	t.Parallel()
	// A control character is rejected by net/url before any of this package's
	// own checks get a look at it.
	if _, err := ParseLink("https://drive.filen.io/f/abc\x7f#key"); err == nil {
		t.Error("ParseLink returned nil error")
	}
}
