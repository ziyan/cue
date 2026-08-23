package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/ziyan/cue/internal/config"
)

// NewHealthCommand builds "cue health", which asks the running daemon whether
// it is working and exits non-zero if it is not.
//
// It exists because the image has no curl and no shell, so a container health
// check has nothing else to run. Pointing the binary at its own daemon is the
// smallest thing that works and needs no extra program in the image.
func NewHealthCommand() *cli.Command {
	return &cli.Command{
		Name:  "health",
		Usage: "ask the running daemon whether it is working; exit non-zero if not",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "url",
				Usage: "where the daemon's interface is, if it is not where the configuration says",
			},
		},
		Action: checkHealth,
	}
}

func checkHealth(ctx context.Context, command *cli.Command) error {
	address := command.String("url")
	if address == "" {
		address = healthURL(command.Root().String("config"))
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("the daemon is not answering on %s: %w", address, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", response.Status, body)
	}
	fmt.Printf("%s", body)
	return nil
}

// healthURL works out where the interface is from the configuration, falling
// back to the default port when there is no configuration to read — which is
// the case during a first start, when a health check may well run.
func healthURL(filename string) string {
	port := "8080"
	if configuration, err := config.Load(filename); err == nil {
		if _, listenPort, err := net.SplitHostPort(configuration.Web.Listen); err == nil && listenPort != "" {
			port = listenPort
		}
	}
	return "http://127.0.0.1:" + port + "/healthz"
}
