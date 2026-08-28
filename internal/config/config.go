// Package config is cue.yaml: the typed shape of it, a validator that reports
// every problem at once with the path of each, and a Store that hands out
// immutable snapshots and rewrites the file atomically.
//
// It is the source of truth for everything an operator can set. Nothing else
// in this project keeps settings of its own, and no command line flag
// configures anything that is not also here.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ziyan/cue/internal/util/atomicfile"
	"github.com/ziyan/cue/internal/util/security"
)

// Load reads a configuration file, fills in every default the file does not
// mention, generates any identifier that is still missing, and validates the
// result.
func Load(filename string) (*Configuration, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", filename, err)
	}
	return Parse(content)
}

// Parse is Load without the file, which is what the tests and the API use.
func Parse(content []byte) (*Configuration, error) {
	configuration := Default()

	// A field the daemon does not know is reported rather than silently
	// ignored: a typo in a key is otherwise indistinguishable from a setting
	// that does not work. It is not fatal, though, and that distinction is the
	// whole of this function.
	//
	// A setting can be removed — browser.debuggingPort was, because having it
	// at all turned out to be the bug — and every device already in service
	// has it written into its file. A daemon that refuses to start over a
	// setting that no longer exists turns an upgrade into a screen that has
	// gone black and a machine nobody can reach. So an unknown field is
	// named in the log and skipped, and anything else in the file is still
	// refused.
	var ignored []string
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	err := decoder.Decode(configuration)
	if err != nil && !errors.Is(err, io.EOF) {
		unknown, other := separateUnknownFields(err)
		if len(other) > 0 {
			return nil, fmt.Errorf("config: %s", strings.Join(other, "; "))
		}
		for _, field := range unknown {
			log.Warningf("%s — it is not a setting this version has, and is ignored; "+
				"it will be removed from the file the next time the file is written", field)
		}
		ignored = unknown

		// Decoded again without the strictness, because the first pass stops
		// at the error and leaves the rest of the file unread.
		configuration = Default()
		relaxed := yaml.NewDecoder(bytes.NewReader(content))
		if err := relaxed.Decode(configuration); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config: %w", err)
		}
	}

	configuration.IgnoredSettings = ignored
	configuration.Normalize()
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return configuration, nil
}

// separateUnknownFields splits a yaml.TypeError into the problems that are
// only a name this version does not have, and everything else.
//
// go-yaml reports every problem it found in one error whose Error() is a
// heading followed by indented lines, and printing it with %w produces the
// heading and nothing else — which is how a device came to log "yaml:
// unmarshal errors:" ten times and say nothing whatever about what was wrong
// with its configuration.
func separateUnknownFields(err error) (unknown, other []string) {
	var typeError *yaml.TypeError
	if !errors.As(err, &typeError) {
		return nil, []string{err.Error()}
	}
	for _, message := range typeError.Errors {
		if strings.Contains(message, "not found in type") {
			unknown = append(unknown, message)
			continue
		}
		other = append(other, message)
	}
	return unknown, other
}

// Normalize fills in the values that are generated rather than configured,
// and tidies the ones an operator is likely to write loosely. It is safe to
// call more than once and does not change a value that is already set:
// identifiers in particular are generated once and never regenerated, because
// the browser's tab bookkeeping refers to them.
func (self *Configuration) Normalize() {
	if self.Device.Identifier == "" {
		self.Device.Identifier = security.NewIdentifier()
	}
	self.Device.Name = strings.TrimSpace(self.Device.Name)
	self.Log.Level = strings.ToUpper(strings.TrimSpace(self.Log.Level))

	// "video:" is what "media:" used to be called. A file written by an older
	// version is read and moved across rather than being told that a setting
	// it has is not one this version knows -- which would drop the item, and
	// dropping somebody's content silently is the failure worth going out of
	// the way to avoid.
	for index := range self.Playlist.Items {
		item := &self.Playlist.Items[index]
		if item.Video != nil && item.Media == nil {
			item.Media = item.Video
		}
		item.Video = nil
		if item.Media != nil && item.Media.Kind == "" {
			// Everything written under the old name was a video; there were no
			// pictures then.
			item.Media.Kind = "video"
		}
	}

	// The upload limit, and the setting it used to be called.
	//
	// The default is filled in here rather than among the other defaults
	// because those are applied before the file is read: a default sitting in
	// the field would mean the older setting never looked unset, and somebody
	// upgrading would silently get four gigabytes instead of the number they
	// chose.
	if self.Playlist.MaximumUploadSize == 0 {
		self.Playlist.MaximumUploadSize = self.Playlist.MaximumVideoSize
	}
	self.Playlist.MaximumVideoSize = 0
	if self.Playlist.MaximumUploadSize <= 0 {
		// Four gigabytes: larger than any promotional loop, and small enough
		// that one upload cannot fill a modest disk by itself.
		self.Playlist.MaximumUploadSize = 4 << 30
	}

	// A file written before this setting existed has it empty, and an empty
	// value must not read as "never offer to be set up".
	if self.Network.Onboarding == "" {
		self.Network.Onboarding = OnboardingAuto
	}
	if self.Network.LostAfter <= 0 {
		self.Network.LostAfter = Duration(10 * time.Minute)
	}

	if self.Display.ModeName == "" {
		self.Display.ModeName = "cue"
	}
	for index := range self.Display.Outputs {
		output := &self.Display.Outputs[index]
		if output.Mode == "" {
			output.Mode = ModePreferred
		}
		if output.Rotate == "" {
			output.Rotate = "normal"
		}
	}

	for index := range self.Playlist.Items {
		item := &self.Playlist.Items[index]
		if item.Identifier == "" {
			item.Identifier = security.NewIdentifier()
		}
		item.URL = strings.TrimSpace(item.URL)
	}

	if !self.Web.SessionSecret.IsSet() {
		self.Web.SessionSecret = Secret(security.NewToken())
	}
}

