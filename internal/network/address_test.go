package network

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A bind-mounted file is one whose inode cannot be replaced but whose contents
// can be written. Mounting one in a test needs privileges a test does not
// have, so the shape is reproduced instead: the file is there and writable,
// and the directory around it is not, which is what makes the rename fail in
// exactly the way the mount does.
func immovableFile(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	filename := filepath.Join(directory, "resolv.conf")

	if err := os.WriteFile(filename, []byte("nameserver 203.0.113.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o755) })

	// Running as root defeats the directory permission, and the point of the
	// test with it.
	if syscall.Geteuid() == 0 {
		t.Skip("running as root, so an unwritable directory is still writable")
	}
	return filename
}

func TestNameServersAreWrittenEvenWhenTheFileCannotBeReplaced(t *testing.T) {
	// This is a container's /etc/resolv.conf. Giving up on it looked like a
	// reasonable degradation and is not one: a device that cannot resolve the
	// address of its dashboard is a black screen.
	filename := immovableFile(t)

	previous := resolvConfFilename
	resolvConfFilename = filename
	t.Cleanup(func() { resolvConfFilename = previous })

	if err := writeNameservers([]string{"192.0.2.53", "192.0.2.54"}, "example.invalid"); err != nil {
		t.Fatalf("the name servers were not written: %s", err)
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"nameserver 192.0.2.53", "nameserver 192.0.2.54", "search example.invalid"} {
		if !strings.Contains(string(content), wanted) {
			t.Errorf("%q is missing from the file:\n%s", wanted, content)
		}
	}
	if strings.Contains(string(content), "203.0.113.1") {
		t.Error("the old contents are still there, so the file was appended to rather than replaced")
	}
}

func TestSomethingThatIsNotAnAddressIsNotWritten(t *testing.T) {
	// The values come from a DHCP server or from a form, and a resolv.conf
	// with a hostname where an address belongs is one glibc stops reading at.
	directory := t.TempDir()
	filename := filepath.Join(directory, "resolv.conf")

	previous := resolvConfFilename
	resolvConfFilename = filename
	t.Cleanup(func() { resolvConfFilename = previous })

	if err := writeNameservers([]string{"192.0.2.53", "not-an-address", ""}, ""); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "not-an-address") {
		t.Errorf("a name that is not an address was written:\n%s", content)
	}
	if !strings.Contains(string(content), "192.0.2.53") {
		t.Errorf("the good address was dropped with the bad one:\n%s", content)
	}
}
