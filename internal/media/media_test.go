package media

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "videos"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestAVideoComesBackExactlyAsItWentIn(t *testing.T) {
	store := newTestStore(t)
	content := bytes.Repeat([]byte("a test video's bytes"), 5000)

	stored, err := store.Add("promo.mp4", "video/mp4", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("adding: %s", err)
	}
	if stored.Name != "promo.mp4" || stored.Size != int64(len(content)) {
		t.Errorf("stored as %+v", stored)
	}

	path, err := store.Path(stored.File)
	if err != nil {
		t.Fatalf("finding it again: %s", err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, content) {
		t.Error("what came back is not what went in")
	}
}

// The same file uploaded twice must cost one copy, which is the whole reason
// for naming files after their contents.
func TestTheSameVideoTwiceIsStoredOnce(t *testing.T) {
	store := newTestStore(t)
	content := []byte("the same test bytes both times")

	first, err := store.Add("promo.mp4", "video/mp4", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Add("a-different-name.mp4", "video/mp4", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if first.File != second.File {
		t.Errorf("the same bytes were stored as %s and %s", first.File, second.File)
	}

	videos, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) != 1 {
		t.Errorf("the store holds %d videos after adding one file twice", len(videos))
	}
}

func TestDifferentVideosAreStoredSeparately(t *testing.T) {
	store := newTestStore(t)

	first, err := store.Add("one.mp4", "video/mp4", strings.NewReader("the first test video"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Add("two.mp4", "video/mp4", strings.NewReader("the second test video"))
	if err != nil {
		t.Fatal(err)
	}
	if first.File == second.File {
		t.Error("two different files were stored under one name")
	}
}

// A name from a request must never be able to reach outside the store. This is
// the one place a stored file's name is turned into a path.
func TestANameFromARequestCannotReachOutsideTheStore(t *testing.T) {
	store := newTestStore(t)

	for _, attempt := range []string{
		"../../../../etc/shadow",
		"..",
		"/etc/shadow",
		"abcdef/../../etc/shadow",
		"0123456789abcdef0123456789abcdef/../../etc/shadow",
		"",
		"0123456789ABCDEF0123456789abcdef",
		"0123456789abcdef0123456789abcde",
	} {
		if _, err := store.Path(attempt); err == nil {
			t.Errorf("Path accepted %q", attempt)
		}
		if err := store.Remove(attempt); err == nil {
			t.Errorf("Remove accepted %q", attempt)
		}
	}
}

// An upload that dies half way must leave nothing that looks like a whole
// video, or a screen would play a truncated file and nobody would know why.
func TestAnUploadThatFailsLeavesNothingBehind(t *testing.T) {
	store := newTestStore(t)

	broken := io.MultiReader(strings.NewReader("the start of a test video"), failingReader{})
	if _, err := store.Add("promo.mp4", "video/mp4", broken); err == nil {
		t.Fatal("a failed upload was accepted")
	}

	videos, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) != 0 {
		t.Errorf("a failed upload left %d video(s) behind: %+v", len(videos), videos)
	}
}

func TestAnEmptyUploadIsRefused(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Add("empty.mp4", "video/mp4", strings.NewReader("")); err == nil {
		t.Error("an empty file was stored as a video")
	}
}

// Sweeping is what keeps the disk of a machine nobody logs into from filling
// up with last year's promotions.
func TestSweepingRemovesOnlyWhatNothingRefersTo(t *testing.T) {
	store := newTestStore(t)

	kept, err := store.Add("kept.mp4", "video/mp4", strings.NewReader("a test video that is still used"))
	if err != nil {
		t.Fatal(err)
	}
	dropped, err := store.Add("dropped.mp4", "video/mp4", strings.NewReader("a test video nobody wants"))
	if err != nil {
		t.Fatal(err)
	}
	// Both are new, and new files are left alone; age them so the sweep will
	// consider them.
	ageEverything(t, store)

	removed, err := store.Sweep([]string{kept.File})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != dropped.File {
		t.Errorf("the sweep removed %v, want just %s", removed, dropped.File)
	}
	if _, err := store.Path(kept.File); err != nil {
		t.Errorf("the sweep removed a video an item still names: %s", err)
	}
	if _, err := store.Path(dropped.File); err == nil {
		t.Error("the video nothing refers to is still there")
	}
}

// A video that has just been uploaded is, for a moment, a video nothing refers
// to -- its item has not been saved yet. Sweeping it then would lose it, and
// the loss would look random.
func TestSweepingLeavesAVideoThatHasJustBeenUploaded(t *testing.T) {
	store := newTestStore(t)

	fresh, err := store.Add("just-uploaded.mp4", "video/mp4", strings.NewReader("a test video not yet saved"))
	if err != nil {
		t.Fatal(err)
	}

	removed, err := store.Sweep(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("the sweep removed %v, and one of them had only just been uploaded", removed)
	}
	if _, err := store.Path(fresh.File); err != nil {
		t.Errorf("a video uploaded a moment ago was swept away: %s", err)
	}
}

func TestSweepingClearsUpAbandonedUploads(t *testing.T) {
	store := newTestStore(t)

	abandoned := filepath.Join(store.directory, "incoming-123456")
	if err := os.WriteFile(abandoned, []byte("half a test video"), 0o644); err != nil {
		t.Fatal(err)
	}
	ageEverything(t, store)

	if _, err := store.Sweep(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Error("an abandoned upload is still taking up space")
	}
}

func TestTheNameShownIsTidiedButTheFileIsNot(t *testing.T) {
	store := newTestStore(t)

	stored, err := store.Add("/some/where/../promo\x07.mp4", "video/mp4", strings.NewReader("a test video"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(stored.Name, "/\x07") {
		t.Errorf("the name shown is %q, which still has a path or a control character in it", stored.Name)
	}
	if !safeFile.MatchString(stored.File) {
		t.Errorf("the file is stored as %q, which is not safe to put in a path", stored.File)
	}
}

func ageEverything(t *testing.T, store *Store) {
	t.Helper()
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * settleTime)
	for _, entry := range entries {
		if err := os.Chtimes(filepath.Join(store.directory, entry.Name()), old, old); err != nil {
			t.Fatal(err)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("the connection went away")
}

// What sort of thing something is decides how the screen shows it and what
// says when it is finished, so it has to be right.
func TestWhatSortOfThingSomethingIs(t *testing.T) {
	for mediaType, want := range map[string]Kind{
		"image/png":       KindPicture,
		"image/jpeg":      KindPicture,
		"image/gif":       KindPicture,
		"image/webp":      KindPicture,
		"video/mp4":       KindVideo,
		"video/webm":      KindVideo,
		"video/quicktime": KindVideo,
		// Anything unrecognised is treated as a video, because a video is the
		// thing with an end: calling a video a picture would leave a screen on
		// a frozen first frame and then move on.
		"application/octet-stream": KindVideo,
	} {
		if got := KindOf(mediaType); got != want {
			t.Errorf("%s is treated as %q, want %q", mediaType, got, want)
		}
	}
}

func TestOnlyPicturesAndVideosAreAccepted(t *testing.T) {
	for mediaType, want := range map[string]bool{
		"image/png": true, "video/mp4": true,
		"text/html": false, "application/pdf": false, "text/plain": false, "": false,
	} {
		if got := Playable(mediaType); got != want {
			t.Errorf("Playable(%q) is %v, want %v", mediaType, got, want)
		}
	}
}

func TestAPictureIsStoredWithItsKind(t *testing.T) {
	store := newTestStore(t)

	stored, err := store.Add("poster.png", "image/png", strings.NewReader("a test picture's bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Kind != KindPicture {
		t.Errorf("a PNG was stored as %q", stored.Kind)
	}

	// And it comes back that way, because the interface asks the store rather
	// than parsing media types again.
	back, err := store.Details(stored.File)
	if err != nil {
		t.Fatal(err)
	}
	if back.Kind != KindPicture {
		t.Errorf("the stored picture reads back as %q", back.Kind)
	}
}

// A store written by an older version has to be picked up, not left behind.
// The playlist items name files by digest, so a device that ignored the old
// directory would show nothing where a video used to be while the bytes sat on
// the disk for ever.
func TestAStoreFromAnOlderVersionIsAdopted(t *testing.T) {
	state := t.TempDir()
	previous := filepath.Join(state, "videos")
	current := filepath.Join(state, "media")

	if err := os.MkdirAll(previous, 0o755); err != nil {
		t.Fatal(err)
	}
	const digest = "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(previous, digest+".video"), []byte("a test video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previous, digest+".json"),
		[]byte(`{"name":"promo.mp4","size":12,"type":"video/mp4"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	Adopt(previous, current)
	store, err := Open(current)
	if err != nil {
		t.Fatal(err)
	}

	// The file is found under the name the playlist already knows it by.
	path, err := store.Path(digest)
	if err != nil {
		t.Fatalf("the video from the older version was lost: %s", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "a test video" {
		t.Errorf("the bytes did not survive: %q (%v)", content, err)
	}
	// And what is known about it came across too.
	details, err := store.Details(digest)
	if err != nil || details.Name != "promo.mp4" {
		t.Errorf("what was known about it was lost: %+v (%v)", details, err)
	}
	// It is renamed to what this version calls it, so nothing keeps looking
	// for the old suffix.
	if _, err := os.Stat(filepath.Join(current, digest+".video")); !os.IsNotExist(err) {
		t.Error("the old file name is still there")
	}
	if _, err := os.Stat(previous); !os.IsNotExist(err) {
		t.Error("the old directory is still there")
	}
}

// If this version has already stored something, the old directory must be left
// alone rather than moved over the top of it.
func TestAdoptingDoesNotOverwriteWhatIsAlreadyThere(t *testing.T) {
	state := t.TempDir()
	previous := filepath.Join(state, "videos")
	current := filepath.Join(state, "media")

	if err := os.MkdirAll(previous, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previous, "old.media"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(current)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := store.Add("new.mp4", "video/mp4", strings.NewReader("a newer test video"))
	if err != nil {
		t.Fatal(err)
	}

	Adopt(previous, current)

	if _, err := store.Path(kept.File); err != nil {
		t.Errorf("adopting an old directory destroyed what was already stored: %s", err)
	}
}
