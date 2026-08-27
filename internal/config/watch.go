package config

import (
	"crypto/sha256"
	"os"
)

// Noticing that somebody has edited the file.
//
// The daemon used to need a signal for this -- edit the file over SSH, then
// send it SIGHUP -- which is one more thing to know and easy to forget, and
// the symptom of forgetting is a change that appears to have been ignored.
//
// The awkward part is that the daemon writes this file too, whenever anything
// is changed through the web interface. Without care, its own write looks
// exactly like somebody editing it, and applying a change re-applies it, which
// can mean the browser restarting for no reason. So the store remembers what
// the file looked like the last time it read or wrote it, and a change that
// matches is not a change.

// digestOf is what the file looked like. A file that cannot be read has no
// digest, which is treated as "different" so the next attempt reloads.
func digestOf(filename string) ([32]byte, bool) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256(content), true
}

// remember records the file as it stands now, so that a later change can be
// told from this one.
func (self *Store) remember() {
	digest, ok := digestOf(self.filename)

	self.digestMutex.Lock()
	self.digest, self.digestKnown = digest, ok
	self.digestMutex.Unlock()
}

// changedOnDisk reports whether the file differs from what this store last
// read or wrote.
func (self *Store) changedOnDisk() bool {
	digest, ok := digestOf(self.filename)

	self.digestMutex.Lock()
	previous, known := self.digest, self.digestKnown
	self.digestMutex.Unlock()

	if !ok || !known {
		return ok
	}
	return digest != previous
}

// ReloadIfChanged re-reads the file, but only when it differs from what this
// store last read or wrote. It reports whether it reloaded.
//
// This is what the file watcher calls. A file that no longer parses is
// refused, the configuration in force is kept, and the digest is remembered
// anyway -- otherwise every further event on a broken file would report the
// same error again, filling the log while somebody is still editing.
func (self *Store) ReloadIfChanged() (bool, error) {
	if !self.changedOnDisk() {
		return false, nil
	}
	if err := self.Reload(); err != nil {
		self.remember()
		return false, err
	}
	return true, nil
}
