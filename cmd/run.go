package cmd

import (
	"context"

	"github.com/urfave/cli/v3"
)

// NewRunCommand builds "cue run", the daemon itself.
func NewRunCommand() *cli.Command {
	return &cli.Command{
		Name:   "run",
		Usage:  "run the display daemon",
		Action: runDaemon,
	}
}

func runDaemon(ctx context.Context, command *cli.Command) error {
	return nil
}
