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
	"github.com/ziyan/cue/internal/minishell"
	"github.com/ziyan/cue/internal/version"
)

func main() {
	// The cue binary answers to two names. Under its own it is the daemon and
	// its command line tools; under the name "sh" it is a one-command shell
	// that exists because the X server compiles its keyboard map by running
	// xkbcomp through /bin/sh, and this image has no shell. See
	// internal/minishell, which explains why that is not solved by putting
	// one in.
	if minishell.IsInvokedAsShell(os.Args[0]) {
		os.Exit(minishell.Run(os.Args, os.Stderr))
	}

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
			cmd.NewHealthCommand(),
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
