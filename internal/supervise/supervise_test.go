package supervise

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests drive real processes, because everything worth testing about a
// supervisor — process groups, signals, exit codes — is exactly the part a
// fake would not have. Rather than depend on a shell, which the container
// image deliberately does not have, the test binary re-executes itself: a
// child sees CUE_TEST_HELPER in its environment and behaves as the named
// helper instead of running tests.
func TestMain(m *testing.M) {
	if behaviour := os.Getenv("CUE_TEST_HELPER"); behaviour != "" {
		helperMain(behaviour)
		return
	}
	os.Exit(m.Run())
}

func helperMain(behaviour string) {
	switch behaviour {
	case "exit-immediately":
		os.Exit(3)
	case "run-forever":
		fmt.Println("helper is up")
		select {}
	case "touch-then-run-forever":
		if filename := os.Getenv("CUE_TEST_FILE"); filename != "" {
			existing, _ := os.ReadFile(filename)
			count, _ := strconv.Atoi(strings.TrimSpace(string(existing)))
			_ = os.WriteFile(filename, []byte(strconv.Itoa(count+1)), 0o600)
		}
		select {}
	case "ignore-sigterm":
		// Used to prove the supervisor escalates to SIGKILL.
		blockTerm()
		select {}
	case "spawn-child-then-run-forever":
		// The child is deliberately left running, to prove that stopping the
		// parent stops the whole process group.
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "CUE_TEST_HELPER=run-forever")
		_ = child.Start()
		if filename := os.Getenv("CUE_TEST_FILE"); filename != "" {
			_ = os.WriteFile(filename, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		}
		select {}
	default:
		fmt.Fprintf(os.Stderr, "unknown helper %q\n", behaviour)
		os.Exit(64)
	}
}

func helperSettings(name, behaviour string, environment ...string) *Settings {
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return &Settings{
		Name:        name,
		Path:        executable,
		Environment: append([]string{"CUE_TEST_HELPER=" + behaviour}, environment...),
		StopTimeout: 2 * time.Second,
	}
}

