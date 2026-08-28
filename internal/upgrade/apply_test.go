package upgrade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBothThingsAreNeededToApply(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-here.sock")
	present := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(present, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, one := range []struct {
		allow    bool
		socket   string
		can      bool
		mentions string
	}{
		{true, present, true, ""},
		{false, present, false, "allowApply"},
		{true, missing, false, "not in this container"},
		{false, missing, false, "Both are needed"},
	} {
		previous := SocketPath
		SocketPath = one.socket
		can, why := CanApply(one.allow)
		SocketPath = previous

		if can != one.can {
			t.Errorf("allow=%v socket=%v: can apply = %v, want %v",
				one.allow, one.socket == present, can, one.can)
		}
		if one.can && why != "" {
			t.Errorf("allow=%v: said it could and complained anyway: %s", one.allow, why)
		}
		if !one.can && !strings.Contains(why, one.mentions) {
			t.Errorf("allow=%v socket=%v: %q does not mention %q",
				one.allow, one.socket == present, why, one.mentions)
		}
	}
}
