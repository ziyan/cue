package drm

import (
	"fmt"
	"strings"
)

// What a monitor says about itself.
//
// Every monitor answers a small structure over the display cable — its maker,
// its model, how big its panel physically is, and the timings it can be driven
// at. The kernel puts the raw bytes in /sys/class/drm/<connector>/edid and
// decodes none of it.
//
// It is worth decoding because almost every hard question about one of these
// screens is answered by it. Why is the browser scaling the page: because the
// panel claims a physical size that works out to 72 dots per inch. Why is the
// picture soft: because the mode being driven is not the panel's native one.
// Which of the four sockets is the television on: the one whose monitor is
// called what the television is called. Without this, all of that is guesswork
// from a name and a list of sizes.

// Monitor is a decoded EDID.
type Monitor struct {
	// Manufacturer is the three-letter PNP code — DEL, SAM, GSM — which is
	// how monitors identify their maker. Not expanded to a company name:
	// the list is long, proprietary, and out of date the moment it is copied.
	Manufacturer string `json:"manufacturer"`

	// Model is the name the monitor gives, when it gives one, and otherwise
	// the numeric product code its maker assigned.
	Model string `json:"model"`

	// Serial is the unit's own serial number, when it publishes one.
	Serial string `json:"serial,omitempty"`

	// Year is when it was made. Useful mostly for working out whether the
	// thing on the wall is the one somebody remembers buying.
	Year int `json:"year,omitempty"`

	// WidthMillimetres and HeightMillimetres are the size of the panel
	// itself. This is what a browser turns into a scale factor, and a monitor
	// that reports it wrongly is why a page comes out zoomed.
	WidthMillimetres  int `json:"widthMillimetres,omitempty"`
	HeightMillimetres int `json:"heightMillimetres,omitempty"`

	// PreferredMode is the one the panel is actually made of — its native
	// resolution. Driving anything else means the monitor is scaling, which
	// on a dashboard of small text is the difference between readable and
	// not.
	PreferredMode string `json:"preferredMode,omitempty"`

	// Digital is false for the analogue VGA input still found on projectors
	// and on the graphics built into server boards.
	Digital bool `json:"digital"`

	// Version is the EDID structure version, "1.4" and so on.
	Version string `json:"version,omitempty"`
}

// DotsPerInch is the density the monitor's own numbers imply, along its
// width. Zero when it did not say how big it is.
//
// This is the number a browser turns into a page scale, so it is the number to
// look at when a dashboard comes out too large or too small.
func (self Monitor) DotsPerInch() int {
	if self.WidthMillimetres <= 0 || self.PreferredMode == "" {
		return 0
	}
	width, _, ok := parseMode(self.PreferredMode)
	if !ok {
		return 0
	}
	return int(float64(width) / (float64(self.WidthMillimetres) / 25.4))
}

// Describe is a one-line summary for a log or a listing.
func (self Monitor) Describe() string {
	parts := make([]string, 0, 4)
	if name := strings.TrimSpace(self.Manufacturer + " " + self.Model); name != "" {
		parts = append(parts, name)
	}
	if self.PreferredMode != "" {
		parts = append(parts, self.PreferredMode)
	}
	if self.WidthMillimetres > 0 && self.HeightMillimetres > 0 {
		parts = append(parts, fmt.Sprintf("%dx%dmm", self.WidthMillimetres, self.HeightMillimetres))
	}
	if density := self.DotsPerInch(); density > 0 {
		parts = append(parts, fmt.Sprintf("%d dpi", density))
	}
	return strings.Join(parts, ", ")
}

// The fixed layout of the 128 bytes every EDID starts with.
const (
	edidMinimumLength = 128

	edidManufacturerOffset = 8
	edidProductOffset      = 10
	edidSerialOffset       = 12
	edidWeekOffset         = 16
	edidYearOffset         = 17
	edidVersionOffset      = 18
	edidRevisionOffset     = 19
	edidInputOffset        = 20
	edidWidthOffset        = 21
	edidHeightOffset       = 22

	edidDescriptorStart  = 54
	edidDescriptorLength = 18
	edidDescriptorCount  = 4

	descriptorSerial = 0xff
	descriptorName   = 0xfc
)

