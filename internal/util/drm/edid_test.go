package drm

import (
	"testing"
)

// buildEDID makes the 128 bytes a monitor answers with. Written out rather
// than pasted as a hex blob from a real monitor, so that each field this
// decoder reads is visibly placed where the specification puts it — a blob
// would pass whatever the decoder happened to do with it.
func buildEDID(change func(edid []byte)) []byte {
	edid := make([]byte, 128)
	copy(edid, []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00})

	// "DEL", packed as three five-bit letters with A as 1.
	packed := uint16('D'-'A'+1)<<10 | uint16('E'-'A'+1)<<5 | uint16('L'-'A'+1)
	edid[8] = byte(packed >> 8)
	edid[9] = byte(packed)

	edid[10], edid[11] = 0x34, 0x12 // product code 0x1234, little endian
	edid[16] = 20                   // week
	edid[17] = 33                   // 1990 + 33
	edid[18], edid[19] = 1, 4       // version 1.4
	edid[20] = 0x80                 // digital input
	edid[21] = 60                   // 600 mm wide
	edid[22] = 34                   // 340 mm tall

	// First descriptor: a detailed timing of 2560x1440.
	timing := edid[54:72]
	timing[0] = 0x01 // a non-zero pixel clock marks it as a timing
	timing[2] = byte(2560 & 0xff)
	timing[4] = byte((2560 >> 4) & 0xf0)
	timing[5] = byte(1440 & 0xff)
	timing[7] = byte((1440 >> 4) & 0xf0)

	// Second descriptor: the monitor's name.
	name := edid[72:90]
	name[3] = 0xfc
	copy(name[5:], "Test Monitor\n")

	// Third: its serial number.
	serial := edid[90:108]
	serial[3] = 0xff
	copy(serial[5:], "ABC123\n")

	if change != nil {
		change(edid)
	}
	return edid
}

func TestAMonitorDescribesItself(t *testing.T) {
	monitor, ok := DecodeMonitor(buildEDID(nil))
	if !ok {
		t.Fatal("a well formed EDID was refused")
	}

	for name, got := range map[string]string{
		"manufacturer":   monitor.Manufacturer,
		"model":          monitor.Model,
		"serial":         monitor.Serial,
		"preferred mode": monitor.PreferredMode,
		"version":        monitor.Version,
	} {
		if got == "" {
			t.Errorf("%s was not read", name)
		}
	}
	if monitor.Manufacturer != "DEL" {
		t.Errorf("manufacturer is %q, want DEL", monitor.Manufacturer)
	}
	if monitor.Model != "Test Monitor" {
		t.Errorf("model is %q, want %q", monitor.Model, "Test Monitor")
	}
	if monitor.Serial != "ABC123" {
		t.Errorf("serial is %q, want ABC123", monitor.Serial)
	}
	if monitor.PreferredMode != "2560x1440" {
		t.Errorf("preferred mode is %q, want 2560x1440", monitor.PreferredMode)
	}
	if monitor.Year != 2023 {
		t.Errorf("year is %d, want 2023", monitor.Year)
	}
	if monitor.WidthMillimetres != 600 || monitor.HeightMillimetres != 340 {
		t.Errorf("panel is %dx%dmm, want 600x340", monitor.WidthMillimetres, monitor.HeightMillimetres)
	}
	if !monitor.Digital {
		t.Error("a digital input was read as analogue")
	}
}

func TestTheDensityIsTheNumberABrowserScalesBy(t *testing.T) {
	// 2560 pixels across 600 mm is about 108 dots per inch. This is the
	// number that decides whether a page comes out the size it was drawn: a
	// panel that reports its size wrongly is why one screen showed its
	// dashboard shrunk into a corner.
	monitor, _ := DecodeMonitor(buildEDID(nil))
	if density := monitor.DotsPerInch(); density < 105 || density > 112 {
		t.Errorf("density is %d, want about 108", density)
	}

	// And a monitor that gives no size gives no density, rather than a
	// division by zero or a confident wrong answer.
	noSize, _ := DecodeMonitor(buildEDID(func(edid []byte) {
		edid[21], edid[22] = 0, 0
	}))
	if density := noSize.DotsPerInch(); density != 0 {
		t.Errorf("a monitor with no stated size reported %d dpi", density)
	}
}

func TestRubbishIsRefusedRatherThanDecoded(t *testing.T) {
	// A disconnected socket, a short read, and a cheap adapter that answers
	// with nothing useful. Without the header check these decode into a
	// monitor made in 1990 with a zero-by-zero panel, which then appears on
	// the Device page as fact.
	for name, edid := range map[string][]byte{
		"nothing":      nil,
		"too short":    make([]byte, 64),
		"all zeroes":   make([]byte, 128),
		"wrong header": append([]byte{1, 2, 3, 4, 5, 6, 7, 8}, make([]byte, 120)...),
	} {
		if _, ok := DecodeMonitor(edid); ok {
			t.Errorf("%s was decoded as a monitor", name)
		}
	}
}

func TestAMonitorWithNoNameStillIdentifiesItself(t *testing.T) {
	// Plenty give no name descriptor. The product code is not pretty but it
	// is something to search for, and an empty row on the page is not.
	monitor, ok := DecodeMonitor(buildEDID(func(edid []byte) {
		edid[75] = 0x00 // not a name descriptor any more
	}))
	if !ok {
		t.Fatal("refused")
	}
	if monitor.Model != "1234" {
		t.Errorf("model is %q, want the product code 1234", monitor.Model)
	}
}
