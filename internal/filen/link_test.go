package filen

import (
	"errors"
	"strings"
	"testing"
)

// linkUUID is an id in the shape a share link must name its share by.
const linkUUID = "0f8fad5b-d9cb-469f-a165-70867728950e"

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
			raw:  "https://drive.filen.io/d/" + linkUUID + "#thekey",
			want: Link{Kind: FileLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "folder link",
			raw:  "https://drive.filen.io/f/" + linkUUID + "#thekey",
			want: Link{Kind: FolderLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "trailing slash on the path",
			raw:  "https://drive.filen.io/f/" + linkUUID + "/#thekey",
			want: Link{Kind: FolderLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "host case is ignored",
			raw:  "https://DRIVE.FILEN.IO/f/" + linkUUID + "#thekey",
			want: Link{Kind: FolderLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "bare filen.io host",
			raw:  "https://filen.io/d/" + linkUUID + "#thekey",
			want: Link{Kind: FileLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "hash-routed file link",
			raw:  "https://app.filen.io/#/d/" + linkUUID + "%23" + hexKey,
			want: Link{Kind: FileLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "hash-routed folder link",
			raw:  "https://app.filen.io/#/f/" + linkUUID + "%23" + hexKey,
			want: Link{Kind: FolderLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "hash-routed link with a literal second hash",
			raw:  "https://app.filen.io/#/d/" + linkUUID + "#" + hexKey,
			want: Link{Kind: FileLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "hash-routed link with its key as it is",
			raw:  "https://app.filen.io/#/f/" + linkUUID + "%23thekey",
			want: Link{Kind: FolderLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "hash-routed link with a raw key that looks like hex",
			raw:  "https://app.filen.io/#/d/" + linkUUID + "%23" + rawHexKey,
			want: Link{Kind: FileLink, UUID: linkUUID, Key: rawHexKey},
		},
		{
			name: "path-routed file link with a hex key swaps the letters",
			raw:  "https://app.filen.io/f/" + linkUUID + "#" + hexKey,
			want: Link{Kind: FileLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "path-routed folder link with a hex key swaps the letters",
			raw:  "https://app.filen.io/d/" + linkUUID + "#" + hexKey,
			want: Link{Kind: FolderLink, UUID: linkUUID, Key: "thekey"},
		},
		{
			name: "path-routed link with a raw key that looks like hex keeps the letters",
			raw:  "https://drive.filen.io/d/" + linkUUID + "#" + rawHexKey,
			want: Link{Kind: FileLink, UUID: linkUUID, Key: rawHexKey},
		},
		{
			name: "upper-case id is lowered",
			raw:  "https://drive.filen.io/d/" + strings.ToUpper(linkUUID) + "#thekey",
			want: Link{Kind: FileLink, UUID: linkUUID, Key: "thekey"},
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
		{name: "no fragment, which is where the key lives", raw: "https://drive.filen.io/f/" + linkUUID},
		{name: "empty fragment", raw: "https://drive.filen.io/f/" + linkUUID + "#"},
		{name: "unknown link kind", raw: "https://drive.filen.io/x/" + linkUUID + "#thekey"},
		{name: "no uuid", raw: "https://drive.filen.io/f/#thekey"},
		{name: "no kind or uuid", raw: "https://drive.filen.io/#thekey"},
		{name: "id that is not a uuid", raw: "https://drive.filen.io/d/abc-123#thekey"},
		{name: "id followed by another segment", raw: "https://drive.filen.io/d/" + linkUUID + "/extra#thekey"},
		{name: "hash-routed id that is not a uuid", raw: "https://app.filen.io/#/d/abc-123%23" + hexKey},
		{name: "hash route with no key", raw: "https://app.filen.io/#/d/" + linkUUID},
		{name: "hash route with an empty key", raw: "https://app.filen.io/#/d/" + linkUUID + "%23"},
		{name: "hash route that is not a link", raw: "https://app.filen.io/#/drive"},
		{name: "hash route of an unknown kind", raw: "https://app.filen.io/#/x/" + linkUUID + "%23" + hexKey},
		{name: "another host entirely", raw: "https://example.com/f/" + linkUUID + "#thekey"},
		{name: "a host merely ending in the domain", raw: "https://notfilen.io/f/" + linkUUID + "#thekey"},
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

func TestParseLinkKeepsTheKeyOutOfErrors(t *testing.T) {
	t.Parallel()
	const key = "secretkey"
	tests := []struct {
		name string
		raw  string
	}{
		{name: "another host entirely", raw: "https://example.com/d/" + linkUUID + "#" + key},
		{name: "unknown link kind", raw: "https://drive.filen.io/x/" + linkUUID + "#" + key},
		{name: "id that is not a uuid", raw: "https://drive.filen.io/d/abc-123#" + key},
		{name: "hash route of an unknown kind", raw: "https://app.filen.io/#/x/" + linkUUID + "%23" + key},
		{name: "a URL net/url rejects", raw: "https://drive.filen.io/f/abc\x7f#" + key},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseLink(tc.raw)
			if err == nil {
				t.Fatalf("ParseLink(%q) returned nil error", tc.raw)
			}
			if strings.Contains(err.Error(), key) {
				t.Errorf("ParseLink(%q) error %q carries the key", tc.raw, err)
			}
		})
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "path-routed link", raw: "https://drive.filen.io/d/abc-123#thekey", want: "https://drive.filen.io/d/abc-123"},
		{name: "hash-routed link keeps its route", raw: "https://app.filen.io/#/d/abc-123%23thekey", want: "https://app.filen.io/#/d/abc-123"},
		{name: "hash-routed link with a literal second hash", raw: "https://app.filen.io/#/d/abc-123#thekey", want: "https://app.filen.io/#/d/abc-123"},
		{name: "key escaped into the path", raw: "https://app.filen.io/f/abc-123%23thekey", want: "https://app.filen.io/f/abc-123"},
		{name: "another separator after the key", raw: "https://app.filen.io/f/abc-123#thekey%23more", want: "https://app.filen.io/f/abc-123"},
		{name: "hash route with no key", raw: "https://app.filen.io/#/drive", want: "https://app.filen.io/#/drive"},
		{name: "no fragment", raw: "https://drive.filen.io/f/abc-123", want: "https://drive.filen.io/f/abc-123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := redact(tc.raw); got != tc.want {
				t.Errorf("redact(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
