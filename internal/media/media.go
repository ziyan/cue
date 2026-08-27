// Package media keeps the pictures and videos an operator has uploaded on the
// device's own disk, so that a screen goes on showing them with no network at
// all.
//
// A picture and a video are the same sort of thing here. They are uploaded the
// same way, stored the same way, named the same way and swept the same way,
// and they differ in exactly two places: which element the player draws them
// with, and what decides when they are finished. Keeping two parallel sets of
// code for that would be twice as much to get right.
//
// Files are stored under a digest of their own contents rather than under the
// name they arrived with. Uploaded names are not unique, not safe to use as
// paths, and not stable: two people upload "promo.mp4" and mean different
// files. A digest is unique, safe, and the same for the same bytes, so
// uploading one file twice costs one copy -- and it makes cleaning up exact,
// because a file is wanted if some playlist item names it and unwanted
// otherwise, with no bookkeeping that can drift out of step with the truth.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("media")

// Kind is what sort of thing a stored file is, which decides how the screen
// shows it and what says when it has finished.
type Kind string

const (
	// KindVideo plays, and the playlist moves on when it ends.
	KindVideo Kind = "video"

	// KindPicture is shown, and the playlist moves on when the ordinary
	// rotation time is up -- a picture having no end of its own.
	KindPicture Kind = "picture"
)

// Stored is one file on the device.
type Stored struct {
	// File is what it is stored under, and what a playlist item names. It is
	// hexadecimal and nothing else, which is what makes it safe to put in a
	// path built from a request.
	File string `json:"file"`

	// Name is what it was called when it arrived, for the interface to show.
	Name string `json:"name"`

	Size int64  `json:"size"`
	Type string `json:"type"`

	// Kind is worked out from Type when the file arrives, so that nothing
	// later has to parse a media type again to know what to do with it.
	Kind Kind `json:"kind"`
}

// Store is the directory the uploads live in.
type Store struct {
	directory string
}

// Open prepares the store. The directory is created if it is not there.
func Open(directory string) (*Store, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("media: cannot create %s: %w", directory, err)
	}
	store := &Store{directory: directory}
	if err := store.adoptOldNames(); err != nil {
		return nil, err
	}
	return store, nil
}

// Adopt takes over a directory an older version used, if it is still there and
// this one has not been used yet.
//
// This store used to be called "videos", before it held pictures too. Somebody
// upgrading has files under the old name and playlist items naming them, and
// leaving those behind would be a screen that suddenly shows nothing where a
// video used to be -- while the bytes sit on the disk for ever, because
// nothing knows about them any more.
func Adopt(previous, current string) {
	if previous == current {
		return
	}
	if _, err := os.Stat(previous); err != nil {
		return
	}
	// Only into an empty place. If both hold something, this version has
	// already stored files under the new name and moving the old directory
	// over them would either fail or lose them.
	if entries, err := os.ReadDir(current); err == nil && len(entries) > 0 {
		log.Warningf("%s and %s both hold files; leaving the older one alone", previous, current)
		return
	}
	_ = os.Remove(current)
	if err := os.Rename(previous, current); err != nil {
		log.Warningf("cannot move %s to %s: %s", previous, current, err)
		return
	}
	log.Noticef("moved what was in %s to %s", previous, current)
}

// adoptOldNames renames the files an older version wrote.
//
// The bytes of an upload used to be kept as "<digest>.video", which stopped
// being true when the same store began holding pictures. The digest is what
// everything else refers to, so only the suffix changes and nothing that names
// a file has to be touched.
func (self *Store) adoptOldNames() error {
	entries, err := os.ReadDir(self.directory)
	if err != nil {
		return fmt.Errorf("media: cannot read %s: %w", self.directory, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".video") {
			continue
		}
		file := strings.TrimSuffix(name, ".video")
		if !safeFile.MatchString(file) {
			continue
		}
		if err := os.Rename(filepath.Join(self.directory, name), self.contents(file)); err != nil {
			log.Warningf("cannot rename %s: %s", name, err)
			continue
		}
		log.Noticef("renamed %s to what this version calls it", name)
	}
	return nil
}

