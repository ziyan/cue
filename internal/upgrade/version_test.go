package upgrade

import "testing"

func TestWhatCountsAsNewer(t *testing.T) {
	for _, one := range []struct {
		current   string
		candidate string
		newer     bool
		why       string
	}{
		{"0.1.0", "0.2.0", true, "a later minor"},
		{"0.1.0", "0.1.1", true, "a later patch"},
		{"0.9.0", "1.0.0", true, "a later major"},
		{"0.2.0", "0.2.0", false, "the same version"},
		{"0.2.0", "0.1.9", false, "an earlier one is not an upgrade"},
		{"1.0.0", "0.9.9", false, "an earlier major is not an upgrade"},

		// Compared as numbers, not as text. As text "0.10.0" sorts before
		// "0.9.0", so a device on 0.9.0 would be told nothing and a device on
		// 0.10.0 would be offered 0.9.0 as an upgrade.
		{"0.9.0", "0.10.0", true, "ten is after nine"},
		{"0.10.0", "0.9.0", false, "nine is not after ten"},
		{"9.9.9", "10.0.0", true, "ten is after nine at the major too"},

		// The tag carries a v and the version does not.
		{"0.1.0", "v0.2.0", true, "a leading v is not part of the number"},
		{"v0.1.0", "0.2.0", true, "on either side"},

		// A development build is not on the ladder.
		{"0.0.0-dev", "9.9.9", false, "a development build is never out of date"},
		{"", "1.0.0", false, "and neither is an empty one"},
		{"1.0.0-rc1", "1.0.0", false, "nor anything else this project does not publish"},

		// Nonsense from the other end must not be believed.
		{"0.1.0", "not a version", false, "an answer that is not a version"},
		{"0.1.0", "", false, "an empty answer"},
		{"0.1.0", "1.2", false, "two numbers is not a version"},
		{"0.1.0", "1.2.3.4", false, "nor is four"},
		{"0.1.0", "-1.0.0", false, "nor a negative one"},
	} {
		if got := Newer(one.current, one.candidate); got != one.newer {
			t.Errorf("Newer(%q, %q) = %v, want %v (%s)",
				one.current, one.candidate, got, one.newer, one.why)
		}
	}
}

func TestADevelopmentBuildIsRecognised(t *testing.T) {
	for _, version := range []string{"0.0.0-dev", "", "1.0.0-rc1", "main", "0.1"} {
		if !isDevelopment(version) {
			t.Errorf("%q is not recognised as a build outside the release workflow", version)
		}
	}
	for _, version := range []string{"0.1.0", "v0.1.0", "10.20.30"} {
		if isDevelopment(version) {
			t.Errorf("%q is a release and was treated as a development build", version)
		}
	}
}
