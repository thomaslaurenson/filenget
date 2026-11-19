package filen

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Kind distinguishes the two sorts of public share link.
type Kind string

const (
	// FileLink is a link to one file.
	FileLink Kind = "file"
	// FolderLink is a link to a folder.
	FolderLink Kind = "folder"
)

// filenHost is the domain a share link sits under. The API this package talks
// to is Filen's, so a link anywhere else could only fail further in, with a
// worse message than saying so here.
const filenHost = "filen.io"

// ErrNotALink reports a URL that is not a Filen file or folder share link.
var ErrNotALink = errors.New("not a Filen file or folder link")

// uuidPattern is the shape of the id a link names its share by. Checking it
// here, as Filen's apps do, fails a mangled link by name rather than leaving
// it to the API, which answers only that the link was not found.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Link is a parsed Filen public share link.
type Link struct {
	Kind Kind
	UUID string
	Key  string
}

// ParseLink splits a share link into its kind, the uuid identifying the share
// and the key held in the URL fragment. The fragment is the whole reason this
// package exists: a browser never sends it, so Filen cannot decrypt the share
// server-side and nor can anything that only fetches the URL.
//
// Filen's apps have written three shapes of link, and the letter naming the
// kind does not mean the same thing in all of them:
//
//	https://drive.filen.io/d/<uuid>#<key>     d is a file, f a folder
//	https://app.filen.io/#/d/<uuid>%23<hex>   d is a file, f a folder
//	https://app.filen.io/f/<uuid>#<hex>       f is a file, d a folder
//
// Only the last swaps the letters, and it is the only one that both keeps its
// route in the path and hex-encodes its key, which is how it is told apart.
func ParseLink(raw string) (Link, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		// A url.Error repeats the whole URL, key included, so only its cause
		// is kept.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return Link{}, fmt.Errorf("parse %q: %w", redact(raw), err)
	}
	if !isFilenHost(parsed.Hostname()) {
		return Link{}, fmt.Errorf("%q: %w", redact(raw), ErrNotALink)
	}
	// The hash-router shape carries its whole route in the fragment, where
	// url.Parse has already unescaped the %23 that separates the key.
	route, key := parsed.Path, parsed.Fragment
	hashRouted := strings.HasPrefix(parsed.Fragment, "/")
	if hashRouted {
		route, key, _ = strings.Cut(parsed.Fragment, "#")
	}
	letter, uuid, found := strings.Cut(strings.Trim(route, "/"), "/")
	// Filen's web app lowercases the id before it asks the API about it
	uuid = strings.ToLower(uuid)
	if !found || !uuidPattern.MatchString(uuid) || key == "" {
		return Link{}, fmt.Errorf("%q: %w", redact(raw), ErrNotALink)
	}
	key, encoded := decodeKey(key)
	kind, ok := linkKind(letter, encoded && !hashRouted)
	if !ok {
		return Link{}, fmt.Errorf("%q: %w", redact(raw), ErrNotALink)
	}
	return Link{Kind: kind, UUID: uuid, Key: key}, nil
}

// redact cuts the key from a link so an error can name the link without
// putting its key into a terminal or a CI log. The key follows the first '#'
// or '%', except in a hash-routed link, whose route is kept up to the separator
// before its key because it names the share. Cutting at the last separator
// instead would leak a key that a mangled link follows with another one.
func redact(raw string) string {
	cut := strings.IndexAny(raw, "#%")
	if cut < 0 {
		return raw
	}
	if strings.HasPrefix(raw[cut:], "#/") {
		if end := strings.IndexAny(raw[cut+1:], "#%"); end >= 0 {
			return raw[:cut+1+end]
		}
		return raw
	}
	return raw[:cut]
}

// linkKind reads the letter a route names its kind with, which the newest shape
// of link swaps.
func linkKind(letter string, swapped bool) (Kind, bool) {
	switch {
	case letter == "d" && !swapped, letter == "f" && swapped:
		return FileLink, true
	case letter == "f" && !swapped, letter == "d" && swapped:
		return FolderLink, true
	default:
		return "", false
	}
}

// decodeKey undoes the hex encoding Filen's current apps wrap a key in,
// reporting whether there was any. Every key is printable ASCII, so hex that
// decodes to anything else is a raw key that only looks like hex, as a version
// 3 key does, and is kept as it is.
func decodeKey(key string) (string, bool) {
	decoded, err := hex.DecodeString(key)
	if err != nil {
		return key, false
	}
	for _, b := range decoded {
		if b < '!' || b > '~' {
			return key, false
		}
	}
	return string(decoded), true
}

func isFilenHost(host string) bool {
	host = strings.ToLower(host)
	return host == filenHost || strings.HasSuffix(host, "."+filenHost)
}