// safeFile is what a stored file's name may look like: hexadecimal, nothing
// else. Names arrive in requests, and a name that could contain a slash or a
// pair of dots is a way to ask this store for /etc/shadow.
var safeFile = regexp.MustCompile(`^[0-9a-f]{32}$`)

// maximumName is how much of an uploaded name is kept. Somebody's file manager
// will one day produce a name thousands of characters long, and it is only
// ever shown in a list.
const maximumName = 120

// Add streams a video in and returns what it was stored as.
//
// The bytes go to a temporary file in the same directory while the digest is
// computed, and that file is renamed into place at the end. Nothing is held in
// memory: these are hundreds of megabytes. And nothing half-written is ever
// visible under a real name, because a rename within one directory either
// happens or does not.
func (self *Store) Add(name, mediaType string, source io.Reader) (Stored, error) {
	temporary, err := os.CreateTemp(self.directory, "incoming-*")
	if err != nil {
		return Stored{}, fmt.Errorf("media: cannot start storing a video: %w", err)
	}
	defer func() {
		_ = temporary.Close()
		// Harmless once the rename has happened, and the whole point when it
		// has not.
		_ = os.Remove(temporary.Name())
	}()

	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, digest), source)
	if err != nil {
		return Stored{}, fmt.Errorf("media: cannot store a video: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Stored{}, fmt.Errorf("media: cannot finish storing a video: %w", err)
	}
	if size == 0 {
		return Stored{}, fmt.Errorf("media: there was nothing in that file")
	}

	stored := Stored{
		File: hex.EncodeToString(digest.Sum(nil))[:32],
		Name: tidyName(name),
		Size: size,
		Type: mediaType,
		Kind: KindOf(mediaType),
	}

	if err := os.Rename(temporary.Name(), self.contents(stored.File)); err != nil {
		return Stored{}, fmt.Errorf("media: cannot store a video: %w", err)
	}
	if err := self.writeDetails(stored); err != nil {
		return Stored{}, err
	}
	log.Noticef("stored %q as %s, %d byte(s)", stored.Name, stored.File, stored.Size)
	return stored, nil
}

// Path is where a stored video's bytes are, for serving it back.
func (self *Store) Path(file string) (string, error) {
	if !safeFile.MatchString(file) {
		return "", fmt.Errorf("media: %q is not the name of a stored video", file)
	}
	path := self.contents(file)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("media: no video is stored as %s", file)
	}
	return path, nil
}

// Details is what is known about one stored video.
func (self *Store) Details(file string) (Stored, error) {
	if !safeFile.MatchString(file) {
		return Stored{}, fmt.Errorf("media: %q is not the name of a stored video", file)
	}
	return self.readDetails(file)
}

// Remove deletes one, and what is known about it. Removing something that is
// not there is not an error: the point is that it is gone.
func (self *Store) Remove(file string) error {
	if !safeFile.MatchString(file) {
		return fmt.Errorf("media: %q is not the name of a stored video", file)
	}
	if err := os.Remove(self.contents(file)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("media: cannot remove %s: %w", file, err)
	}
	if err := os.Remove(self.details(file)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("media: cannot remove what is known about %s: %w", file, err)
	}
	return nil
}

// List is everything stored.
func (self *Store) List() ([]Stored, error) {
	entries, err := os.ReadDir(self.directory)
	if err != nil {
		return nil, fmt.Errorf("media: cannot read %s: %w", self.directory, err)
	}

	var stored []Stored
	for _, entry := range entries {
		file := strings.TrimSuffix(entry.Name(), ".media")
		if entry.IsDir() || file == entry.Name() || !safeFile.MatchString(file) {
			continue
		}
		details, err := self.readDetails(file)
		if err != nil {
			continue
		}
		stored = append(stored, details)
	}
	return stored, nil
}

// settleTime is how long a newly written file is left alone even when nothing
// refers to it.
//
// A video that has finished uploading but whose item has not been saved yet is
// exactly a file nothing refers to. Without this, uploading one and then
// saving would lose it every time, and the loss would look random.
const settleTime = 15 * time.Minute

