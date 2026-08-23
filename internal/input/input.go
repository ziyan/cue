// Package input reports the keyboards, pointers and touchscreens attached to
// the machine, by reading the listing the kernel maintains at
// /proc/bus/input/devices.
//
// It needs no privileges, no library and no ioctls — which matters, because
// the alternative is opening every /dev/input/event* node and asking it what
// it can do, and that requires permissions a container may well not have.
//
// What this is for: a display with a touchscreen has to be told, in one place,
// that it has one. Chromium behaves differently when touch is available, and
// an operator looking at the Device page wants to know whether the panel the
// screen came with is actually being seen by the machine — which, when a
// touchscreen "does not work", is the first thing worth knowing and the
// hardest to find out on a machine with no shell.
package input

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Device is one input device.
type Device struct {
	Name string `json:"name"`

	// Handlers are the kernel's names for the ways it can be read: "event5",
	// "mouse1", "kbd".
	Handlers []string `json:"handlers"`

	// The three kinds worth telling apart. A device can be more than one: a
	// touchpad is a pointer, and many touchscreens also report a mouse.
	Keyboard bool `json:"keyboard"`
	Pointer  bool `json:"pointer"`
	Touch    bool `json:"touch"`

	// Direct distinguishes a touchscreen, where a finger is on the thing it
	// is pointing at, from a touchpad, where it is not. The kernel says so in
	// the device's property bits.
	Direct bool `json:"direct"`
}

const listingPath = "/proc/bus/input/devices"

// Devices lists every input device the kernel knows about.
func Devices() ([]Device, error) {
	file, err := os.Open(listingPath)
	if err != nil {
		if os.IsNotExist(err) {
			// A container without /proc/bus/input, which is not an error: a
			// screen nobody touches needs no input devices at all.
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var devices []Device
	current := Device{}
	started := false

	flush := func() {
		if started && current.Name != "" {
			devices = append(devices, current)
		}
		current = Device{}
		started = false
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if len(line) < 3 || line[1] != ':' {
			continue
		}

		started = true
		kind, value := line[0], strings.TrimSpace(line[2:])

		switch kind {
		case 'N':
			// N: Name="ELAN06B0:00 04F3:3327 Touchscreen"
			current.Name = strings.Trim(strings.TrimPrefix(value, "Name="), `"`)
		case 'H':
			// H: Handlers=mouse1 event5
			current.Handlers = strings.Fields(strings.TrimPrefix(value, "Handlers="))
			for _, handler := range current.Handlers {
				if handler == "kbd" {
					current.Keyboard = true
				}
				if strings.HasPrefix(handler, "mouse") {
					current.Pointer = true
				}
			}
		case 'B':
			applyBits(&current, value)
		}
	}
	flush()

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return devices, nil
}

// applyBits reads one of the capability lines. The kernel writes them as
// hexadecimal bitmaps, one bit per event code, most significant word first:
//
//	B: PROP=2
//	B: EV=b
//	B: ABS=6618000 0
//
// Only three bits are wanted here, so the whole bitmap is not decoded — just
// asked whether a particular bit is set.
func applyBits(device *Device, value string) {
	name, rest, found := strings.Cut(value, "=")
	if !found {
		return
	}
	bits := strings.Fields(rest)

	switch name {
	case "KEY":
		// A keyboard is a device with letter keys on it. Nearly everything
		// sets EV_KEY — a mouse has buttons, a lid switch has a state, a
		// touchscreen reports contact — so asking whether the device can
		// produce the letter A is what actually distinguishes one.
		if bitSet(bits, keyA) {
			device.Keyboard = true
		}
		// BTN_TOUCH is what a touch device reports on contact.
		if bitSet(bits, btnTouch) {
			device.Touch = true
		}
	case "ABS":
		// ABS_MT_POSITION_X means multi-touch positions, which only a touch
		// device reports.
		if bitSet(bits, absMultiTouchPositionX) {
			device.Touch = true
		}
	case "REL":
		device.Pointer = true
	case "PROP":
		// INPUT_PROP_DIRECT: the finger is on the thing it is pointing at.
		// This is the difference between a touchscreen and a touchpad, and
		// nothing else in the listing distinguishes them.
		if bitSet(bits, inputPropDirect) {
			device.Direct = true
		}
	}
}

// The event codes this package asks about, from the kernel's input-event-codes.h.
const (
	keyA                   = 0x1e
	btnTouch               = 0x14a
	absMultiTouchPositionX = 0x35
	inputPropDirect        = 0x01
)

// bitSet reports whether one bit is set in a bitmap written as hexadecimal
// words, most significant first. Each word is 32 or 64 bits depending on the
// kernel's word size, which is why the width is taken from the text rather
// than assumed.
func bitSet(words []string, bit int) bool {
	if len(words) == 0 {
		return false
	}
	width := len(words[len(words)-1]) * 4
	if width < 32 {
		width = 32
	}

	index := bit / width
	if index >= len(words) {
		return false
	}
	// The words are written most significant first, so the word holding the
	// lowest bits is the last one.
	word := words[len(words)-1-index]

	value, err := strconv.ParseUint(word, 16, 64)
	if err != nil {
		return false
	}
	return value&(1<<uint(bit%width)) != 0
}

// Touchscreens are the input devices a finger is put directly on.
func Touchscreens(devices []Device) []Device {
	var found []Device
	for _, device := range devices {
		if device.Touch && device.Direct {
			found = append(found, device)
		}
	}
	return found
}

// HasTouchscreen reports whether this machine has one, which is what decides
// whether the browser is told touch is available.
func HasTouchscreen(devices []Device) bool {
	return len(Touchscreens(devices)) > 0
}
