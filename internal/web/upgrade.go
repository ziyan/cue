package web

import (
	"context"
	"net/http"
	"time"

	"github.com/ziyan/cue/internal/upgrade"
)

// upgradeAnswer is what the Upgrade page reads.
type upgradeAnswer struct {
	upgrade.State

	// CanApply says whether the button does anything on this device.
	CanApply bool `json:"canApply"`
	// WhyNot is what would have to change for it to, in words for a person.
	// Empty when it can.
	WhyNot string `json:"whyNot,omitempty"`
	// Image is where a newer one would come from, so that somebody upgrading
	// by hand does not have to guess.
	Image string `json:"image,omitempty"`
}

// upgradeState answers what is known about newer releases.
//
// A GET does not ask GitHub unless nothing is known yet or what is known is a
// day old. Opening this page repeatedly must not spend a public API's rate
// limit, and the answer changes every few weeks at most.
func (self *Server) upgradeState(response http.ResponseWriter, request *http.Request) {
	if self.upgrades == nil {
		writeError(response, http.StatusServiceUnavailable, "this daemon is not checking for releases")
		return
	}

	state := self.upgrades.State()
	if state.CheckedAt.IsZero() || time.Since(state.CheckedAt) > time.Hour {
		ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
		defer cancel()
		// The error is already in the state, as Trouble. A check that failed
		// is something to show, not something to refuse the page over.
		state, _ = self.upgrades.Check(ctx)
	}

	canApply, whyNot := upgrade.CanApply(self.store.Current().Upgrade.AllowApply)
	writeJSON(response, http.StatusOK, upgradeAnswer{
		State:    state,
		CanApply: canApply,
		WhyNot:   whyNot,
		Image:    upgrade.ImageFor(state.Latest),
	})
}