// Marshal renders the configuration as the file's contents, with a header
// explaining what the file is. A machine write cannot preserve comments a
// person wrote, so the header is re-emitted every time and every list entry
// has a place to put a note that does survive.
//
// Only what differs from the default is written. See prune.go for why.
func (self *Configuration) Marshal() ([]byte, error) {
	chosen, err := self.withoutDefaults()
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	buffer.WriteString(fileHeader)

	if chosen == nil {
		// A device that has been told nothing. An empty mapping is a valid
		// configuration and reads back correctly; saying so out loud is
		// kinder than a file that looks like it failed to write.
		buffer.WriteString("# Nothing here differs from the defaults. cue.yaml.example,\n" +
			"# beside this file, shows every setting and what it starts as.\n{}\n")
		return buffer.Bytes(), nil
	}

	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(chosen); err != nil {
		return nil, fmt.Errorf("config: encode: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("config: encode: %w", err)
	}
	return buffer.Bytes(), nil
}

// Save validates and then writes the configuration. It never writes a file
// that would not load, so a rejected change leaves the previous file intact.
func (self *Configuration) Save(filename string) error {
	self.Normalize()
	if err := self.Validate(); err != nil {
		return err
	}
	content, err := self.Marshal()
	if err != nil {
		return err
	}
	// 0600: this file holds the passwords the kiosk logs into things with,
	// and on a device the only account that needs it is the daemon's.
	return atomicfile.Write(filename, content, 0o600)
}

// Clone returns a deep copy, so that a caller holding a snapshot cannot be
// surprised by a reload happening underneath it.
func (self *Configuration) Clone() *Configuration {
	content, err := yaml.Marshal(self)
	if err != nil {
		// Marshalling a struct of scalars and slices cannot fail; if it ever
		// does, carrying on with a shallow copy would be worse.
		panic(fmt.Sprintf("config: cannot clone: %s", err))
	}
	clone := &Configuration{}
	if err := yaml.Unmarshal(content, clone); err != nil {
		panic(fmt.Sprintf("config: cannot clone: %s", err))
	}
	return clone
}

// RestoreSecrets copies every secret that arrived as the redacted placeholder
// back from the previous configuration. The web interface is never shown a
// password, so when it posts a form back it sends the placeholder; without
// this, opening the settings page and saving it would erase every credential
// on the device.
func RestoreSecrets(updated, previous *Configuration) {
	if updated.VNC.Password.IsRedacted() {
		updated.VNC.Password = previous.VNC.Password
	}
	// The credential the service issued. The interface is never shown it, so
	// a form posted back carries the placeholder, and taking that literally
	// would unlink a device every time somebody changed its name.
	if updated.Service.Secret.IsRedacted() {
		updated.Service.Secret = previous.Service.Secret
	}
	// Playlist items are matched by identifier rather than by position,
	// because the interface can reorder them in the same request that saves
	// them.
	previousLogins := map[string]*Login{}
	for index := range previous.Playlist.Items {
		item := &previous.Playlist.Items[index]
		if item.Login != nil {
			previousLogins[item.Identifier] = item.Login
		}
	}
	for index := range updated.Playlist.Items {
		item := &updated.Playlist.Items[index]
		if item.Login == nil || !item.Login.Password.IsRedacted() {
			continue
		}
		if previousLogin, found := previousLogins[item.Identifier]; found {
			item.Login.Password = previousLogin.Password
		}
	}

	// These two are never sent to the interface at all, so an update that did
	// not come from a reload would otherwise clear them and log everybody out.
	if updated.Web.SessionSecret == "" {
		updated.Web.SessionSecret = previous.Web.SessionSecret
	}
	if updated.Web.PasswordHash == "" {
		updated.Web.PasswordHash = previous.Web.PasswordHash
	}
}

const fileHeader = `# cue.yaml — the configuration for one display.
#
# This file is the single source of truth for everything this device does. It
# is written both by hand and by the web interface; a machine write reformats
# it and does not keep comments, so put anything you want to remember in the
# "title" of a playlist item or the "location" of the device.
#
# An edit is noticed and applied within a second or so; there is nothing to
# send and nothing to restart. A file that does not parse is refused and the
# screen carries on with what it had.
#
# Only what somebody chose is written here. A setting that matches the default
# is left out, which is why this file is short. cue.yaml.example, beside it,
# lists every setting and what it is when nobody says otherwise.
#
# Documentation: docs/reference/configuration.md
#
# This file contains passwords. It is written with mode 0600 and should not be
# copied anywhere it would be readable by anybody else.

`
