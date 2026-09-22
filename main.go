// Command filenget downloads the files behind a Filen public share link.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thomaslaurenson/filenget/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cmd.NewRootCmd(os.Stdout, os.Stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		// Ask the context rather than the error: an interrupted download reports
		// its own symptom, a cancelled request or a short read, rather than the
		// cancellation itself.
		if errors.Is(ctx.Err(), context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "[!] %v\n", err)
		os.Exit(1)
	}
}
