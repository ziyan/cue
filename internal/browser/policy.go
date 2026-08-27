package browser

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ziyan/cue/internal/util/atomicfile"
)

// What the browser is not allowed to put on the wall.
//
// A kiosk browser signs into dashboards on its own -- that is what the login
// rules are for -- and a browser that has just been given a password offers to
// remember it. On a desk that is a helpful bubble somebody dismisses. On a wall
// it is a panel over the dashboard, in front of whoever the screen was put
// there for, until somebody walks over with a mouse. The same is true of every
// other thing a browser volunteers: offering to translate the page, asking
// whether a site may send notifications, or telling somebody their password
// was found in a breach.
//
// Command line flags cannot switch most of these off. Enterprise policy can,
// and this is what enterprise policy is for: a browser somebody else is
// responsible for. The file is written where this Chromium looks for it,
// which was read out of the binary rather than remembered:
//
//	$ strings chromium | grep /etc/chromium
//	/etc/chromium/policies
//
// It is written at every start rather than baked into the image, so that it
// travels with the daemon that depends on it and cannot drift away from the
// version that expects it.

// policyDirectory is where Chromium reads policy meant for it by whoever
// administers the machine.
const policyDirectory = "/etc/chromium/policies/managed"

// kioskPolicy is what a screen on a wall should never be asked.
//
// Every name here was checked against the browser in this image; a policy that
// build does not know is ignored in silence, which is the worst way for this
// to fail.
func kioskPolicy() map[string]interface{} {
	return map[string]interface{}{
		// The one that started this. Signing a dashboard in is what the login
		// rules do, and the offer to remember the password lands on the wall.
		"PasswordManagerEnabled": false,

		// And the rest of the same family: filling in forms, and being told a
		// password has appeared in somebody else's breach.
		"AutofillAddressEnabled":       false,
		"AutofillCreditCardEnabled":    false,
		"PasswordLeakDetectionEnabled": false,

		// Things pages ask for. A screen nobody is standing at cannot answer a
		// permission prompt, so the answer is no rather than a dialogue that
		// waits for ever. 2 is "block" in Chromium's numbering.
		"DefaultNotificationsSetting": 2,
		"DefaultGeolocationSetting":   2,
		"DefaultPopupsSetting":        2,

		// The bar across the top offering to translate a dashboard that is in
		// the language somebody chose.
		"TranslateEnabled": false,

		// Chromium's own suggestions, sign-in and telemetry. None of them has
		// anywhere to go on a screen with no keyboard.
		"PromotionalTabsEnabled":  false,
		"BrowserSignin":           0,
		"SyncDisabled":            true,
		"MetricsReportingEnabled": false,
	}
}

// writePolicy puts the policy where the browser will read it.
//
// A failure is reported and not fatal. A screen showing a dashboard with an
// occasional bubble over it is worth more than a screen that refused to start.
func (self *Browser) writePolicy() {
	if err := os.MkdirAll(policyDirectory, 0o755); err != nil {
		log.Warningf("cannot create %s, so the browser may offer to save passwords "+
			"on the screen: %s", policyDirectory, err)
		return
	}

	content, err := json.MarshalIndent(kioskPolicy(), "", "  ")
	if err != nil {
		log.Warningf("cannot write the browser policy: %s", err)
		return
	}
	content = append(content, '\n')

	filename := filepath.Join(policyDirectory, "cue.json")
	if err := atomicfile.Write(filename, content, 0o644); err != nil {
		log.Warningf("cannot write %s, so the browser may offer to save passwords "+
			"on the screen: %s", filename, err)
		return
	}
	log.Debugf("wrote %s", filename)
}
