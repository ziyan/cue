package web

import (
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"testing"
)

// The interface is a set of ES modules that import each other by path, and
// they are compiled into the binary. A file renamed or forgotten produces a
// blank page in a browser and nothing at all in the daemon's log, which is a
// bad way to find out. So: every import in every module has to resolve to a
// file that is actually in the binary.
func TestEveryModuleImportResolves(t *testing.T) {
	content, err := fs.Sub(staticFiles, "static")
	if err != nil {
		t.Fatalf("sub: %s", err)
	}

	importPattern := regexp.MustCompile(`(?m)^\s*import\s+[^"']*["']([^"']+)["']`)
	checked := 0

	err = fs.WalkDir(content, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(name, ".js") {
			return err
		}
		// The vendored client is somebody else's code and is checked as a
		// whole by the smoke test loading it, not module by module here.
		if strings.HasPrefix(name, "novnc/") {
			return nil
		}

		source, err := fs.ReadFile(content, name)
		if err != nil {
			return err
		}

		for _, match := range importPattern.FindAllStringSubmatch(string(source), -1) {
			target := match[1]
			if !strings.HasPrefix(target, ".") {
				// A bare specifier would need an import map, which this
				// interface deliberately does not have.
				t.Errorf("%s imports %q, which a browser cannot resolve without an import map", name, target)
				continue
			}
			resolved := path.Clean(path.Join(path.Dir(name), target))
			if _, err := fs.Stat(content, resolved); err != nil {
				t.Errorf("%s imports %q, which is %s, and that is not in the binary", name, target, resolved)
			}
			checked++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %s", err)
	}

	if checked == 0 {
		t.Fatal("no imports were checked at all; this test is not testing anything")
	}
	t.Logf("%d imports resolve", checked)
}

// The files index.html asks for have to be there too, and they are named in
// markup rather than in an import.
func TestTheShellsOwnFilesAreThere(t *testing.T) {
	content, err := fs.Sub(staticFiles, "static")
	if err != nil {
		t.Fatalf("sub: %s", err)
	}

	shell, err := fs.ReadFile(content, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %s", err)
	}

	referencePattern := regexp.MustCompile(`(?:href|src)="(/[^"]+)"`)
	found := 0
	for _, match := range referencePattern.FindAllStringSubmatch(string(shell), -1) {
		target := strings.TrimPrefix(match[1], "/")
		if _, err := fs.Stat(content, target); err != nil {
			t.Errorf("index.html asks for %q, which is not in the binary", match[1])
		}
		found++
	}
	if found == 0 {
		t.Error("index.html asks for nothing at all, which cannot be right")
	}
}

// And the vendored client's own entry point, which the Screen page imports.
func TestTheVendoredVNCClientIsThere(t *testing.T) {
	content, err := fs.Sub(staticFiles, "static")
	if err != nil {
		t.Fatalf("sub: %s", err)
	}
	for _, name := range []string{"novnc/core/rfb.js", "novnc/LICENSE.txt"} {
		if _, err := fs.Stat(content, name); err != nil {
			t.Errorf("%s is missing; the Screen page cannot work without it", name)
		}
	}
}

// Serving them is a different thing from having them, and the fall-back to
// the shell for unknown paths must not swallow a real file.
func TestTheAssetsAreServedWithoutFallingBackToTheShell(t *testing.T) {
	server := newTestServer(t, defaultConfigurationForTest())

	for _, path := range []string{"/app.js", "/style.css", "/dom.js", "/api.js",
		"/pages/overview.js", "/pages/content.js", "/pages/screen.js", "/pages/device.js",
		"/novnc/core/rfb.js"} {
		response := do(server, http.MethodGet, path, nil, nil)
		if response.Code != http.StatusOK {
			t.Errorf("%s answered %d", path, response.Code)
			continue
		}
		// The fall-back serves index.html for a path the interface routes
		// itself. A JavaScript file answered with HTML is a module that will
		// not parse, and the browser's only complaint is in its own console.
		if strings.HasSuffix(path, ".js") && strings.Contains(response.Body.String(), "<!doctype html>") {
			t.Errorf("%s was answered with the shell instead of the file", path)
		}
	}
}
