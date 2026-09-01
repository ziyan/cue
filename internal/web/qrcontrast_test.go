package web

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// The code must stay readable by a machine, whatever it is made to look like.
//
// The colours were softened because a hard white square on a dark screen is
// the brightest thing in a room at night, and these hang on walls. Softening
// is a matter of taste; contrast is not -- it is the thing a scanner needs,
// and the failure if it goes too far is a code that photographs and does not
// decode, at a distance, in a room somebody has walked to.
//
// So the palette is free to change and this is not: dark on light, and far
// enough apart that no camera has to work at it.
func TestTheCodeStaysReadableByAScanner(t *testing.T) {
	ground := relativeLuminance(t, codeGround)
	modules := relativeLuminance(t, codeModules)

	if modules >= ground {
		t.Fatalf("the modules are not darker than the ground (%.3f against %.3f); "+
			"scanners read dark on light", modules, ground)
	}

	ratio := (ground + 0.05) / (modules + 0.05)
	t.Logf("%s on %s is %.1f:1", codeModules, codeGround, ratio)

	// Well beyond anything a scanner needs, and beyond what text would be
	// asked for. Below this and it is worth measuring against a real camera
	// rather than changing the number.
	if ratio < 12 {
		t.Errorf("the code is %.1f:1, which is not enough for a camera across a room", ratio)
	}
}

// relativeLuminance is the WCAG definition, which is the one contrast ratios
// are built on.
func relativeLuminance(t *testing.T, colour string) float64 {
	t.Helper()
	hex := strings.TrimPrefix(colour, "#")
	if len(hex) != 6 {
		t.Fatalf("%q is not a six-digit colour", colour)
	}
	channel := func(at int) float64 {
		value, err := strconv.ParseInt(hex[at:at+2], 16, 0)
		if err != nil {
			t.Fatalf("%q is not a colour: %s", colour, err)
		}
		part := float64(value) / 255
		if part <= 0.03928 {
			return part / 12.92
		}
		return math.Pow((part+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(0) + 0.7152*channel(2) + 0.0722*channel(4)
}
