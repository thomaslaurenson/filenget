package cmd

import (
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()
	stdout, stderr, err := run(t, "version")

	if err != nil {
		t.Fatalf("version returned error: %v", err)
	}
	if !strings.Contains(stdout, Version) {
		t.Errorf("version stdout = %q, want it to contain %q", stdout, Version)
	}
	if stderr != "" {
		t.Errorf("version stderr = %q, want empty", stderr)
	}
}

func TestVersionRejectsArgs(t *testing.T) {
	t.Parallel()
	stdout, _, err := run(t, "version", "extra")

	if err == nil {
		t.Error("version with an argument returned nil error")
	}
	if stdout != "" {
		t.Errorf("version stdout = %q, want empty", stdout)
	}
}

func TestVersionFrom(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		injected string
		module   string
		want     string
	}{
		{name: "ldflags version wins", injected: "1.2.3", module: "v9.9.9", want: "1.2.3"},
		{name: "go install rescues dev", injected: devVersion, module: "v1.2.3", want: "1.2.3"},
		{name: "local build stays dev", injected: devVersion, module: "(devel)", want: devVersion},
		{name: "no module version stays dev", injected: devVersion, module: "", want: devVersion},
		{
			name:     "pseudo-version stays dev",
			injected: devVersion,
			module:   "v0.0.0-20260101120000-abcdef123456",
			want:     devVersion,
		},
		{
			name:     "pseudo-version above a tag stays dev",
			injected: devVersion,
			module:   "v1.2.4-0.20260101120000-abcdef123456",
			want:     devVersion,
		},
		{
			name:     "a dirty build stays dev",
			injected: devVersion,
			module:   "v1.2.3+dirty",
			want:     devVersion,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := versionFrom(tc.injected, tc.module); got != tc.want {
				t.Errorf("versionFrom(%q, %q) = %q, want %q", tc.injected, tc.module, got, tc.want)
			}
		})
	}
}
