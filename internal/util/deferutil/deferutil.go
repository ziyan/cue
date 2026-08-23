// Package deferutil holds the recover that every goroutine in this project
// defers. A panic in one goroutine takes the whole process down, and this
// process is a container's process 1: taking it down turns a display off.
package deferutil

import (
	"runtime/debug"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("deferutil")

// Recover swallows a panic and logs it with a stack trace. Defer it as the
// first statement of every goroutine that is not the main one.
func Recover() {
	if message := recover(); message != nil {
		log.Errorf("recovered from a panic: %s\n%s", message, string(debug.Stack()))
	}
}