// Sweep deletes every stored video not in wanted, and returns what it deleted.
//
// This is what keeps the disk of a machine nobody logs into from filling with
// videos of last year's promotions. It is exact because of how files are
// named: an item names a digest, a file is that digest, and there is nothing
// in between to get out of step.
func (self *Store) Sweep(wanted []string) ([]string, error) {
	keep := make(map[string]bool, len(wanted))
	for _, file := range wanted {
		keep[file] = true
	}

	entries, err := os.ReadDir(self.directory)
	if err != nil {
		return nil, fmt.Errorf("media: cannot read %s: %w", self.directory, err)
	}

	var removed []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		file := strings.TrimSuffix(strings.TrimSuffix(name, ".media"), ".json")

		// An abandoned upload, from a request that died halfway.
		if strings.HasPrefix(name, "incoming-") {
			if recentlyWritten(entry) {
				continue
			}
			if err := os.Remove(filepath.Join(self.directory, name)); err == nil {
				removed = append(removed, name)
			}
			continue
		}

		if !safeFile.MatchString(file) || keep[file] {
			continue
		}
		if recentlyWritten(entry) {
			continue
		}
		if err := os.Remove(filepath.Join(self.directory, name)); err != nil {
			log.Warningf("cannot remove the unused video %s: %s", name, err)
			continue
		}
		if strings.HasSuffix(name, ".media") {
			removed = append(removed, file)
		}
	}
	return removed, nil
}

func recentlyWritten(entry os.DirEntry) bool {
	information, err := entry.Info()
	if err != nil {
		// Cannot tell how old it is, so leave it alone. Deleting something on
		// a guess is the worse mistake.
		return true
	}
	return time.Since(information.ModTime()) < settleTime
}

func (self *Store) contents(file string) string {
	return filepath.Join(self.directory, file+".media")
}

func (self *Store) details(file string) string {
	return filepath.Join(self.directory, file+".json")
}

// writeDetails records what a digest cannot say: what the file was called and
// what it is. Without it the interface could only ever show a row of
// hexadecimal.
func (self *Store) writeDetails(stored Stored) error {
	encoded, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if err := os.WriteFile(self.details(stored.File), encoded, 0o644); err != nil {
		return fmt.Errorf("media: cannot record what %s is: %w", stored.File, err)
	}
	return nil
}

func (self *Store) readDetails(file string) (Stored, error) {
	content, err := os.ReadFile(self.details(file))
	if err != nil {
		// The bytes may still be there without their description, if the
		// daemon died between writing the two. That is worth reporting as a
		// video rather than hiding, so it can at least be played and deleted.
		if information, statErr := os.Stat(self.contents(file)); statErr == nil {
			return Stored{File: file, Name: file, Size: information.Size(),
				Type: "video/mp4", Kind: KindVideo}, nil
		}
		return Stored{}, err
	}
	var stored Stored
	if err := json.Unmarshal(content, &stored); err != nil {
		return Stored{}, err
	}
	stored.File = file
	if stored.Kind == "" {
		stored.Kind = KindOf(stored.Type)
	}
	return stored, nil
}

// tidyName makes an uploaded name safe to show and short enough to fit.
//
// It is never used as a path -- the digest is -- so this is only about what a
// person reads. Control characters are stripped because a name containing them
// can make a log line lie about its own shape.
func tidyName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == "" {
		name = "video"
	}

	var builder strings.Builder
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			continue
		}
		builder.WriteRune(character)
		if builder.Len() >= maximumName {
			break
		}
	}
	if builder.Len() == 0 {
		return "video"
	}
	return builder.String()
}

// KindOf says what sort of thing a media type is.
//
// Anything that is not a picture is treated as a video, because a video is the
// thing with an end and getting that wrong the other way -- treating a video
// as a picture -- would leave a screen showing a frozen first frame for the
// rotation time and then moving on.
func KindOf(mediaType string) Kind {
	if strings.HasPrefix(mediaType, "image/") {
		return KindPicture
	}
	return KindVideo
}

// Playable reports whether this is something a screen can show at all, which
// is what the upload checks before storing anything.
func Playable(mediaType string) bool {
	return strings.HasPrefix(mediaType, "video/") || strings.HasPrefix(mediaType, "image/")
}
