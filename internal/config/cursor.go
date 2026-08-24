package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// CursorMode is what the mouse pointer does on a screen nobody is standing at.
//
// It reads as a boolean as well as a word, because it was a boolean first and
// every device already in service has "cursor: false" written in its file. A
// setting that changes shape has to keep accepting what it used to accept, or
// the upgrade is a daemon that will not start.
type CursorMode string

const (
	// CursorHidden: no pointer, ever. The X server is started without one.
	CursorHidden CursorMode = "hidden"

	// CursorAuto: no pointer until somebody moves it, then gone again once
	// they stop. What a screen with a mouse or a touchscreen attached wants,
	// and harmless on one with neither, where nothing ever moves.
	CursorAuto CursorMode = "auto"

	// CursorAlways: a pointer at all times, as any other machine has.
	CursorAlways CursorMode = "always"
)

// UnmarshalYAML accepts "hidden", "auto", "always", and the true and false
// this used to be.
func (self *CursorMode) UnmarshalYAML(node *yaml.Node) error {
	var asBool bool
	if err := node.Decode(&asBool); err == nil {
		if asBool {
			*self = CursorAlways
		} else {
			*self = CursorHidden
		}
		return nil
	}

	var asString string
	if err := node.Decode(&asString); err != nil {
		return fmt.Errorf("must be hidden, auto or always")
	}
	switch mode := CursorMode(strings.ToLower(strings.TrimSpace(asString))); mode {
	case CursorHidden, CursorAuto, CursorAlways:
		*self = mode
		return nil
	case "":
		*self = CursorAuto
		return nil
	default:
		return fmt.Errorf("must be hidden, auto or always, not %q", asString)
	}
}

// MarshalYAML writes the word, never the boolean: the file is rewritten by the
// interface, and writing back "false" would keep the old spelling alive for
// ever.
func (self CursorMode) MarshalYAML() (interface{}, error) {
	if self == "" {
		return string(CursorAuto), nil
	}
	return string(self), nil
}

// Valid reports whether this is one of the three.
func (self CursorMode) Valid() bool {
	switch self {
	case CursorHidden, CursorAuto, CursorAlways:
		return true
	}
	return false
}

// ServerDrawsOne reports whether the X server should be started with a cursor
// at all. Without one there is nothing to show later, so "auto" needs it.
func (self CursorMode) ServerDrawsOne() bool {
	return self != CursorHidden
}
