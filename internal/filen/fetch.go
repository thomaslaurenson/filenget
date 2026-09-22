package filen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

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
	for _, link := range parsed {
		// Carrying on past a failure means an interrupted run would otherwise
		// work through every remaining link to collect the same cancellation.
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		files, err := c.Files(ctx, link)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, f := range files {
			if err := c.fetchOne(ctx, progress, dir, f); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

// fetchOne downloads one file unless it is already at the destination.
func (c *Client) fetchOne(ctx context.Context, progress io.Writer, dir string, f File) error {
	// The name comes from metadata written by whoever made the share, so one
	// like "../../.ssh/authorized_keys" must not be able to place a file
	// outside the destination directory.
	name := filepath.Base(f.Name)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return fmt.Errorf("file name %q is not usable", f.Name)
	}
	target := filepath.Join(dir, name)
	// Only absence means there is work to do. Any other answer is a fault worth
	// reporting here, where the path is known, rather than further in.
	switch _, err := os.Stat(target); {
	case err == nil:
		fmt.Fprintf(progress, "[*] %s: already present\n", name)
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("stat %q: %w", target, err)
	}
	fmt.Fprintf(progress, "[*] %s: fetching %d bytes in %d chunk(s)\n", name, f.Size, f.Chunks)
	return c.Download(ctx, f, target)
}
