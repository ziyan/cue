package cmd

import (
	"context"

	"github.com/urfave/cli/v3"
)

// NewDisplayCommand builds "cue display", which reports what the machine's
// graphics hardware looks like. It is the first thing to run when a screen
// stays black.
func NewDisplayCommand() *cli.Command {
	return &cli.Command{
		Name:  "display",
		Usage: "inspect the screens attached to this machine",
		Commands: []*cli.Command{
			{
				Name:   "probe",
				Usage:  "list the outputs the kernel can see, whether or not an X server is running",
				Action: probeDisplay,
			},
		},
	}
}

func probeDisplay(ctx context.Context, command *cli.Command) error {
	return nil
}
