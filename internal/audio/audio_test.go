package audio

import (
	"testing"

	"github.com/ziyan/cue/internal/config"
)

func TestTheCardListingIsReadAsItHasLookedForTwentyYears(t *testing.T) {
	// /proc/asound/cards is two lines per card and this is exactly its shape.
	cases := map[string]struct {
		index      int
		identifier string
	}{
		" 0 [PCH            ]: HDA-Intel - HDA Intel PCH": {0, "PCH"},
		" 1 [HDMI           ]: HDA-Intel - HDA ATI HDMI":  {1, "HDMI"},
		"12 [USB            ]: USB-Audio - Plantronics":   {12, "USB"},
	}
	for line, want := range cases {
		index, identifier, ok := parseCardLine(line)
		if !ok {
			t.Errorf("%q was not recognised as a card", line)
			continue
		}
		if index != want.index || identifier != want.identifier {
			t.Errorf("%q gave %d/%q, want %d/%q", line, index, identifier, want.index, want.identifier)
		}
	}
}

func TestSomethingThatIsNotACardLineIsNotReadAsOne(t *testing.T) {
	for _, line := range []string{
		"                      HDA Intel PCH at 0xf7314000 irq 145",
		"--- no soundcards ---",
		"",
	} {
		if _, _, ok := parseCardLine(line); ok {
			t.Errorf("%q was read as a card", line)
		}
	}
}

func TestTheDeviceNameIsOneAProgramCanOpen(t *testing.T) {
	// plughw rather than hw, because plughw converts sample rates and
	// formats; a browser handed a raw hw device that does not accept its
	// format simply plays nothing.
	device := Device{Index: 1, Identifier: "HDMI"}
	if name := device.ALSAName(); name != "plughw:HDMI" {
		t.Errorf("the device is %q, want plughw:HDMI", name)
	}

	// A card with no identifier still has a number.
	unnamed := Device{Index: 2}
	if name := unnamed.ALSAName(); name != "plughw:2" {
		t.Errorf("the unnamed device is %q, want plughw:2", name)
	}
}

func TestSoundOffMeansTheBrowserIsMuted(t *testing.T) {
	arguments := OutputArguments(&config.Audio{Enabled: false})
	if len(arguments) != 1 || arguments[0] != "--mute-audio" {
		t.Errorf("sound switched off gave %v", arguments)
	}
}

func TestNoChosenCardMeansNoFlagAtAll(t *testing.T) {
	// On a machine with one sound card, letting ALSA choose is right.
	if arguments := OutputArguments(&config.Audio{Enabled: true}); len(arguments) != 0 {
		t.Errorf("no card was chosen but %v was passed", arguments)
	}
}

func TestTheDescriptionSaysWhatIsWrongWhenThereAreNoCards(t *testing.T) {
	// A container without /dev/snd is silent and says nothing about why.
	text := Describe(&config.Audio{Enabled: true}, nil)
	if text == "" {
		t.Fatal("no description at all")
	}
	if !contains(text, "/dev/snd") {
		t.Errorf("the description does not suggest what is wrong: %q", text)
	}
}

func contains(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
