package filen

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Kind distinguishes the two sorts of public share link.
type Kind string

const (
	// FileLink is a link to one file, https://drive.filen.io/d/<uuid>#<key>.
	FileLink Kind = "d"
	// FolderLink is a link to a folder, https://drive.filen.io/f/<uuid>#<key>.
	FolderLink Kind = "f"
)

// filenHost is the domain a share link sits under. The API this package talks
// to is Filen's, so a link anywhere else could only fail further in, with a
// worse message than saying so here.
const filenHost = "filen.io"

// ErrNotALink reports a URL that is not a Filen file or folder share link.
var ErrNotALink = errors.New("not a Filen file or folder link")

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
func ParseLink(raw string) (Link, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return Link{}, fmt.Errorf("parse %q: %w", raw, err)
	}
	if !isFilenHost(parsed.Hostname()) {
		return Link{}, fmt.Errorf("%q: %w", raw, ErrNotALink)
	}
	kind, uuid, found := strings.Cut(strings.Trim(parsed.Path, "/"), "/")
	if !found || uuid == "" || parsed.Fragment == "" {
		return Link{}, fmt.Errorf("%q: %w", raw, ErrNotALink)
	}
	switch Kind(kind) {
	case FileLink, FolderLink:
		return Link{Kind: Kind(kind), UUID: uuid, Key: parsed.Fragment}, nil
	default:
		return Link{}, fmt.Errorf("%q: %w", raw, ErrNotALink)
	}
}

func isFilenHost(host string) bool {
	host = strings.ToLower(host)
	return host == filenHost || strings.HasSuffix(host, "."+filenHost)
}
