package cmd

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

const devVersion = "dev"

// Version is injected at build time via ldflags, falling back to devVersion.
var Version = devVersion

// init fills in the version for a build that set no ldflags.
func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		Version = versionFrom(Version, info.Main.Version)
	}
}

// versionFrom picks the version to report, given the one ldflags stamped in and
// the one the toolchain recorded. An injected version always wins; the module
// version only rescues a "dev" that came from "go install". The leading "v" is
// dropped so both routes report the same string.
func versionFrom(injected, module string) string {
	if injected != devVersion || !isReleaseVersion(module) {
		return injected
	}
	return strings.TrimPrefix(module, "v")
}

// pseudoVersion matches the timestamp and commit tail the toolchain appends when
// a build has no tag to name itself after. The separator is "-" when there was
// no earlier tag ("v0.0.0-<stamp>-<commit>") and "." above an existing one
// ("v1.2.4-0.<stamp>-<commit>"), so both have to match.
var pseudoVersion = regexp.MustCompile(`[-.][0-9]{14}-[0-9a-f]{12}$`)

// isReleaseVersion reports whether module names a published tag. A working tree
// is recorded as "(devel)", or as a pseudo-version with a "+dirty" suffix once
// the toolchain has VCS information. Reporting one of those would make an
// ordinary local build announce itself as a published version.
func isReleaseVersion(module string) bool {
	switch {
	case module == "", module == "(devel)":
		return false
	case strings.Contains(module, "+"):
		return false
	default:
		return !pseudoVersion.MatchString(module)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the filenget version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "filenget version %s\n", Version)
			return nil
		},
	}
}
