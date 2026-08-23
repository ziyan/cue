package cmd

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/daemon"
	"github.com/ziyan/cue/internal/version"
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
	filename := command.Root().String("config")

	store, err := config.Open(filename)
	if err != nil {
		return err
	}
	configuration := store.Current()

	// A level given on the command line has already been applied and wins;
	// otherwise the configured level takes effect now.
	if command.Root().String("log-level") == "" {
		SetLogLevel(configuration.Log.Level)
	}
	applyTimezone(configuration.Device.Timezone)

	log.Noticef("starting cue %s", version.String())
	log.Noticef("this device is %q (%s)", configuration.Device.Name, configuration.Device.Identifier)
	log.Noticef("configuration: %s", filename)

	running, err := daemon.New(store)
	if err != nil {
		return err
	}

	if err := running.Run(ctx); err != nil {
		return fmt.Errorf("cue: %w", err)
	}
	log.Noticef("stopped")
	return nil
}
