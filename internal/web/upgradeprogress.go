package web

import "time"

// upgradeProgress is what an upgrade is doing, for the page to show.
//
// An upgrade takes minutes and takes the screen away in the middle of them, so
// "it is running, since when, and what it is doing" is the least a page can
// say. Without it the interface offered the button again on every reload,
// which invites pressing it twice -- and twice is the one thing that must not
// happen.
type upgradeProgress struct {
	// Running is whether one is going on now.
	Running bool `json:"running"`
	// Version being installed.
	Version string `json:"version,omitempty"`
	// Stage in words, for showing: "Fetching …", "Replacing the container".
	Stage string `json:"stage,omitempty"`
	// StartedAt is when the button was pressed.
	StartedAt time.Time `json:"startedAt,omitempty"`
	// Trouble is why the last attempt failed, and stays after Running goes
	// false so that somebody who reloads the page finds out what happened
	// rather than an interface that looks like nothing was ever tried.
	Trouble string `json:"trouble,omitempty"`
}

// claimUpgrade takes the one upgrade this device may run at a time. It reports
// whether it got it.
func (self *Server) claimUpgrade(version string) bool {
	self.upgradeMutex.Lock()
	defer self.upgradeMutex.Unlock()

	if self.upgradeProgress.Running {
		return false
	}
	self.upgradeProgress = upgradeProgress{
		Running:   true,
		Version:   version,
		Stage:     "Starting",
		StartedAt: time.Now(),
	}
	return true
}

// upgradeSaying records what the upgrade is doing now.
func (self *Server) upgradeSaying(stage string) {
	self.upgradeMutex.Lock()
	defer self.upgradeMutex.Unlock()
	if self.upgradeProgress.Running {
		self.upgradeProgress.Stage = stage
	}
}

// upgradeFailed gives the claim back and records why, so that the next person
// to look at the page is told rather than left to guess.
func (self *Server) upgradeFailed(reason string) {
	self.upgradeMutex.Lock()
	defer self.upgradeMutex.Unlock()
	self.upgradeProgress.Running = false
	self.upgradeProgress.Stage = ""
	self.upgradeProgress.Trouble = reason
}

// upgradeNow is what to show.
func (self *Server) upgradeNow() upgradeProgress {
	self.upgradeMutex.Lock()
	defer self.upgradeMutex.Unlock()
	return self.upgradeProgress
}
