package config

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ziyan/cue/internal/util/atomicfile"
)

// The example file written beside the real one.
//
// It exists because of what the real file no longer contains. Only settings
// somebody chose are written there, which makes it short and honest but means
// a reader cannot see what else there is, or what a setting starts as. The
// example is the other half: every setting, with the value it has when nobody
// has said otherwise, entirely commented out.
//
// It is written by the daemon rather than shipped as a file, so that it can
// never describe a different version from the one running. It is rewritten at
// every start for the same reason.

// exampleHeader explains what the reader is looking at.
const exampleHeader = `# Every setting Cue has, and what it is when nobody says otherwise.
#
# This file does nothing. It is written by the daemon at every start so that it
# always matches the version running, and it is overwritten, so do not edit it.
#
# The real file beside this one holds only what somebody chose: a setting that
# matches the default is left out of it, which is why it is short. To change
# something, copy the line here into cue.yaml and edit it there.
#
# What each setting means: docs/reference/configuration.md
`

// ExampleFilename is where the example goes, given the real file's path.
func ExampleFilename(filename string) string {
	return filepath.Join(filepath.Dir(filename), filepath.Base(filename)+".example")
}

// WriteExample writes the commented-out full configuration beside the real
// file.
func WriteExample(filename string) error {
	content, err := Example()
	if err != nil {
		return err
	}
	// 0644, unlike the real file: there are no secrets in it, and somebody
	// reading a device over SSH as themselves should be able to look.
	return atomicfile.Write(ExampleFilename(filename), content, 0o644)
}

// Example is the contents of that file.
func Example() ([]byte, error) {
	baseline := Default()
	baseline.Normalize()
	// The identifier is generated per device and means nothing here; showing
	// one would invite somebody to copy it, and two devices with the same
	// identifier is a confusing thing to debug.
	baseline.Device.Identifier = ""

	var body bytes.Buffer
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(baseline); err != nil {
		return nil, fmt.Errorf("config: cannot write the example: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("config: cannot write the example: %w", err)
	}

	var buffer bytes.Buffer
	buffer.WriteString(exampleHeader)
	buffer.WriteString("#\n")
	for _, line := range strings.Split(strings.TrimRight(body.String(), "\n"), "\n") {
		if line == "" {
			buffer.WriteString("#\n")
			continue
		}
		buffer.WriteString("# " + line + "\n")
	}
	return buffer.Bytes(), nil
}
