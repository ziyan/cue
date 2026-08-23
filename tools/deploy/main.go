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
	wait := flag.Duration("wait", 0,
		"wait this long for the machine to appear before giving up, instead of failing immediately when it is off")
	terminal := flag.Int("virtual-terminal", 2, "the console the X server draws on; must match display.virtualTerminal")
	flag.Parse()

	if *host == "" {
		fmt.Fprintln(os.Stderr, "deploy: -host is needed")
		os.Exit(2)
	}

	if *wait > 0 {
		if err := waitForHost(*host, *wait); err != nil {
			fmt.Fprintf(os.Stderr, "\ndeploy: %s\n", err)
			os.Exit(1)
		}
	}

	if err := deploy(*host, *image, *name, *configFile, *terminal, *stopDisplayManager); err != nil {
		fmt.Fprintf(os.Stderr, "\ndeploy: FAILED: %s\n", err)
		os.Exit(1)
	}
}

// waitForHost polls until the machine answers, which is what -wait is for: a
// device that is switched off cannot be deployed to, and somebody who is about
// to switch it on should not have to come back and run this afterwards. Start
// it, press the power button, walk away.
func waitForHost(host string, limit time.Duration) error {
	step("waiting up to %s for %s to answer", limit.Round(time.Second), host)

	deadline := time.Now().Add(limit)
	announced := false
	for time.Now().Before(deadline) {
		if remote(host, "true") == nil {
			if announced {
				fmt.Println()
			}
			fmt.Printf("    %s is up\n", host)
			return nil
		}
		if !announced {
			fmt.Printf("    not answering yet; switch it on and this will carry on by itself")
			announced = true
		}
		fmt.Print(".")
		time.Sleep(15 * time.Second)
	}
	if announced {
		fmt.Println()
	}
	return fmt.Errorf("%s did not answer within %s; it is switched off, or on another network", host, limit)
}

func deploy(host, image, name, configFile string, terminal int, stopDisplayManager bool) error {
	step("checking that %s is reachable and has Docker", host)
	version, err := remoteOutput(host, "docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return fmt.Errorf("cannot reach Docker on %s: %w", host, err)
	}
	fmt.Printf("    Docker %s\n", strings.TrimSpace(version))

	// Stop the previous deployment before looking at what holds the graphics
	// device, or its own X server is found and reported as the thing in the
	// way — which is true, and unhelpful, and stops a redeployment dead.
	step("stopping any previous deployment")
	_ = remote(host, "docker", "rm", "-f", name)

	if stopDisplayManager {
		step("stopping anything that holds the graphics device")

		// Stopping and disabling are two different things and both are
		// needed, in that order. Debian's gdm is a *static* unit and gdm3 is
		// an alias for it, so "systemctl disable" fails on both and leaves
		// the greeter running — which was diagnosed once as "something is
		// still running an X server" with no clue as to what to do about it.
		for _, manager := range []string{"gdm", "gdm3", "lightdm", "sddm", "xdm"} {
			// Failure is the normal case: most of these are not installed.
			_ = remote(host, "systemctl", "stop", manager)
			_ = remote(host, "systemctl", "disable", manager)
		}

		// And the target that starts one at boot. Without this the display
		// manager comes back on the next reboot and takes the graphics device
		// before the daemon can, so the screen works until the machine is
		// restarted and then does not. Undo with
		// "systemctl set-default graphical.target".
		if err := remote(host, "systemctl", "set-default", "multi-user.target"); err != nil {
			fmt.Println("    could not set the boot target; a display manager may come back on the next restart")
		} else {
			fmt.Println("    this machine will now boot without a desktop; undo with set-default graphical.target")
		}
		var remaining []string
		for _, server := range []string{"Xorg", "Xwayland"} {
			output, err := remoteOutput(host, "pgrep", "-a", "-x", server)
			// pgrep exits non-zero when it matches nothing, which is what
			// success looks like here.
			if err == nil && strings.TrimSpace(output) != "" {
				remaining = append(remaining, strings.TrimSpace(output))
			}
		}
		if len(remaining) > 0 {
			return fmt.Errorf("something is still running an X server on %s, and it holds the graphics device:\n%s",
				host, strings.Join(remaining, "\n"))
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
		// Published ports rather than the host's network namespace. Host
		// networking looks like the obvious choice for an appliance and was
		// the first thing tried, but Chromium does not honour
		// --ignore-certificate-errors in it: a self-signed certificate — which
		// is what every appliance on a private network has — is refused with
		// ERR_CERT_AUTHORITY_INVALID, while the same image on a bridge
		// network loads the same page. Public certificates work either way,
		// so nothing about it looks broken until the one page that matters
		// will not load.
		//
		// Nothing is lost: the interface and the VNC server answer on the
		// machine's address through these, and hotplug is noticed by polling
		// /sys rather than by kernel events, which a container never receives
		// on a bridge network anyway.
		"-p", "8080:8080",
		"-p", "5900:5900",
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

// quoteForRemoteShell renders one argument so that the shell on the other end
// treats it as a single word.
//
// This is not optional and getting it wrong is not obvious. ssh does not take
// an argument list: it joins whatever it is given with spaces and hands the
// result to a shell on the remote machine, which parses it again. So an
// argument containing a pipe, a bracket or a space means something entirely
// different there. Asking whether an X server was still running with the
// pattern "Xorg|Xwayland" ran pgrep and piped its output into Xwayland, which
// tried to start an X server, failed, and was reported as the X server that
// was still running — a refusal to deploy caused by the check itself.
func quoteForRemoteShell(argument string) string {
	if argument == "" {
		return "''"
	}
	safe := true
	for _, character := range argument {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("-_./:=,+@", character):
		default:
			safe = false
		}
		if !safe {
			break
		}
	}
	if safe {
		return argument
	}
	// Single quotes protect everything except a single quote, which is ended,
	// escaped, and reopened.
	return "'" + strings.ReplaceAll(argument, "'", `'''`) + "'"
}

// remoteCommand builds the ssh invocation, quoting every argument for the
// shell that will parse them on the other side.
func remoteCommand(host string, arguments []string) *exec.Cmd {
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		quoted = append(quoted, quoteForRemoteShell(argument))
	}
	return exec.Command("ssh", host, strings.Join(quoted, " "))
}

// remote runs a command on the machine. A failure carries what was said with
// it: a deployment that stops with "exit status 1" and nothing else is a
// deployment somebody has to reproduce by hand to understand.
func remote(host string, arguments ...string) error {
	output, err := remoteOutput(host, arguments...)
	if err != nil {
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			return fmt.Errorf("%s: %w: %s", strings.Join(arguments, " "), err, trimmed)
		}
		return fmt.Errorf("%s: %w", strings.Join(arguments, " "), err)
	}
	return nil
}

func remoteOutput(host string, arguments ...string) (string, error) {
	command := remoteCommand(host, arguments)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return output.String(), err
}

func remoteInput(host string, input io.Reader, arguments ...string) error {
	command := remoteCommand(host, arguments)
	command.Stdin = input
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}