// DecodeMonitor reads what a monitor says about itself.
//
// A connector with nothing plugged into it has no EDID at all, and one with a
// cheap adapter in the way often has a truncated or nonsense one, so anything
// that cannot be read is left empty rather than guessed at.
func DecodeMonitor(edid []byte) (Monitor, bool) {
	if len(edid) < edidMinimumLength || !hasEDIDHeader(edid) {
		return Monitor{}, false
	}

	monitor := Monitor{
		Manufacturer: manufacturerCode(edid[edidManufacturerOffset], edid[edidManufacturerOffset+1]),
		Digital:      edid[edidInputOffset]&0x80 != 0,
		Version: fmt.Sprintf("%d.%d",
			edid[edidVersionOffset], edid[edidRevisionOffset]),
		WidthMillimetres:  int(edid[edidWidthOffset]) * 10,
		HeightMillimetres: int(edid[edidHeightOffset]) * 10,
	}
	if year := int(edid[edidYearOffset]); year > 0 {
		// The byte holds the year since 1990.
		monitor.Year = 1990 + year
	}

	for index := 0; index < edidDescriptorCount; index++ {
		offset := edidDescriptorStart + index*edidDescriptorLength
		if offset+edidDescriptorLength > len(edid) {
			break
		}
		descriptor := edid[offset : offset+edidDescriptorLength]

		// A descriptor beginning with two zero bytes is text or a marker; one
		// that does not is a detailed timing, and the first of those is the
		// panel's native mode.
		if descriptor[0] != 0 || descriptor[1] != 0 {
			if monitor.PreferredMode == "" {
				if width, height, ok := timingSize(descriptor); ok {
					monitor.PreferredMode = fmt.Sprintf("%dx%d", width, height)
				}
			}
			continue
		}
		switch descriptor[3] {
		case descriptorName:
			monitor.Model = descriptorText(descriptor)
		case descriptorSerial:
			monitor.Serial = descriptorText(descriptor)
		}
	}

	if monitor.Model == "" {
		// No name given, so the numeric product code, which is at least
		// something to search for.
		monitor.Model = fmt.Sprintf("%04X",
			uint16(edid[edidProductOffset])|uint16(edid[edidProductOffset+1])<<8)
	}
	if monitor.Serial == "" {
		if serial := uint32(edid[edidSerialOffset]) |
			uint32(edid[edidSerialOffset+1])<<8 |
			uint32(edid[edidSerialOffset+2])<<16 |
			uint32(edid[edidSerialOffset+3])<<24; serial != 0 {
			monitor.Serial = fmt.Sprintf("%d", serial)
		}
	}
	return monitor, true
}

// hasEDIDHeader checks the eight bytes every EDID begins with. Without it a
// connector that returns a short read, or a file of zeroes, decodes into a
// monitor made in 1990 with a zero-by-zero panel.
func hasEDIDHeader(edid []byte) bool {
	header := []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}
	for index, want := range header {
		if edid[index] != want {
			return false
		}
	}
	return true
}

// manufacturerCode unpacks the three five-bit letters the maker is identified
// by, which are packed into two bytes with A as 1.
func manufacturerCode(first, second byte) string {
	packed := uint16(first)<<8 | uint16(second)
	letters := [3]byte{
		byte((packed>>10)&0x1f) + 'A' - 1,
		byte((packed>>5)&0x1f) + 'A' - 1,
		byte(packed&0x1f) + 'A' - 1,
	}
	for _, letter := range letters {
		if letter < 'A' || letter > 'Z' {
			return ""
		}
	}
	return string(letters[:])
}

// descriptorText reads the thirteen bytes of a text descriptor, which are
// padded with a newline and then spaces.
func descriptorText(descriptor []byte) string {
	text := string(descriptor[5:])
	if cut := strings.IndexByte(text, '\n'); cut >= 0 {
		text = text[:cut]
	}
	return strings.TrimSpace(text)
}

// timingSize reads the width and height out of a detailed timing descriptor,
// where each is twelve bits split across three bytes.
func timingSize(descriptor []byte) (width, height int, ok bool) {
	width = int(descriptor[2]) | int(descriptor[4]&0xf0)<<4
	height = int(descriptor[5]) | int(descriptor[7]&0xf0)<<4
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

// parseMode reads "1920x1080".
func parseMode(mode string) (width, height int, ok bool) {
	first, second, found := strings.Cut(mode, "x")
	if !found {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(first, "%d", &width); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(second, "%d", &height); err != nil {
		return 0, 0, false
	}
	return width, height, width > 0 && height > 0
}