func TestAProgramThatStaysUpIsReported(t *testing.T) {
	process := New(helperSettings("forever", "run-forever"))
	process.Start(context.Background())
	defer process.Stop(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := process.WaitReady(ctx); err != nil {
		t.Fatalf("wait ready: %s", err)
	}

	status := process.Status()
	if status.State != StateRunning {
		t.Errorf("state is %q, want %q", status.State, StateRunning)
	}
	if status.ProcessID == 0 {
		t.Error("no process id was reported")
	}
	if status.Restarts != 0 {
		t.Errorf("restarts is %d, want 0", status.Restarts)
	}
}

func TestAProgramThatKeepsExitingIsRestartedWithAGrowingBackoff(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "starts")

	settings := helperSettings("flapping", "touch-then-run-forever", "CUE_TEST_FILE="+counter)
	settings.Restart = true
	settings.MinimumBackoff = 10 * time.Millisecond
	settings.MaximumBackoff = 40 * time.Millisecond
	// The helper runs forever, so make the supervisor stop it itself by
	// giving it a readiness check that never passes: the program is killed,
	// which is the same path a crash takes.
	settings.Ready = func(ctx context.Context) error { return fmt.Errorf("never ready") }
	settings.ReadyTimeout = 20 * time.Millisecond
	settings.ReadyInterval = 5 * time.Millisecond

	process := New(settings)
	process.Start(context.Background())
	defer process.Stop(context.Background())

	// Four starts at 10, 20 and 40 milliseconds of backoff is well inside a
	// second even on a loaded machine.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(counter)
		if err == nil {
			count, _ := strconv.Atoi(strings.TrimSpace(string(content)))
			if count >= 4 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	content, _ := os.ReadFile(counter)
	t.Fatalf("the program was started %q times in five seconds, want at least 4", content)
}

func TestStoppingKillsTheWholeProcessGroup(t *testing.T) {
	// This is the failure that matters: Chromium is a tree of processes, and
	// signalling only its root leaves renderers holding the graphics device
	// open, so the next browser cannot start.
	childFile := filepath.Join(t.TempDir(), "child")

	settings := helperSettings("parent", "spawn-child-then-run-forever", "CUE_TEST_FILE="+childFile)
	process := New(settings)
	process.Start(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := process.WaitReady(ctx); err != nil {
		t.Fatalf("wait ready: %s", err)
	}

	var childProcessId int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(childFile)
		if err == nil {
			childProcessId, _ = strconv.Atoi(strings.TrimSpace(string(content)))
			if childProcessId > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childProcessId == 0 {
		t.Fatal("the helper never reported a child process")
	}

	process.Stop(context.Background())

	// The child should be gone. Signal 0 asks whether it still exists.
	for attempt := 0; attempt < 100; attempt++ {
		if err := syscallKill(childProcessId, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscallKill(childProcessId, 9)
	t.Fatalf("the child process %d survived its parent being stopped", childProcessId)
}

func TestStoppingEscalatesToSIGKILL(t *testing.T) {
	settings := helperSettings("stubborn", "ignore-sigterm")
	settings.StopTimeout = 300 * time.Millisecond

	process := New(settings)
	process.Start(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := process.WaitReady(ctx); err != nil {
		t.Fatalf("wait ready: %s", err)
	}

	started := time.Now()
	stopContext, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	process.Stop(stopContext)

	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Errorf("stopping took %s; the escalation to SIGKILL did not happen", elapsed)
	}
	if state := process.State(); state != StateStopped {
		t.Errorf("state is %q after stopping, want %q", state, StateStopped)
	}
}

func TestAProgramThatCannotStartIsReportedRatherThanRetriedForever(t *testing.T) {
	settings := &Settings{
		Name:    "missing",
		Path:    "/nonexistent/definitely-not-here",
		Restart: false,
	}
	process := New(settings)
	process.Start(context.Background())
	defer process.Stop(context.Background())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status := process.Status()
		if status.LastError != "" {
			if !strings.Contains(status.LastError, "cannot start") {
				t.Errorf("the error was %q, which does not say what went wrong", status.LastError)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("a program that cannot start should report why")
}

func TestEnvironOverridesRatherThanAppends(t *testing.T) {
	environment := Environ([]string{"DISPLAY=:0", "HOME=/root"}, map[string]string{"DISPLAY": ":9"})
	found := 0
	for _, entry := range environment {
		if strings.HasPrefix(entry, "DISPLAY=") {
			found++
			if entry != "DISPLAY=:9" {
				t.Errorf("DISPLAY is %q, want :9", entry)
			}
		}
	}
	if found != 1 {
		t.Errorf("DISPLAY appears %d times, want once; a duplicate is resolved differently by different programs", found)
	}
}

func TestTheCommandLineIsRebuiltForEveryStart(t *testing.T) {
	// A program's command line comes from the configuration, and the
	// configuration changes while the daemon runs. Captured once, a change
	// that asks for a restart restarts the program into exactly the command
	// line it already had: the screen blanks, every log line says the change
	// was applied, and the setting is not in force.
	current := []string{"--first"}
	settings := &Settings{
		Name:           "test",
		Path:           "/bin/true",
		BuildArguments: func() []string { return current },
	}

	if got := strings.Join(settings.CommandLine(), " "); got != "--first" {
		t.Fatalf("first start got %q", got)
	}

	current = []string{"--second", "--third"}
	if got := strings.Join(settings.CommandLine(), " "); got != "--second --third" {
		t.Errorf("after the configuration changed the command line was still %q", got)
	}
}

func TestAStaticCommandLineStillWorks(t *testing.T) {
	settings := &Settings{Name: "test", Arguments: []string{"--only"}}
	if got := strings.Join(settings.CommandLine(), " "); got != "--only" {
		t.Errorf("a program with fixed arguments got %q", got)
	}
}
