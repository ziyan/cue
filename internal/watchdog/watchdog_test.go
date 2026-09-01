package watchdog

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
)

func testSettings() *config.Watchdog {
	return &config.Watchdog{
		Enabled:                      true,
		Interval:                     config.Duration(20 * time.Millisecond),
		Timeout:                      config.Duration(10 * time.Millisecond),
		FailuresBeforeReload:         2,
		FailuresBeforeRecreate:       4,
		FailuresBeforeClearCache:     6,
		FailuresBeforeRestart:        8,
		FailuresBeforeRestartDisplay: 10,
	}
}

// recorder counts which remedies were applied and in what order, which is the
// whole of what the escalation has to get right.
type recorder struct {
	mutex  sync.Mutex
	steps  []string
	counts map[string]int
}

func newRecorder() *recorder {
	return &recorder{counts: map[string]int{}}
}

func (self *recorder) remedy(name string) func(context.Context) error {
	return func(context.Context) error {
		self.mutex.Lock()
		defer self.mutex.Unlock()
		self.steps = append(self.steps, name)
		self.counts[name]++
		return nil
	}
}

// count reads one remedy's tally under the lock. The test used to read the map
// directly while the watchdog's own goroutine was writing to it.
func (self *recorder) count(name string) int {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.counts[name]
}

// waitFor polls until something is true, and fails the test if it never is.
//
// This exists because the timing tests slept for a fixed sixty milliseconds
// and then asserted that a watchdog running on a ten millisecond interval had
// got round to something. On this machine it always had. On a shared CI runner
// it sometimes had not, and the test failed on work that was merely late.
// Waiting for the condition is both faster in the ordinary case and immune to
// a slow machine.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (self *recorder) applied() []string {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return append([]string(nil), self.steps...)
}

func (self *recorder) remedies() Remedies {
	return Remedies{
		ReloadPage:     self.remedy("reload"),
		RecreatePage:   self.remedy("recreate"),
		ClearCache:     self.remedy("cache"),
		RestartBrowser: self.remedy("restart"),
		RestartDisplay: self.remedy("display"),
	}
}

