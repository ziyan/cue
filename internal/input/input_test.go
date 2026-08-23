package input

import "testing"

func TestApplyBitsTellsAKeyboardFromEverythingElseThatHasKeys(t *testing.T) {
	// Nearly every input device sets EV_KEY: a mouse has buttons, a lid
	// switch has a state, a touchscreen reports contact. What makes a
	// keyboard a keyboard is having letter keys.
	keyboard := Device{}
	// A bitmap with KEY_A (0x1e) set: bit 30 of the lowest word.
	applyBits(&keyboard, "KEY=402000007 ff803078f800d001 feffffdfffcfffff fffffffffffffffe")
	if !keyboard.Keyboard {
		t.Error("a keyboard was not recognised as one")
	}

	mouse := Device{}
	// BTN_LEFT and friends, and no letters.
	applyBits(&mouse, "KEY=70000 0 0 0 0")
	if mouse.Keyboard {
		t.Error("a mouse was reported as a keyboard")
	}
}

func TestApplyBitsFindsATouchDevice(t *testing.T) {
	device := Device{}
	// ABS_MT_POSITION_X is 0x35, which is bit 53: the second word from the end.
	applyBits(&device, "ABS=260800000000003")
	if !device.Touch {
		t.Error("a device reporting multi-touch positions was not recognised")
	}
}

func TestDirectIsWhatSeparatesAScreenFromAPad(t *testing.T) {
	// A touchpad and a touchscreen look identical in every other respect;
	// INPUT_PROP_DIRECT is the only thing that says the finger is on the
	// thing it is pointing at.
	screen := Device{Touch: true}
	applyBits(&screen, "PROP=2")
	if !screen.Direct {
		t.Error("INPUT_PROP_DIRECT was not read")
	}

	pad := Device{Touch: true}
	applyBits(&pad, "PROP=5")
	if pad.Direct {
		t.Error("a touchpad was reported as direct")
	}

	if !HasTouchscreen([]Device{pad, screen}) {
		t.Error("a machine with a touchscreen reported none")
	}
	if HasTouchscreen([]Device{pad}) {
		t.Error("a machine with only a touchpad reported a touchscreen")
	}
}

func TestBitSetReadsTheKernelsMostSignificantFirstBitmaps(t *testing.T) {
	// The kernel writes these words most significant first, so the word
	// holding the lowest bits is the last one. Getting this backwards makes
	// every capability wrong in a way that still looks plausible.
	if !bitSet([]string{"1"}, 0) {
		t.Error("bit 0 of 0x1 was not set")
	}
	if bitSet([]string{"1"}, 1) {
		t.Error("bit 1 of 0x1 was set")
	}
	// Two words: the second is the low one. Bit 32 is bit 0 of the first.
	if !bitSet([]string{"1", "0000000000000000"}, 64) {
		t.Error("bit 64 across two 64-bit words was not found")
	}
	if bitSet([]string{"1"}, 500) {
		t.Error("a bit beyond the end of the bitmap was reported as set")
	}
	if bitSet(nil, 0) {
		t.Error("an empty bitmap reported a bit as set")
	}
	if bitSet([]string{"nonsense"}, 0) {
		t.Error("a bitmap that is not hexadecimal reported a bit as set")
	}
}

func TestTouchscreensPicksOutOnlyTheDirectOnes(t *testing.T) {
	devices := []Device{
		{Name: "a keyboard", Keyboard: true},
		{Name: "a touchpad", Touch: true, Pointer: true},
		{Name: "a touchscreen", Touch: true, Direct: true},
	}
	found := Touchscreens(devices)
	if len(found) != 1 || found[0].Name != "a touchscreen" {
		t.Errorf("found %v, want only the touchscreen", found)
	}
}
