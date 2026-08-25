package web

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every class the interface asks for has to exist in the stylesheet.
//
// This exists because of a mistake worth not repeating: a block was removed
// from the stylesheet by cutting between two landmarks, and the second landmark
// had moved to the end of the file, so everything between them went with it —
// the sign-in box's width among them. The symptom was a login form stretched
// across a whole monitor, and nothing failed: the page rendered, the tests
// passed, and the only way to notice was to look at it.
//
// A stylesheet cannot be tested for looking right. It can be tested for still
// containing rules for the things the interface puts on the screen.
func TestEveryClassTheInterfaceUsesIsStyled(t *testing.T) {
	stylesheet, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatalf("cannot read the stylesheet: %s", err)
	}
	styled := classesDefinedIn(stripCSSComments(string(stylesheet)))

	// Classes that are deliberately not styled: they mark something for the
	// tests or for a script and are never drawn.
	allowed := map[string]bool{}

	var missing []string
	for _, name := range classesUsedInScripts(t) {
		if styled[name] || allowed[name] {
			continue
		}
		missing = append(missing, name)
	}
	for _, name := range missing {
		t.Errorf("the interface puts class %q on an element and the stylesheet has no rule for it", name)
	}
}

var (
	cssComment    = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssClassRule  = regexp.MustCompile(`\.([a-zA-Z][\w-]*)`)
	scriptClasses = regexp.MustCompile("class:\\s*[`\"']([^`\"']*)")
	plausibleName = regexp.MustCompile(`^[a-zA-Z][\w-]*$`)
)

func stripCSSComments(text string) string {
	return cssComment.ReplaceAllString(text, "")
}

func classesDefinedIn(stylesheet string) map[string]bool {
	found := map[string]bool{}
	for _, match := range cssClassRule.FindAllStringSubmatch(stylesheet, -1) {
		found[match[1]] = true
	}
	return found
}

// classesUsedInScripts reads the class names the interface asks for. Template
// expressions are skipped: a class built from a variable cannot be checked
// here, and guessing at one would make this test lie.
func classesUsedInScripts(t *testing.T) []string {
	t.Helper()

	var names []string
	seen := map[string]bool{}
	err := fs.WalkDir(staticFiles, "static", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "novnc" {
			return fs.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".js" {
			return nil
		}
		content, err := staticFiles.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range scriptClasses.FindAllStringSubmatch(string(content), -1) {
			for _, name := range strings.Fields(match[1]) {
				if !plausibleName.MatchString(name) || seen[name] {
					continue
				}
				seen[name] = true
				names = append(names, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cannot read the interface: %s", err)
	}
	if len(names) < 20 {
		t.Fatalf("only %d classes were found, so this test would pass for the wrong reason", len(names))
	}
	return names
}
