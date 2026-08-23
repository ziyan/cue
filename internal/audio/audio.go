// Package audio finds the machine's sound devices and decides which one the
// browser plays through.
//
// There is no sound server here on purpose. The runtime of this image is the
// daemon, the X server, the browser, the VNC server and the time client, and
// adding a sixth process to route audio between programs would only be worth
// it if there were more than one program making sound. There is one. Chromium
// opens an ALSA device by name, this package works out which name, and that
// is the whole of it.
//
// What it costs: no per-application volume, and nothing else can play at the
// same time. What it saves: a sound server, its session bus, and the class of
// fault where the browser is fine and the sound server has died.
package audio

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ziyan/cue/internal/config"
)

// Device is one sound card, as the kernel reports it.
type Device struct {
	// Index is the card number ALSA knows it by.
	Index int `json:"index"`

	// Identifier is the short name, like "PCH" or "HDMI". It is what goes
	// into an ALSA device name.
	Identifier string `json:"identifier"`

	// Name is the long description, like "HDA Intel PCH at 0xf7314000".
	Name string `json:"name"`

	// Playback and Capture say what the card can do, worked out from the
	// device nodes the kernel created for it.
	Playback bool `json:"playback"`
	Capture  bool `json:"capture"`
}

// ALSAName is the name to give a program that wants to open this card, such
// as "hw:PCH" or "plughw:1".
func (self Device) ALSAName() string {
	if self.Identifier != "" {
		return "plughw:" + self.Identifier
	}
	return "plughw:" + strconv.Itoa(self.Index)
}

// Devices lists the machine's sound cards.
//
// /proc/asound/cards is a two-line-per-card listing that has looked the same
// for twenty years:
//
//	0 [PCH            ]: HDA-Intel - HDA Intel PCH
//	                     HDA Intel PCH at 0xf7314000 irq 145
func Devices() ([]Device, error) {
	file, err := os.Open("/proc/asound/cards")
	if err != nil {
		if os.IsNotExist(err) {
			// A machine with no sound hardware, or a container without
			// /dev/snd passed through. Not an error: a screen showing camera
			// feeds does not need sound.
			return nil, nil
		}
		return nil, fmt.Errorf("audio: cannot read the sound cards: %w", err)
	}
	defer func() { _ = file.Close() }()

	var devices []Device
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		index, identifier, ok := parseCardLine(line)
		if !ok {
			continue
		}

		device := Device{Index: index, Identifier: identifier}
		if scanner.Scan() {
			device.Name = strings.TrimSpace(scanner.Text())
		}
		device.Playback = deviceExists(fmt.Sprintf("/dev/snd/pcmC%dD0p", index))
		device.Capture = deviceExists(fmt.Sprintf("/dev/snd/pcmC%dD0c", index))
		devices = append(devices, device)
	}
	return devices, nil
}

// parseCardLine reads the first line of a card's entry.
func parseCardLine(line string) (int, string, bool) {
	trimmed := strings.TrimSpace(line)
	open := strings.IndexByte(trimmed, '[')
	closed := strings.IndexByte(trimmed, ']')
	if open < 0 || closed < open {
		return 0, "", false
	}
	index, err := strconv.Atoi(strings.TrimSpace(trimmed[:open]))
	if err != nil {
		return 0, "", false
	}
	return index, strings.TrimSpace(trimmed[open+1 : closed]), true
}

func deviceExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// OutputArguments are the browser flags that choose where sound comes out.
//
// An empty audio.sink lets ALSA choose, which on a machine with one sound card
// is right and on a machine with several is a coin toss — hence the setting.
func OutputArguments(settings *config.Audio) []string {
	if !settings.Enabled {
		// Chromium with no audio output at all still plays video; it just
		// cannot make a sound. A screen showing camera feeds in an open-plan
		// office wants exactly this.
		return []string{"--mute-audio"}
	}
	if settings.Sink == "" {
		return nil
	}
	return []string{"--alsa-output-device=" + settings.Sink}
}

// Describe is a one-line summary for the log at start-up, so that a device
// which turns out to be silent can be diagnosed from the log rather than by
// standing next to it.
func Describe(settings *config.Audio, devices []Device) string {
	if !settings.Enabled {
		return "sound is off"
	}
	if len(devices) == 0 {
		return "sound is on, but this machine reports no sound cards; is /dev/snd passed through?"
	}
	if settings.Sink == "" {
		return fmt.Sprintf("sound goes to whichever card ALSA picks, of %d", len(devices))
	}
	return "sound goes to " + settings.Sink
}
