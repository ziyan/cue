package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/util/drm"
)

// NewDisplayCommand builds "cue display", which reports what the machine's
// graphics hardware looks like. It is the first thing to run when a screen
// stays black, and it deliberately works whether or not the daemon is running:
// the kernel's view needs no X server at all.
func NewDisplayCommand() *cli.Command {
	return &cli.Command{
		Name:  "display",
		Usage: "inspect the screens attached to this machine",
		Commands: []*cli.Command{
			{
				Name:   "probe",
				Usage:  "list the display connectors the kernel can see, with or without an X server",
				Action: probeDisplay,
			},
			{
				Name:  "outputs",
				Usage: "ask a running X server what it is driving",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "number",
						Usage: "X display number to connect to",
					},
					&cli.StringFlag{
						Name:  "authority",
						Usage: "path to the X authority file (defaults to $XAUTHORITY)",
					},
				},
				Action: showOutputs,
			},
		},
	}
}

func probeDisplay(ctx context.Context, command *cli.Command) error {
	connectors, err := drm.Connectors()
	if err != nil {
		return err
	}
	if len(connectors) == 0 {
		fmt.Println("this machine reports no display connectors at all")
		fmt.Println("inside a container that usually means /dev/dri was not passed through")
		return nil
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "CONNECTOR\tSTATUS\tMONITOR\tMODES")
	for _, connector := range connectors {
		status := "disconnected"
		if connector.Connected {
			status = "connected"
		}
		modes := strings.Join(firstFew(connector.Modes, 4), " ")
		if modes == "" {
			modes = "-"
		}
		monitor := connector.Monitor
		if monitor == "" {
			monitor = "-"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", connector.Name, status, monitor, modes)
	}
	return writer.Flush()
}

func showOutputs(ctx context.Context, command *cli.Command) error {
	// This path exists for looking at a display the daemon is driving, so the
	// authority file is the daemon's unless something else is named.
	authority := command.String("authority")
	if authority == "" {
		authority = os.Getenv("XAUTHORITY")
	}
	if authority == "" {
		return fmt.Errorf("no X authority file given; pass --authority or set XAUTHORITY")
	}
	cookie, err := readCookie(authority)
	if err != nil {
		return err
	}

	connection, err := display.Open(ctx, command.Int("number"), cookie)
	if err != nil {
		return err
	}
	defer connection.Close()

	screen := connection.Screen()
	fmt.Printf("screen: %dx%d\n\n", screen.Width, screen.Height)

	outputs, err := connection.Outputs()
	if err != nil {
		return err
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "OUTPUT\tSTATUS\tGEOMETRY\tROTATION\tPREFERRED")
	for _, output := range outputs {
		status := "disconnected"
		switch {
		case output.Connected && output.Enabled:
			status = "on"
		case output.Connected:
			status = "connected, off"
		}
		geometry := "-"
		if output.Enabled {
			geometry = fmt.Sprintf("%dx%d+%d+%d", output.Width, output.Height, output.X, output.Y)
		}
		preferred := output.PreferredMode
		if preferred == "" {
			preferred = "-"
		}
		name := output.Name
		if output.Primary {
			name += " *"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", name, status, geometry, output.Rotation, preferred)
	}
	return writer.Flush()
}

func firstFew(values []string, count int) []string {
	if len(values) <= count {
		return values
	}
	return append(values[:count:count], "…")
}
