package devices

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The real device files cannot be created in a test, so the behaviour is
// checked against ordinary files whose group is the one the test process is
// already in — which is enough, because what is being tested is the reading
// of a group from a file and the shape of the answer, not anything about
// device nodes.
func TestGroupsReadsTheOwningGroupsAndDeduplicates(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"card0", "renderD128", "controlC0"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o660); err != nil {
			t.Fatalf("write: %s", err)
		}
	}

	groups := Groups(directory)

	// Every file has the same group, so however many there are the answer is
	// one group — a process cannot usefully be added to the same group twice.
	if len(groups) > 1 {
		t.Errorf("three files with one group between them gave %v", groups)
	}
	if len(groups) == 1 && groups[0] != uint32(syscall.Getgid()) {
		t.Errorf("the group is %d, want this process's own %d", groups[0], syscall.Getgid())
	}
}

func TestGroupZeroIsNeverReturned(t *testing.T) {
	// Group zero is root. Adding an unprivileged process to it would be the
	// opposite of the point of this package, so a file owned by it is skipped
	// rather than reported.
	information, err := os.Stat("/etc/hostname")
	if err != nil {
		t.Skip("no /etc/hostname to look at")
	}
	if group, ok := groupOf(information); ok && group == 0 {
		t.Error("group zero was reported")
	}
}

func TestAPathThatIsNotThereIsSkipped(t *testing.T) {
	// A machine with no sound card is an ordinary machine.
	if groups := Groups("/nonexistent/dri", "/nonexistent/snd"); len(groups) != 0 {
		t.Errorf("groups from nothing: %v", groups)
	}
}

func TestASingleFileWorksAsWellAsADirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renderD128")
	if err := os.WriteFile(path, nil, 0o660); err != nil {
		t.Fatalf("write: %s", err)
	}
	if groups := Groups(path); len(groups) != 1 {
		t.Errorf("naming one file gave %v, want one group", groups)
	}
}

func TestDescribeSaysSomethingUsefulWhenThereIsNothing(t *testing.T) {
	// The case worth wording carefully: a container with no /dev/dri renders
	// in software and says nothing about why.
	text := Describe([]string{"/nonexistent"})
	if text == "" {
		t.Fatal("no description at all")
	}
	if !contains(text, "/dev/dri") {
		t.Errorf("the description does not suggest what is wrong: %q", text)
	}
}

func contains(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
