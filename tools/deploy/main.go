// Command deploy puts this build of Cue onto a machine and starts it.
//
// It exists because the alternative — a page of instructions somebody follows
// by hand — is how two machines end up subtly different. Everything it does is
// something an operator could do themselves, in the order that works, with the
// checks that catch the usual mistakes:
//
//	go run ./tools/deploy -host display-1
//
// It needs the image built (make docker) and ssh access to the machine as
// root. It does not need anything installed there but Docker.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	host := flag.String("host", "", "the machine to deploy to, as ssh would address it")
	image := flag.String("image", "cue:dev", "the image to send")
	name := flag.String("name", "cue", "what to call the container")
	stopDisplayManager := flag.Bool("stop-display-manager", false,
		"stop and disable gdm, lightdm or sddm on the machine — they hold the graphics device, so the screen stays black until they are gone")
	configFile := flag.String("config", "", "a cue.yaml to install; the machine's own is kept if this is empty")
	terminal := flag.Int("virtual-terminal", 2, "the console the X server draws on; must match display.virtualTerminal")
	flag.Parse()

	if *host == "" {
		fmt.Fprintln(os.Stderr, "deploy: -host is needed")
		os.Exit(2)
	}

	if err := deploy(*host, *image, *name, *configFile, *terminal, *stopDisplayManager); err != nil {
		fmt.Fprintf(os.Stderr, "\ndeploy: FAILED: %s\n", err)
		os.Exit(1)
	}
}

func deploy(host, image, name, configFile string, terminal int, stopDisplayManager bool) error {
	step("checking that %s is reachable and has Docker", host)
	version, err := remoteOutput(host, "docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return fmt.Errorf("cannot reach Docker on %s: %w", host, err)
	}
	fmt.Printf("    Docker %s\n", strings.TrimSpace(version))

	if stopDisplayManager {
		step("stopping anything that holds the graphics device")
		for _, manager := range []string{"gdm", "gdm3", "lightdm", "sddm", "xdm"} {
			// Failure is the normal case: most of these are not installed.
			_ = remote(host, "systemctl", "disable", "--now", manager)
		}
		remaining, _ := remoteOutput(host, "pgrep", "-a", "-f", "Xorg|Xwayland")
		if strings.TrimSpace(remaining) != "" {
			return fmt.Errorf("something is still running an X server on %s, and it holds the graphics device:\n%s",
				host, remaining)
		}
		fmt.Println("    nothing is holding it now")
	}

	step("sending %s", image)
	if err := sendImage(host, image); err != nil {
		return err
	}

	step("preparing the directories")
	if err := remote(host, "mkdir", "-p", "/etc/cue", "/var/lib/cue"); err != nil {
		return err
	}

	if configFile != "" {
		step("installing %s", configFile)
		content, err := os.ReadFile(configFile)
		if err != nil {
			return err
		}
		// Written through a pipe rather than scp so that this needs nothing
		// on the machine but ssh, and so that the file lands with the right
		// mode: it holds the passwords the screen signs in with.
		if err := remoteInput(host, bytes.NewReader(content), "sh", "-c",
			"umask 077 && cat > /etc/cue/cue.yaml"); err != nil {
			return err
		}
	}

	step("starting the container")
	_ = remote(host, "docker", "rm", "-f", name)

	// Input and sound are passed through only when the machine has them.
	// Naming a device that is not there is an error, and a screen nobody
	// touches and nobody listens to is a perfectly ordinary screen.
	var optional []string
	for _, device := range []string{"/dev/input", "/dev/snd"} {
		if remote(host, "test", "-e", device) == nil {
			optional = append(optional, device)
		} else {
			fmt.Printf("    %s is not on this machine; carrying on without it\n", device)
		}
	}

	if err := remote(host, containerArguments(image, name, terminal, optional)...); err != nil {
		return err
	}

	step("waiting for it to say it is working")
	deadline := time.Now().Add(3 * time.Minute)
	var lastError error
	for time.Now().Before(deadline) {
		if err := remote(host, "docker", "exec", name, "/usr/local/bin/cue", "health"); err == nil {
			fmt.Printf("\ndeploy: %s is running Cue\n", host)
			reportScreen(host, name)
			return nil
		} else {
			lastError = err
		}
		time.Sleep(3 * time.Second)
	}

	// A deployment that fails should hand over the reason, not the fact.
	logs, _ := remoteOutput(host, "docker", "logs", "--tail", "60", name)
	return fmt.Errorf("it did not come up within three minutes (%w)\n\n--- the last of its log ---\n%s", lastError, logs)
}

