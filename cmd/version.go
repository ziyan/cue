package cmd

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/ziyan/cue/internal/version"
)

// NewVersionCommand builds "cue version".
func NewVersionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "print the version and exit",
		Action: func(ctx context.Context, command *cli.Command) error {
			fmt.Printf("cue %s\n", version.String())
			return nil
		},
	}
}
