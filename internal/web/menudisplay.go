package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
)

// The screen's own picture, set from the screen.
//
// Deliberately two questions and no more: how big, and which way up. Those are
// the two somebody standing in front of a wall display can answer by looking at
// it -- the picture is the wrong size, or it is on its side because the screen
// was mounted in portrait.
//
// Everything else the display section can express -- the X server to use, the
// virtual terminal, a modeline, extra arguments -- is either not a choice
// anybody makes at a screen or not one they should make without reading
// something first. Those stay in the web interface, behind a disclosure, where
// there is room to explain them.

// menuDisplay is what the outputs are and what each could be.
func (self *Server) menuDisplay(response http.ResponseWriter, request *http.Request) {
	server := self.device.XServer()
	if server == nil {
		writeError(response, http.StatusServiceUnavailable, "there is no screen to ask")
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	connection, err := display.Open(ctx, self.store.Current().Display.Number, server.Cookie())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, err.Error())
		return
	}
	defer connection.Close()

	outputs, err := connection.Outputs()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	configured := map[string]config.Output{}
	for _, one := range self.store.Current().Display.Outputs {
		configured[one.Name] = one
	}

	listed := make([]map[string]interface{}, 0, len(outputs))
	for _, one := range outputs {
		if !one.Connected {
			// A connector with nothing in it is not a choice anybody wants to
			// be offered.
			continue
		}
		entry := map[string]interface{}{
			"name":     one.Name,
			"mode":     one.CurrentMode,
			"best":     one.PreferredMode,
			"rotation": one.Rotation,
			"modes":    everydayModes(one.Modes, one.PreferredMode),
		}
		if settings, found := configured[one.Name]; found {
			entry["chosen"] = settings.Mode
			entry["chosenRotation"] = settings.Rotate
		}
		listed = append(listed, entry)
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"outputs": listed})
}

// everydayModes is the list to offer, which is not the list the monitor gave.
//
// A monitor reports everything it will accept, which is commonly thirty or
// forty entries: the same handful of sizes at nine refresh rates each, plus
// sizes nobody has wanted since cathode ray tubes. Offering all of that to
// somebody standing at a screen with a mouse is offering them a haystack.
//
// So: one entry per size, keeping the highest rate for each, largest first,
// and no more than eight. Anybody who needs 1920x1080 at exactly 50 Hz has a
// reason and a keyboard, and the web interface will let them say so.
func everydayModes(modes []string, preferred string) []string {
	seen := map[string]bool{}
	kept := make([]string, 0, 8)

	// The monitor's own preference first, whatever else happens: it is nearly
	// always the right answer and it should be the easy one to pick.
	if preferred != "" {
		kept = append(kept, preferred)
		seen[sizeOf(preferred)] = true
	}

	for _, mode := range modes {
		size := sizeOf(mode)
		if size == "" || seen[size] {
			continue
		}
		seen[size] = true
		kept = append(kept, mode)
		if len(kept) >= 8 {
			break
		}
	}
	return kept
}

// sizeOf is the "1920x1080" of "1920x1080@60".
func sizeOf(mode string) string {
	for index := 0; index < len(mode); index++ {
		if mode[index] == '@' {
			return mode[:index]
		}
	}
	return mode
}

// menuSetDisplay writes what somebody chose at the screen.
func (self *Server) menuSetDisplay(response http.ResponseWriter, request *http.Request) {
	var wanted struct {
		Output   string `json:"output"`
		Mode     string `json:"mode"`
		Rotation string `json:"rotation"`
	}
	if err := json.NewDecoder(request.Body).Decode(&wanted); err != nil {
		writeError(response, http.StatusBadRequest, "that is not a screen to set up")
		return
	}
	if wanted.Output == "" {
		writeError(response, http.StatusBadRequest, "which screen?")
		return
	}
	switch wanted.Rotation {
	case "", "normal", "left", "right", "inverted":
	default:
		writeError(response, http.StatusBadRequest, "that is not a way up")
		return
	}

	err := self.store.Update(func(configuration *config.Configuration) error {
		settings := config.Output{
			Name:   wanted.Output,
			Mode:   wanted.Mode,
			Rotate: wanted.Rotation,
		}
		if settings.Mode == "" {
			settings.Mode = "preferred"
		}
		if settings.Rotate == "" {
			settings.Rotate = "normal"
		}
		for index := range configuration.Display.Outputs {
			if configuration.Display.Outputs[index].Name == settings.Name {
				configuration.Display.Outputs[index] = settings
				return nil
			}
		}
		configuration.Display.Outputs = append(configuration.Display.Outputs, settings)
		return nil
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	log.Noticef("somebody at the screen set %s to %s %s",
		wanted.Output, wanted.Mode, wanted.Rotation)
	writeJSON(response, http.StatusOK, map[string]interface{}{"output": wanted.Output})
}