// containerArguments builds the docker command that starts the daemon.
//
// It is a function of its own so that it can be tested, because it is a list
// of thirty flags where one wrong word means a screen that stays black on a
// machine somebody has to drive to. The device list in particular has to agree
// with display.virtualTerminal, and the reason each entry is there is in
// deploy/docker-compose.yml, which this mirrors.
func containerArguments(image, name string, terminal int, optionalDevices []string) []string {
	arguments := []string{
		"docker", "run", "-d",
		"--name", name,
		"--restart", "unless-stopped",
		// The web interface and the VNC server should answer on the machine's
		// own address, and kernel hotplug events are only delivered in the
		// host's network namespace.
		"--network", "host",
		// Chromium exhausts Docker's default 64 megabytes and then crashes
		// tabs with no explanation anybody can act on.
		"--shm-size", "1g",
		// The graphics device, the console layer, and the one console the X
		// server is told to draw on.
		"--device", "/dev/dri:/dev/dri",
		"--device", "/dev/tty0:/dev/tty0",
		"--device", fmt.Sprintf("/dev/tty%d:/dev/tty%d", terminal, terminal),
		// Switching that console, and setting the clock.
		"--cap-add", "SYS_TTY_CONFIG",
		"--cap-add", "SYS_TIME",
		"--cap-add", "SYS_RAWIO",
		// What lets the browser keep its own sandbox: it creates process and
		// network namespaces, which the default seccomp policy refuses
		// without this. Granting it does not turn seccomp off — the policy is
		// capability-aware. See deploy/docker-compose.yml for the trade.
		"--cap-add", "SYS_ADMIN",
		// The directory, not the file: a rename cannot replace a bind mount,
		// and the daemon rewrites its configuration atomically.
		"-v", "/etc/cue:/etc/cue",
		"-v", "/var/lib/cue:/var/lib/cue",
	}
	for _, device := range optionalDevices {
		arguments = append(arguments, "--device", device+":"+device)
	}
	return append(arguments, image, "run")
}

// reportScreen says what the machine ended up driving, which is the thing
// somebody deploying actually wants to know.
func reportScreen(host, name string) {
	output, err := remoteOutput(host, "docker", "exec", name, "/usr/local/bin/cue", "display", "probe")
	if err != nil {
		return
	}
	fmt.Printf("\n%s\n", strings.TrimSpace(output))
}

// sendImage streams the image over ssh. No registry is involved, because a
// device on somebody's network cannot be assumed to reach one.
func sendImage(host, image string) error {
	save := exec.Command("docker", "save", image)
	load := exec.Command("ssh", host, "docker", "load")

	pipe, err := save.StdoutPipe()
	if err != nil {
		return err
	}
	load.Stdin = pipe
	load.Stdout = os.Stdout
	load.Stderr = os.Stderr
	save.Stderr = os.Stderr

	if err := load.Start(); err != nil {
		return err
	}
	if err := save.Run(); err != nil {
		return fmt.Errorf("docker save: %w", err)
	}
	return load.Wait()
}

func step(format string, arguments ...interface{}) {
	fmt.Printf("==> "+format+"\n", arguments...)
}

func remote(host string, arguments ...string) error {
	command := exec.Command("ssh", append([]string{host}, arguments...)...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

func remoteOutput(host string, arguments ...string) (string, error) {
	command := exec.Command("ssh", append([]string{host}, arguments...)...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return output.String(), err
}

func remoteInput(host string, input io.Reader, arguments ...string) error {
	command := exec.Command("ssh", append([]string{host}, arguments...)...)
	command.Stdin = input
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}
