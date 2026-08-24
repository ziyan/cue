package xserver

import (
	"os"
)

// The X server finds keyboards and mice through udev, not by looking in
// /dev/input. It says so itself, once, in its log:
//
//	(II) The server relies on udev to provide the list of input devices.
//
// udev answers out of a database in /run/udev, which belongs to the machine.
// A container given /dev/input but not that database gets an X server that
// enumerates nothing: the screen has no keyboard and no mouse, and every other
// sign is healthy. /dev/input is full of devices, the daemon's own Device page
// lists all of them — it reads the kernel directly — and the X server's log
// says nothing that looks like an error, because from its point of view
// nothing went wrong. It asked, and the answer was none.
//
// So the daemon checks for itself and says what to do about it.
// udevDirectory is a variable only so the tests can point it elsewhere.
var udevDirectory = "/run/udev/data"

// inputWillWork reports whether the X server will be able to find input
// devices, and what to say if it will not.
func inputWillWork() (problem string, ok bool) {
	entries, err := os.ReadDir(udevDirectory)
	if err != nil {
		return "the X server finds keyboards and mice by asking udev, and this container has no " +
			udevDirectory + " to answer from, so the screen will have neither. " +
			"Mount the machine's database read only: -v /run/udev:/run/udev:ro", false
	}
	if len(entries) == 0 {
		return udevDirectory + " is empty, so the X server will find no keyboard and no mouse. " +
			"If it is mounted from the machine, the machine's udev has not populated it", false
	}
	return "", true
}