func TestAHealthyDisplayIsLeftAlone(t *testing.T) {
	applied := newRecorder()
	watchdog := New(testSettings(), applied.remedies())
	watchdog.AddProbe("fine", func(context.Context) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog.Start(ctx)

	time.Sleep(200 * time.Millisecond)

	if steps := applied.applied(); len(steps) != 0 {
		t.Errorf("a healthy display was %v; nothing should have been done to it", steps)
	}
	if state := watchdog.State(); state.ConsecutiveFailures != 0 {
		t.Errorf("consecutive failures is %d, want 0", state.ConsecutiveFailures)
	}
}

func TestFailuresEscalateInOrderAndStopAtEachStep(t *testing.T) {
	applied := newRecorder()
	watchdog := New(testSettings(), applied.remedies())
	watchdog.AddProbe("broken", func(context.Context) error { return fmt.Errorf("wedged") })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog.Start(ctx)

	// Long enough for the failure count to climb past every threshold.
	time.Sleep(700 * time.Millisecond)
	cancel()

	steps := applied.applied()
	if len(steps) == 0 {
		t.Fatal("nothing was tried on a display that never answers")
	}

	// The ladder must be climbed in order: a cheap remedy before an expensive
	// one, and never the other way round.
	rank := map[string]int{"reload": 1, "recreate": 2, "cache": 3, "restart": 4, "display": 5}
	highest := 0
	for _, step := range steps {
		if rank[step] < highest {
			t.Errorf("the remedies were applied as %v, which goes back down the ladder", steps)
			break
		}
		highest = rank[step]
	}
	if steps[0] != "reload" {
		t.Errorf("the first thing tried was %q, want the cheapest remedy", steps[0])
	}
}

func TestOneRungIsNotHammeredWhileTheLadderIsUnclimbed(t *testing.T) {
	// The failure this guards against: with only a reload available, a page
	// that takes forty seconds to load is reloaded every fifteen and never
	// finishes. A step is applied once per episode, and an episode ends when
	// the display answers.
	applied := newRecorder()
	settings := testSettings()
	settings.Interval = config.Duration(10 * time.Millisecond)
	settings.Timeout = config.Duration(5 * time.Millisecond)

	watchdog := New(settings, Remedies{ReloadPage: applied.remedy("reload")})
	watchdog.AddProbe("broken", func(context.Context) error { return fmt.Errorf("wedged") })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog.Start(ctx)

	time.Sleep(300 * time.Millisecond)
	cancel()

	// Thirty probes in that window. The reload fires at two failures, and
	// again only once twice as many have accumulated: 2, 4, 8, 16 — five
	// attempts at the very most, not thirty.
	count := applied.counts["reload"]
	if count == 0 {
		t.Fatal("the page was never reloaded")
	}
	if count > 6 {
		t.Errorf("the page was reloaded %d times in 300ms; the ladder is being hammered", count)
	}
}

func TestAnEpisodeEndsWhenTheDisplayAnswersAgain(t *testing.T) {
	// After a recovery, the cheap remedies must be available again: the next
	// fault should start at the bottom of the ladder, not where the last one
	// left off.
	applied := newRecorder()
	settings := testSettings()
	settings.Interval = config.Duration(10 * time.Millisecond)
	settings.Timeout = config.Duration(5 * time.Millisecond)

	var broken bool
	var mutex sync.Mutex
	setBroken := func(value bool) {
		mutex.Lock()
		defer mutex.Unlock()
		broken = value
	}

	watchdog := New(settings, Remedies{ReloadPage: applied.remedy("reload")})
	watchdog.AddProbe("flaky", func(context.Context) error {
		mutex.Lock()
		defer mutex.Unlock()
		if broken {
			return fmt.Errorf("wedged")
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	setBroken(true)
	watchdog.Start(ctx)
	waitFor(t, "the first remedy", func() bool { return applied.count("reload") > 0 })
	first := applied.count("reload")

	setBroken(false)
	waitFor(t, "the failure count to clear once the display answers",
		func() bool { return watchdog.State().ConsecutiveFailures == 0 })

	setBroken(true)
	waitFor(t, "a remedy after recovering and breaking again",
		func() bool { return applied.count("reload") > first })
	cancel()

	if applied.count("reload") <= first {
		t.Error("after recovering and breaking again, the cheapest remedy was not available")
	}
}

func TestSuspendingStopsFailuresBeingCounted(t *testing.T) {
	// Every deliberate restart suspends the watchdog. Without this, restarting
	// the browser on purpose looks like a fault and escalates into restarting
	// the graphics.
	applied := newRecorder()
	watchdog := New(testSettings(), applied.remedies())
	watchdog.AddProbe("broken", func(context.Context) error { return fmt.Errorf("wedged") })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resume := watchdog.Suspend()
	watchdog.Start(ctx)

	time.Sleep(200 * time.Millisecond)
	if steps := applied.applied(); len(steps) != 0 {
		t.Errorf("a suspended watchdog applied %v", steps)
	}
	if !watchdog.State().Suspended {
		t.Error("the state does not say it is suspended")
	}

	resume()
	if watchdog.State().Suspended {
		t.Error("the state still says it is suspended after resuming")
	}
	if failures := watchdog.State().ConsecutiveFailures; failures != 0 {
		t.Errorf("resuming left %d failures counted; a planned restart should start from zero", failures)
	}
}

func TestNestedSuspendsNeedAsManyResumes(t *testing.T) {
	watchdog := New(testSettings(), Remedies{})

	first := watchdog.Suspend()
	second := watchdog.Suspend()

	first()
	if !watchdog.State().Suspended {
		t.Error("one resume of two lifted the suspension")
	}
	second()
	if watchdog.State().Suspended {
		t.Error("the second resume did not lift the suspension")
	}
}

func TestResumingTwiceIsHarmless(t *testing.T) {
	watchdog := New(testSettings(), Remedies{})
	resume := watchdog.Suspend()
	resume()
	resume()
	if watchdog.State().Suspended {
		t.Error("the watchdog is still suspended")
	}
}

func TestTheFirstProbeToFailIsTheOneReported(t *testing.T) {
	// The order matters: the X server is asked first, because if it is not
	// answering then restarting the browser is pointless.
	watchdog := New(testSettings(), Remedies{})
	watchdog.AddProbe("X server", func(context.Context) error { return fmt.Errorf("no answer") })
	watchdog.AddProbe("browser", func(context.Context) error { return fmt.Errorf("also no answer") })

	name, err := watchdog.ask(context.Background())
	if err == nil {
		t.Fatal("the probes should have failed")
	}
	if name != "X server" {
		t.Errorf("the failure was attributed to %q, want the first probe", name)
	}
}

func TestAProbeThatNeverAnswersFailsAtTheDeadline(t *testing.T) {
	// This is the entire point of the watchdog: a probe that eventually
	// answers after ninety seconds describes a display nobody can read.
	watchdog := New(testSettings(), Remedies{})
	watchdog.AddProbe("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	started := time.Now()
	_, err := watchdog.ask(context.Background())
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("a probe that never answers should fail")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("the probe took %s to give up; the deadline is 10ms", elapsed)
	}
}

func TestADisabledWatchdogDoesNothing(t *testing.T) {
	applied := newRecorder()
	settings := testSettings()
	settings.Enabled = false

	watchdog := New(settings, applied.remedies())
	watchdog.AddProbe("broken", func(context.Context) error { return fmt.Errorf("wedged") })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog.Start(ctx)

	time.Sleep(150 * time.Millisecond)
	if steps := applied.applied(); len(steps) != 0 {
		t.Errorf("a disabled watchdog applied %v", steps)
	}
}

// The watchdog used to hold the settings the daemon was constructed with, and
// the store replaces the whole configuration on every edit -- so turning the
// watchdog on in the file did nothing at all until the next boot, which on a
// display nobody visits is a long way off.
func TestTurningTheWatchdogOnStartsItWatching(t *testing.T) {
	applied := newRecorder()
	settings := testSettings()
	settings.Enabled = false

	watchdog := New(settings, applied.remedies())
	watchdog.AddProbe("broken", func(context.Context) error { return fmt.Errorf("wedged") })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog.Start(ctx)

	time.Sleep(100 * time.Millisecond)
	if steps := applied.applied(); len(steps) != 0 {
		t.Fatalf("a disabled watchdog applied %v", steps)
	}

	enabled := testSettings()
	watchdog.Reconfigure(enabled)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(applied.applied()) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("the watchdog was turned on and never started watching")
}
