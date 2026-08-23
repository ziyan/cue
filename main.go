// Command cue turns a headless Linux machine with a screen attached into a
// managed display: it starts and supervises an X server, a browser in kiosk
// mode, a VNC server, a sound server and a time synchronisation client, and
// serves a web interface for configuring and monitoring all of it.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/ziyan/cue/cmd"
	"github.com/ziyan/cue/internal/version"
)

func main() {
	command := &cli.Command{
		Name:                  "cue",
		Usage:                 "kiosk display daemon",
		Version:               version.String(),
		EnableShellCompletion: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "path to cue.yaml",
				Value:   cmd.DefaultConfigFilename,
				Sources: cli.EnvVars("CUE_CONFIG"),
			},
			&cli.StringFlag{
				Name:    "log-level",
				Aliases: []string{"l"},
				Usage:   "log level (DEBUG, INFO, NOTICE, WARNING, ERROR, CRITICAL); overrides log.level",
				Sources: cli.EnvVars("CUE_LOG_LEVEL"),
			},
		},
		Before: func(ctx context.Context, command *cli.Command) (context.Context, error) {
			cmd.SetupLogging(command.String("log-level"))
			return ctx, nil
		},
		Commands: []*cli.Command{
			cmd.NewRunCommand(),
			cmd.NewConfigCommand(),
			cmd.NewDisplayCommand(),
			cmd.NewVersionCommand(),
		},
	}

	// The daemon is process 1 in its container, where nothing else will
	// deliver a default action for these signals.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := command.Run(ctx, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}
