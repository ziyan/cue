package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that reads and writes as a human string in
// YAML, for example "30s", "5m" or "12h". Go's own duration syntax has no day
// unit, so "d" is added here because a display's schedule talks in days.
type Duration time.Duration

// Duration returns the value as a time.Duration.
func (self Duration) Duration() time.Duration {
	return time.Duration(self)
}

// String formats the duration, preferring the largest whole unit so that a
// day reads back as "1d" rather than time.Duration's "24h0m0s".
func (self Duration) String() string {
	duration := time.Duration(self)
	if duration == 0 {
		return "0s"
	}
	switch {
	case duration%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", duration/(24*time.Hour))
	case duration%time.Hour == 0:
		return fmt.Sprintf("%dh", duration/time.Hour)
	case duration%time.Minute == 0:
		return fmt.Sprintf("%dm", duration/time.Minute)
	case duration%time.Second == 0:
		return fmt.Sprintf("%ds", duration/time.Second)
	}
	return duration.String()
}

// MarshalYAML implements yaml.Marshaler.
func (self Duration) MarshalYAML() (interface{}, error) {
	return self.String(), nil
}

// MarshalJSON implements json.Marshaler, so the API reports "30s" rather than
// a nanosecond count nobody can read.
func (self Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(self.String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (self *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		var seconds float64
		if err := json.Unmarshal(data, &seconds); err != nil {
			return fmt.Errorf("config: %s is not a duration", data)
		}
		*self = Duration(time.Duration(seconds * float64(time.Second)))
		return nil
	}
	parsed, err := ParseDuration(value)
	if err != nil {
		return err
	}
	*self = parsed
	return nil
}

// UnmarshalYAML implements yaml.Unmarshaler. Both "30s" and a bare number of
// seconds are accepted, the latter because it is a natural thing to write.
func (self *Duration) UnmarshalYAML(node *yaml.Node) error {
	var value string
	if err := node.Decode(&value); err != nil {
		var seconds float64
		if err := node.Decode(&seconds); err != nil {
			return fmt.Errorf("config: %q is not a duration", node.Value)
		}
		*self = Duration(time.Duration(seconds * float64(time.Second)))
		return nil
	}
	parsed, err := ParseDuration(value)
	if err != nil {
		return err
	}
	*self = parsed
	return nil
}

// ParseDuration parses a duration, accepting the "d" day unit in addition to
// everything time.ParseDuration understands.
func ParseDuration(value string) (Duration, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, nil
	}
	if strings.HasSuffix(trimmed, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(trimmed, "d"), 64)
		if err == nil {
			return Duration(time.Duration(days * float64(24*time.Hour))), nil
		}
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("config: %q is not a duration, expected something like 30s, 5m, 12h or 1d", value)
	}
	return Duration(parsed), nil
}

// Secret is a string that must not leave the machine it is configured on. It
// round-trips through YAML unchanged, because the configuration file is where
// it lives, but it renders as a placeholder everywhere else: in a log line, in
// a %s, and in every JSON response the API produces.
//
// This exists because the feature that needs it — logging a kiosk browser into
// a dashboard that keeps expiring its session — necessarily stores somebody's
// working password, and this project is published.
type Secret string

// String implements fmt.Stringer with a placeholder, so that a Secret cannot
// be logged by accident.
func (self Secret) String() string {
	if self == "" {
		return ""
	}
	return redacted
}

// IsSet reports whether a value was configured, which is the only thing about
// a secret that is safe to say out loud.
func (self Secret) IsSet() bool {
	return self != ""
}

// Reveal returns the actual value. Every call site is a place a secret can
// escape, so the name is deliberately one that stands out in review.
func (self Secret) Reveal() string {
	return string(self)
}

// MarshalJSON implements json.Marshaler with the placeholder. The API reports
// whether a password is set, never what it is.
func (self Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(self.String())
}

// UnmarshalJSON implements json.Unmarshaler. The placeholder is preserved
// rather than rejected, because an interface that renders a form from the API
// and posts it back will send it verbatim for any password it was never
// shown; RestoreSecrets turns those back into the values already configured.
func (self *Secret) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("config: %s is not a string", data)
	}
	*self = Secret(value)
	return nil
}

// IsRedacted reports whether this value is the placeholder rather than a real
// secret, which is what arrives when a form is posted back unchanged.
func (self Secret) IsRedacted() bool {
	return self == redacted
}

// redacted is what a secret looks like from outside the configuration file.
const redacted = "********"
