package supervise

import (
	"os/signal"
	"syscall"
)

// syscallKill exists so the tests can ask whether a process still exists
// without importing syscall into every test file.
func syscallKill(processId int, signalToSend syscall.Signal) error {
	return syscall.Kill(processId, signalToSend)
}

// blockTerm makes the helper genuinely ignore SIGTERM, so that the only way
// the supervisor can stop it is by escalating to SIGKILL. Without this the
// escalation test would pass whether or not the escalation exists.
func blockTerm() {
	signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
}
