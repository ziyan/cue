package cmd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/ziyan/cue/internal/upgrade"
)

// NewUpgradeSwapCommand builds "cue upgrade-swap", which is not for people.
//
// It runs in a short-lived container the daemon starts when somebody presses
// the upgrade button, and it does the one thing a container cannot do to
// itself: replace it. See internal/upgrade/swap.go for the order it works in
// and why.
//
// It is built from the image being upgraded *to*, so the code performing the
// swap is the code being installed rather than the code being replaced.
func NewUpgradeSwapCommand() *cli.Command {
	return &cli.Command{
		Name:   "upgrade-swap",
		Usage:  "replace another container with one built from a newer image (used by the upgrade button)",
		Hidden: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "container",
				Usage:    "the container to replace",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "image",
				Usage:    "the image to replace it with",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "socket",
				Usage: "where the Docker daemon listens",
				Value: upgrade.SocketPath,
			},
		},
		Action: runSwap,
	}
}

func runSwap(ctx context.Context, command *cli.Command) error {
	docker := upgrade.NewDocker(command.String("socket"))
	if err := docker.Ping(ctx); err != nil {
		return err
	}

	// Whether the replacement is working is an HTTP request to this machine's
	// own interface, which this container can make because it was given the
	// host's network namespace and the configuration that says which port.
	address := healthURL(command.Root().String("config"))
	healthy := func(ctx context.Context) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 5 * time.Second}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("the new daemon answered %s", response.Status)
		}
		return nil
	}

	return upgrade.Swap(ctx, docker, command.String("container"), command.String("image"), healthy)
}
