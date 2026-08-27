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
		// With what ssh or docker actually said. Without it this is "exit
		// status 1" against the first step of a deployment, and the reasons
		// are all different things to do about them: no such host, no key,
		// the wrong account, no Docker, a Docker that will not talk to this
		// account.
		if trimmed := strings.TrimSpace(version); trimmed != "" {
			return fmt.Errorf("cannot reach Docker on %s: %w: %s", host, err, trimmed)
		}
		return fmt.Errorf("cannot reach Docker on %s: %w "+
			"(is it \"root@%s\"? this connects as whoever ssh would)", host, err, host)
	}
	fmt.Printf("    Docker %s\n", strings.TrimSpace(version))

	// The image goes first, before anything running on the machine is
	// touched.
	//
	// It used to go after, and that cost a screen seventy minutes of being
	// dark. The order was: remove the running container, stop the display
	// manager, then send eight hundred megabytes over the network. The
	// network went away during the send — it was a link between two sites and
	// it does that — and the machine was left with no container, no display
	// manager, and no image to make a new one from. Every step that had run
	// had succeeded.
	//
	// Sending first costs nothing: loading an image the machine already has
	// is quick, and a container that is running keeps its own reference to
	// the image it started from, so nothing is disturbed by the new one
	// arriving. If the transfer fails now, the screen is still showing what
	// it was showing.
	step("sending %s", image)
	if err := sendImage(host, image); err != nil {
		return fmt.Errorf("%w (nothing on %s was changed)", err, host)
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

	// Everything above this line leaves the machine as it was found, and
	// everything below it takes the screen down. They are in that order on
	// purpose.
	//
	// They were not, once. Preparing the directories came after the old
	// container was removed, and a deployment to a machine that went away
	// between the two left a device with no container at all -- a blank
	// screen, and nothing running to put anything on it. The machine in
	// question was a wall display; the failure was noticed by somebody
	// looking at the wall.
	//
	// So nothing is stopped until the last thing that can fail has succeeded,
	// and the window in which the screen is dark is as short as starting a
	// container.
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
		// Say this before returning: by now the previous deployment has been
		// stopped, so the failure below has left a blank screen behind it, and
		// somebody reading only the error would not know that.
		fmt.Printf("\n    %s now has nothing running and its screen is blank.\n"+
			"    Run this again once the reason below is dealt with.\n\n", host)
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
		// Bound the log, because nothing else does. Docker's json-file driver
		// keeps everything by default, and a screen is a machine nobody logs
		// in to for a year at a time: whatever it repeats, it repeats until
		// the disk is full and then the machine stops doing anything at all.
		// On the device this project replaces, the browser's own log turned
		// over 50 MB in one day and, at its worst, 10 MB every four minutes.
		"--log-opt", "max-size=10m",
		"--log-opt", "max-file=5",
		// The host's network namespace. The interface and the VNC server then
		// answer on the machine's own address, kernel hotplug events arrive,
		// and — the reason it is not optional — the daemon can manage the
		// machine's own network interfaces, which live in that namespace and
		// nowhere else.
		//
		// It costs one thing, and it is worth knowing about: the browser's
		// debugging port is then on a namespace shared with everything else
		// on the machine. That is why the daemon lets the browser choose its
		// own port and reads it back from the profile rather than assuming
		// one. Assuming 9222 meant driving a headless Chrome that something
		// unrelated had left there, while the screen showed a page nobody had
		// ever navigated.
		"--network", "host",
		// Configuring interfaces, and joining wireless networks.
		"--cap-add", "NET_ADMIN",
		// Chromium exhausts Docker's default 64 megabytes and then crashes
		// tabs with no explanation anybody can act on.
		"--shm-size", "1g",
		// The machine's device database, read only.
		//
		// The X server does not go looking in /dev/input; it asks udev what
		// input devices exist, and udev answers out of this directory. A
		// container without it gets an X server that says "the server relies
		// on udev to provide the list of input devices" and then adds none of
		// them — so the screen has no keyboard and no mouse, while
		// /dev/input is full of them and the daemon's own Device page lists
		// every one, because that reads the kernel directly.
		"-v", "/run/udev:/run/udev:ro",
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
	// ssh is told to give up on a link that has gone rather than hold the
	// connection open forever, and to notice one that goes away mid-transfer.
	// Without these a send into a dead link does not fail, it stalls: the
	// first version of this sat for minutes with nothing happening and no
	// error, which is the worst of both — too slow to wait for and too quiet
	// to notice.
	load := exec.Command("ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=20",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=4",
		host, "docker", "load")
	save := exec.Command("docker", "save", image)

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

	// This process's own copy of the read end is closed, leaving ssh holding
	// the only one.
	//
	// Without it a send into a dead link hangs for ever rather than failing.
	// StdoutPipe hands the read end to *this* process, and starting ssh gives
	// it a second copy; if this one stays open then ssh exiting does not close
	// the pipe, "docker save" never gets a broken pipe, and it blocks on a
	// full buffer with nothing at the other end. Reproduced against a host
	// that does not resolve: the send sat there for minutes, silently, which
	// is exactly what a deployment did on the night it took a screen down.
	_ = pipe.Close()

	// Both are waited on, and both errors matter. When the far end dies the
	// two fail together — ssh with its own status, docker save with a broken
	// pipe — and reporting only one of them was how a failed send came to
	// look like a successful one.
	saveErr := save.Run()
	loadErr := load.Wait()

	switch {
	case loadErr != nil && saveErr != nil:
		return fmt.Errorf("sending the image failed: %w (and docker save: %v)", loadErr, saveErr)
	case loadErr != nil:
		return fmt.Errorf("sending the image failed: %w", loadErr)
	case saveErr != nil:
		return fmt.Errorf("docker save: %w", saveErr)
	}
	return nil
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
