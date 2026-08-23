// Package reaper collects the children that nobody else will.
//
// The daemon runs as process 1 inside its container. On Linux, when a process
// exits, its children are re-parented to process 1, and process 1 is expected
// to call wait on them. Chromium leaves several behind every time a renderer
// crashes; a container whose process 1 does not reap fills with zombie
// entries until the process table is exhausted, which on a device nobody
// visits takes a few weeks and looks like the screen freezing for no reason.
//
// The processes the daemon starts itself are waited on by os/exec, which
// handles them correctly; this is only for orphans, which os/exec knows
// nothing about.
package reaper

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("reaper")

// Start reaps orphaned children until the context is cancelled. It does
// nothing at all unless this process is process 1, because on a developer's
// machine an indiscriminate wait would steal the exit status of the very
// processes the supervisor is waiting for.
func Start(ctx context.Context) {
	if os.Getpid() != 1 {
		log.Debugf("not process 1, so nothing to reap")
		return
	}

	// SIGCHLD is delivered when a child changes state. Waiting on the signal
	// rather than polling means an orphan is collected immediately and the
	// process sleeps the rest of the time.
	children := make(chan os.Signal, 1)
	signal.Notify(children, syscall.SIGCHLD)

	go func() {
		defer signal.Stop(children)
		for {
			select {
			case <-ctx.Done():
				return
			case <-children:
				reap()
			}
		}
	}()
	log.Debugf("reaping orphaned children")
}

// reap collects every child that has exited, without blocking. One SIGCHLD
// can stand for several children, because signals are not queued.
func reap() {
	for {
		var status syscall.WaitStatus
		processId, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if err != nil {
			// ECHILD simply means there is nothing left to collect, which is
			// the normal way out of this loop.
			if err != syscall.ECHILD {
				log.Debugf("wait4: %s", err)
			}
			return
		}
		if processId <= 0 {
			return
		}
		log.Debugf("reaped orphaned process %d (%s)", processId, describe(status))
	}
}

func describe(status syscall.WaitStatus) string {
	switch {
	case status.Signaled():
		return "killed by " + status.Signal().String()
	case status.Exited():
		if status.ExitStatus() == 0 {
			return "exited cleanly"
		}
		return "exited with status " + strconv.Itoa(status.ExitStatus())
	default:
		return "stopped"
	}
}
