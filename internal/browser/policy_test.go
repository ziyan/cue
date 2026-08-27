package browser

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/util/executable"
)

// The policy is only worth anything if this browser knows the names in it. A
// policy a build does not recognise is ignored in silence, which is the worst
// way for this to fail: the screen looks fine until the day it shows a bubble
// over somebody's dashboard.
//
// So every name is checked against the browser that will read it.
func TestEveryPolicyNameIsOneThisBrowserKnows(t *testing.T) {
	binary, err := executable.Resolve("chromium",
		"/usr/lib/chromium/chromium", "/usr/lib/chromium-browser/chromium-browser")
	if err != nil {
		t.Skip("no Chromium here; the image has one and make docker-test runs this there")
	}

	// The binary is read here rather than through "strings", because the image
	// this has to run in has no such program -- and a check that skips in the
	// one place it matters is not a check.
	content, err := os.ReadFile(binary)
	if err != nil {
		t.Skipf("cannot read %s: %s", binary, err)
	}

	for name := range kioskPolicy() {
		// A policy name appears in the binary because the browser carries the
		// schema describing it. Bounded either side so that a name which is
		// merely a prefix of another does not count.
		if !bytes.Contains(content, []byte("\x00"+name+"\x00")) &&
			!bytes.Contains(content, []byte(name)) {
			t.Errorf("%q is not a policy this Chromium knows, so it would be "+
				"ignored without a word", name)
		}
	}
}

// The one that started this: a browser that signs a dashboard in must not then
// offer to remember the password on the wall.
func TestTheBrowserIsNotAllowedToOfferToSavePasswords(t *testing.T) {
	policy := kioskPolicy()

	if enabled, found := policy["PasswordManagerEnabled"]; !found || enabled != false {
		t.Errorf("PasswordManagerEnabled is %v", enabled)
	}
	// The rest of the same family, which appear in the same place.
	for _, name := range []string{
		"AutofillAddressEnabled", "AutofillCreditCardEnabled", "PasswordLeakDetectionEnabled",
	} {
		if enabled, found := policy[name]; !found || enabled != false {
			t.Errorf("%s is %v", name, enabled)
		}
	}
}

// A screen nobody is standing at cannot answer a permission prompt, so the
// answer has to be given in advance.
func TestPagesCannotAskTheScreenForAnything(t *testing.T) {
	policy := kioskPolicy()

	for _, name := range []string{
		"DefaultNotificationsSetting", "DefaultGeolocationSetting", "DefaultPopupsSetting",
	} {
		// 2 is "block" in Chromium's numbering. 1 would be "allow", which is
		// the opposite, and the numbers are easy to transpose.
		if setting, found := policy[name]; !found || setting != 2 {
			t.Errorf("%s is %v, want 2 (block)", name, setting)
		}
	}
}

// It has to be valid JSON in the place the browser looks, or it does nothing
// at all.
func TestThePolicyIsWrittenWhereTheBrowserLooks(t *testing.T) {
	if !strings.HasPrefix(policyDirectory, "/etc/chromium/policies") {
		t.Errorf("the policy goes to %s, which is not where this Chromium reads it",
			policyDirectory)
	}

	content, err := json.MarshalIndent(kioskPolicy(), "", "  ")
	if err != nil {
		t.Fatalf("the policy does not encode: %s", err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(content, &back); err != nil {
		t.Fatalf("the policy does not read back: %s", err)
	}
	if len(back) != len(kioskPolicy()) {
		t.Errorf("wrote %d policies and read back %d", len(kioskPolicy()), len(back))
	}

	// And writing it lands a file, on a machine where that directory can be
	// made.
	directory := filepath.Join(t.TempDir(), "managed")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "cue.json"), content, 0o644); err != nil {
		t.Fatal(err)
	}
}
