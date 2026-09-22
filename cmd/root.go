// Package cmd wires up filenget's command tree with Cobra. It handles argument
// parsing and dependency wiring only; parsing links, talking to Filen and
// decrypting what comes back live under internal/.
package cmd

import (
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/thomaslaurenson/filenget/internal/filen"
)

// requestTimeout bounds a single HTTP request, the download of one chunk
// included. The context cancels an interrupted run, but a hung connection is
// not an interrupted one and would otherwise wait for ever.
const requestTimeout = 10 * time.Minute

const rootLong = `filenget downloads the files behind a Filen public share link, decrypting with
the key in the link's URL fragment that a browser never sends and curl cannot
use.

filenget is an independent tool, not affiliated with or endorsed by Filen.`

// NewRootCmd builds filenget's command tree, writing results to out and
// progress to errw.
func NewRootCmd(out, errw io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "filenget <directory> <link>...",
		Short:         "Download and decrypt the files behind a Filen public share link",
		Long:          rootLong,
		Args:          cobra.MinimumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := filen.New(&http.Client{Timeout: requestTimeout}, filen.DefaultGateway, filen.DefaultEgest)
			return client.Fetch(cmd.Context(), cmd.ErrOrStderr(), args[0], args[1:])
		},
		// The first argument is the destination directory; every one after it
		// is a link, which the shell would otherwise offer local files for.
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return nil, cobra.ShellCompDirectiveFilterDirs
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
	}
	root.SetOut(out)
	root.SetErr(errw)

	root.AddCommand(newVersionCmd())
	return root
}
