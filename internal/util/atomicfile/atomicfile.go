// Package atomicfile writes a file by writing a temporary file next to it and
// renaming it into place. A configuration file that is half written is worse
// than one that was never written: the daemon that reads it at the next boot
// cannot tell the difference between a truncated file and a deliberate one.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Write replaces filename with data, atomically. The temporary file is
// created in the same directory so that the rename cannot cross a filesystem
// boundary, and it is removed if anything fails before the rename.
func Write(filename string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("atomicfile: create %s: %w", directory, err)
	}

	file, err := os.CreateTemp(directory, "."+filepath.Base(filename)+".*")
	if err != nil {
		return fmt.Errorf("atomicfile: create a temporary file in %s: %w", directory, err)
	}
	temporary := file.Name()

	// Every failure from here on has to remove the temporary file, or a
	// directory that is written to often slowly fills with them.
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(temporary)
	}

	if _, err := file.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: write %s: %w", temporary, err)
	}
	// Chmod rather than relying on CreateTemp's 0600, because a configuration
	// file that another user has to read is a legitimate thing to want.
	if err := file.Chmod(mode); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: set the mode of %s: %w", temporary, err)
	}
	// Sync before the rename: the rename is atomic with respect to other
	// readers, but not with respect to a power cut, and these machines lose
	// power without warning.
	if err := file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: sync %s: %w", temporary, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("atomicfile: close %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, filename); err != nil {
		_ = os.Remove(temporary)
		if errors.Is(err, syscall.EBUSY) {
			// This one is worth explaining. Renaming onto a file that is a
			// bind mount fails with "device or resource busy", and the usual
			// cause is a container started with the configuration file
			// mounted individually:
			//
			//     -v ./cue.yaml:/etc/cue/cue.yaml
			//
			// Nothing can ever replace that file, so every save fails and the
			// message on its own sends people looking at permissions.
			return fmt.Errorf("atomicfile: cannot replace %s because something else is holding that exact file "+
				"— if this is a container, mount the directory %s rather than the file itself: %w",
				filename, directory, err)
		}
		return fmt.Errorf("atomicfile: rename %s to %s: %w", temporary, filename, err)
	}
	return nil
}
