package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run builds a fresh command tree and executes it with args, returning what
// each stream received. A fresh tree per call keeps flag state from leaking
// between cases, which a shared tree would carry over after Execute.
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer
	root := NewRootCmd(&out, &errOut)
	root.SetArgs(args)
	err = root.Execute()

	return out.String(), errOut.String(), err
}

func TestRootRejectsWrongArgumentCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments", args: nil},
		{name: "a destination but no link", args: []string{"somewhere"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stdout, _, err := run(t, tc.args...)

			if err == nil {
				t.Error("returned nil error")
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
		})
	}
}

func TestRootRejectsUnknownFlag(t *testing.T) {
	t.Parallel()
	stdout, _, err := run(t, "--nonsense", "somewhere", "https://drive.filen.io/f/x#k")

	if err == nil {
		t.Error("returned nil error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestRootReportsAnUnusableLink(t *testing.T) {
	t.Parallel()
	// A link on another host fails before any request, so this exercises the
	// wiring from arguments through to the error path without a network.
	dir := filepath.Join(t.TempDir(), "dest")
	stdout, _, err := run(t, dir, "https://example.com/f/share#key")

	if err == nil {
		t.Fatal("returned nil error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the destination directory was created for a link that was rejected")
	}
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()
	stdout, _, err := run(t, "--version")

	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}
	if !strings.Contains(stdout, Version) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, Version)
	}
}

func TestHelpMentionsTheTrademarkPosition(t *testing.T) {
	t.Parallel()
	stdout, _, err := run(t, "--help")

	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	// The binary name opens with Filen's trademark, so the disclaimer travels
	// with the help as well as the README.
	if !strings.Contains(stdout, "not affiliated with or endorsed by Filen") {
		t.Errorf("help text = %q, want the independence statement", stdout)
	}
}
