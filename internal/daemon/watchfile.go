package daemon

import (
	"context"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// watchConfiguration reloads the configuration when somebody edits the file.
//
// This used to need a signal -- edit the file over SSH, then send the daemon
// SIGHUP -- which is one more thing to know, and forgetting it looks exactly
// like a change that was ignored. SIGHUP still works, for anybody who has it
// in their fingers.
//
// The directory is watched rather than the file. The file is written by
// renaming a new one over it, which is what makes a half-written
// configuration impossible, and it also means the file somebody is watching
// stops being the file that exists. Watching the directory and looking at the
// name survives that.
func (self *Daemon) watchConfiguration(ctx context.Context) {
	filename := self.store.Filename()
	directory := filepath.Dir(filename)

	descriptor, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		log.Warningf("cannot watch %s for changes, so editing it by hand will need "+
			"a SIGHUP as before: %s", filename, err)
		return
	}
	defer func() { _ = unix.Close(descriptor) }()

	// IN_MOVED_TO is the one that matters, because that is what an atomic
	// write looks like. IN_CLOSE_WRITE catches an editor that writes in
	// place, and IN_CREATE a file that was not there at all.
	const events = unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE | unix.IN_CREATE
	if _, err := unix.InotifyAddWatch(descriptor, directory, events); err != nil {
		log.Warningf("cannot watch %s for changes: %s", directory, err)
		return
	}

	go func() {
		<-ctx.Done()
		// Closing the descriptor is what stops the read below.
		_ = unix.Close(descriptor)
	}()

	log.Debugf("watching %s for changes", filename)

	wanted := filepath.Base(filename)
	buffer := make([]byte, 4096)
	for {
		read, err := unix.Read(descriptor, buffer)
		if err != nil || ctx.Err() != nil {
			return
		}
		if !mentions(buffer[:read], wanted) {
			continue
		}

		// A pause before reading. An editor may write, move and set the mode
		// of a file in quick succession, and reloading between two of those
		// reads a file that is momentarily not what anybody intended.
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
		drain(descriptor)

		reloaded, err := self.store.ReloadIfChanged()
		if err != nil {
			log.Warningf("%s changed but could not be read: %s", filename, err)
			continue
		}
		if reloaded {
			log.Noticef("%s was edited; applying it", filename)
		}
	}
}

// mentions reports whether any event in this buffer names the file.
//
// An inotify read returns a run of variable-length records: a fixed header,
// then a name padded out to a multiple of the header's alignment.
func mentions(buffer []byte, name string) bool {
	for offset := 0; offset+unix.SizeofInotifyEvent <= len(buffer); {
		event := (*unix.InotifyEvent)(unsafe.Pointer(&buffer[offset]))
		length := int(event.Len)
		start := offset + unix.SizeofInotifyEvent
		if start+length > len(buffer) {
			return false
		}
		if length > 0 {
			written := string(trimAfterNul(buffer[start : start+length]))
			if written == name {
				return true
			}
		}
		offset = start + length
	}
	return false
}

func trimAfterNul(raw []byte) []byte {
	for index, character := range raw {
		if character == 0 {
			return raw[:index]
		}
	}
	return raw
}

// drain takes whatever else has piled up without blocking, so that a burst of
// events from one edit becomes one reload rather than several.
func drain(descriptor int) {
	if err := unix.SetNonblock(descriptor, true); err != nil {
		return
	}
	defer func() { _ = unix.SetNonblock(descriptor, false) }()

	buffer := make([]byte, 4096)
	for {
		if _, err := unix.Read(descriptor, buffer); err != nil {
			return
		}
	}
}
