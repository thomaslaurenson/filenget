package filen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// errNameTaken reports a file whose name another file in the same run was
// already written under.
var errNameTaken = errors.New("another file in this run has the same name")

// errNotTheSharedFile reports something already at the destination that is not
// the file the share holds.
var errNotTheSharedFile = errors.New("already exists and differs from the file in the share")

// Fetch downloads every file behind each link into dir, leaving alone any file
// already there, and writes one line per file to progress.
//
// A file that fails does not abandon the ones after it: every link is attempted
// and the failures are reported together, so one dead file in a folder share
// does not cost the run the files behind it.
func (c *Client) Fetch(ctx context.Context, progress io.Writer, dir string, links []string) error {
	// Parse every link before creating anything, so a mistyped link fails
	// without leaving an empty destination directory behind.
	parsed := make([]Link, 0, len(links))
	for _, raw := range links {
		link, err := ParseLink(raw)
		if err != nil {
			return err
		}
		parsed = append(parsed, link)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", dir, err)
	}
	var failures []error
	// What this run has written, so a later file landing on the same name is
	// not taken for one already present. Compared with os.SameFile rather than
	// by name, so names differing only in case collide exactly where the
	// filesystem ignores case.
	var written []fs.FileInfo
	for _, link := range parsed {
		// Carrying on past a failure means an interrupted run would otherwise
		// work through every remaining link to collect the same cancellation.
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		// Files can return both: the files it could read and the ones it
		// could not
		files, err := c.Files(ctx, link)
		if err != nil {
			failures = append(failures, err)
		}
		for _, f := range files {
			info, err := c.fetchOne(ctx, progress, dir, f, written)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if info != nil {
				written = append(written, info)
			}
		}
	}
	return errors.Join(failures...)
}

// fetchOne downloads one file unless it is already at the destination, and
// returns what it wrote, or nil when it wrote nothing. written is what the run
// has written so far.
func (c *Client) fetchOne(ctx context.Context, progress io.Writer, dir string, f File, written []fs.FileInfo) (fs.FileInfo, error) {
	// The name comes from metadata written by whoever made the share, so it is
	// used as it is spelled or not at all. Reducing "a/b.txt" to "b.txt"
	// would keep the file inside the destination, but lets two files in one
	// share land on the same name.
	if !usableName(f.Name) {
		return nil, fmt.Errorf("file name %q is not usable", f.Name)
	}
	target := filepath.Join(dir, f.Name)
	// Only absence means there is work to do. Any other answer is a fault worth
	// reporting here, where the path is known, rather than further in.
	switch info, err := os.Stat(target); {
	case err == nil:
		if slices.ContainsFunc(written, func(w fs.FileInfo) bool { return os.SameFile(w, info) }) {
			return nil, fmt.Errorf("%q: %w", target, errNameTaken)
		}
		// The size is all that can be compared without fetching the file
		// again, and it catches a copy cut short or left from an older version
		// of the share. A mismatch is reported rather than replaced, because
		// the file may be one of the user's own.
		if !info.Mode().IsRegular() || info.Size() != f.Size {
			return nil, fmt.Errorf("%q: %w", target, errNotTheSharedFile)
		}
		fmt.Fprintf(progress, "[*] %s: already present\n", f.Name)
		return nil, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("stat %q: %w", target, err)
	}
	fmt.Fprintf(progress, "[*] %s: fetching %d bytes in %d chunk(s)\n", f.Name, f.Size, f.Chunks)
	if err := c.Download(ctx, f, target); err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", target, err)
	}
	return info, nil
}

// usableName reports whether a file can be written under the destination with
// name exactly as it is spelled: a single path element that stays inside the
// directory. On Windows, filepath.IsLocal also turns away device names such as
// "CON" and any name with a colon, which would open a device or write to a
// stream of another file; a trailing dot or space is turned away here, because
// Windows drops it and the file would land under another name.
func usableName(name string) bool {
	if name == "." || filepath.Base(name) != name || !filepath.IsLocal(name) {
		return false
	}
	return runtime.GOOS != "windows" || !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, " ")
}
