package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/timezone"
)

// DefaultConfigFilename is where the configuration lives unless --config says
// otherwise.
const DefaultConfigFilename = config.DefaultFilename

// NewConfigCommand builds "cue config", which creates, shows and checks the
// configuration file. Everything it does can also be done by editing the file
// in a text editor; it exists so that the first one does not have to be
// written from memory.
func NewConfigCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "create, show and check the configuration",
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "write a configuration file with the defaults filled in",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "output",
						Aliases: []string{"o"},
						Usage:   "where to write the file",
					},
					&cli.BoolFlag{
						Name:  "force",
						Usage: "overwrite an existing file",
					},
					&cli.BoolFlag{
						Name:  "development",
						Usage: "settings for running on your own machine: a virtual screen, no user switching, and directories under ./dev",
					},
				},
				Action: initConfiguration,
			},
			{
				Name:   "show",
				Usage:  "print the configuration in force, with secrets redacted",
				Action: showConfiguration,
			},
			{
				Name:   "check",
				Usage:  "report every problem with the configuration file",
				Action: checkConfiguration,
			},
		},
	}
}

func initConfiguration(ctx context.Context, command *cli.Command) error {
	filename := command.String("output")
	if filename == "" {
		filename = command.Root().String("config")
	}

	if !command.Bool("force") {
		if _, err := os.Stat(filename); err == nil {
			return fmt.Errorf("%s already exists; pass --force to overwrite it", filename)
		}
	}

	configuration := config.Default()
	if command.Bool("development") {
		applyDevelopmentDefaults(configuration)
	}
	// A device with an empty playlist shows the daemon's own holding page,
	// which tells whoever is standing in front of the screen where to point a
	// browser. That is more useful as a first run than a black screen.
	configuration.Normalize()

	if err := configuration.Save(filename); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", filename)
	fmt.Printf("device identifier: %s\n", configuration.Device.Identifier)
	return nil
}

// applyDevelopmentDefaults makes the configuration one that runs on a
// developer's own machine: no root, no hardware, nothing written outside the
// working directory.
func applyDevelopmentDefaults(configuration *config.Configuration) {
	configuration.Display.Server = config.ServerXvfb
	// Display :0 is almost certainly the developer's own desktop session.
	configuration.Display.Number = 9
	configuration.Display.Framebuffer = "1280x720"
	configuration.Browser.User = ""
	configuration.Browser.Sandbox = false
	configuration.Paths.State = "dev/state"
	configuration.Paths.Runtime = "dev/run"
	configuration.Audio.Enabled = false
	configuration.Time.Enabled = false
	configuration.Web.Listen = "127.0.0.1:8080"
	configuration.VNC.Listen = "127.0.0.1:5909"
	// Nothing sets a debugging port, here or anywhere: the browser chooses
	// one and says so in DevToolsActivePort in its profile, which for this
	// configuration is under dev/state. Attach devtools to whatever is in
	// that file.
}

func showConfiguration(ctx context.Context, command *cli.Command) error {
	configuration, err := loadConfiguration(command.Root().String("config"))
	if err != nil {
		return err
	}
	// Marshal renders every Secret through its YAML form, which is the real
	// value, so the copy printed here has them removed first. "cue config
	// show" is the thing an operator pastes into a bug report.
	redactSecrets(configuration)
	content, err := configuration.Marshal()
	if err != nil {
		return err
	}
	fmt.Print(string(content))
	return nil
}

func checkConfiguration(ctx context.Context, command *cli.Command) error {
	filename := command.Root().String("config")
	configuration, err := loadConfiguration(filename)
	if err != nil {
		return err
	}
	fmt.Printf("%s is valid: %d playlist items, %s\n",
		filename, len(configuration.Playlist.Items), describeDisplay(configuration))
	return nil
}

func describeDisplay(configuration *config.Configuration) string {
	if configuration.Display.Framebuffer != "" {
		return fmt.Sprintf("%s display :%d at %s",
			configuration.Display.Server, configuration.Display.Number, configuration.Display.Framebuffer)
	}
	return fmt.Sprintf("%s display :%d", configuration.Display.Server, configuration.Display.Number)
}

// redactSecrets replaces every secret with a placeholder, for the paths that
// print or serve a whole configuration.
func redactSecrets(configuration *config.Configuration) {
	const placeholder = config.Secret("********")
	if configuration.VNC.Password.IsSet() {
		configuration.VNC.Password = placeholder
	}
	if configuration.Web.SessionSecret.IsSet() {
		configuration.Web.SessionSecret = placeholder
	}
	if configuration.Web.PasswordHash != "" {
		configuration.Web.PasswordHash = string(placeholder)
	}
	for index := range configuration.Playlist.Items {
		login := configuration.Playlist.Items[index].Login
		if login != nil && login.Password.IsSet() {
			login.Password = placeholder
		}
	}
}

// loadConfiguration reads the configuration file and reports a readable error
// when it is missing, since that is what happens on a first run.
func loadConfiguration(filename string) (*config.Configuration, error) {
	configuration, err := config.Load(filename)
	if err != nil {
		if os.IsNotExist(underlying(err)) {
			return nil, fmt.Errorf("no configuration at %s; create one with 'cue config init --output %s'", filename, filename)
		}
		return nil, err
	}
	return configuration, nil
}

// underlying unwraps a wrapped error so that os.IsNotExist can see through it.
func underlying(err error) error {
	type unwrapper interface{ Unwrap() error }
	for {
		unwrapped, ok := err.(unwrapper)
		if !ok {
			return err
		}
		next := unwrapped.Unwrap()
		if next == nil {
			return err
		}
		err = next
	}
}

// applyTimezone sets the process's idea of local time from the configuration.
func applyTimezone(name string) {
	timezone.Apply(name)
}
