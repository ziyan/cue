package config

import (
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// Writing only what somebody actually chose.
//
// The file used to be written out in full, every setting with its value,
// whether or not anybody had ever touched it. That makes a hundred-line file
// out of a device that was told two things, and it hides the two things: an
// operator reading it cannot tell what was decided here from what merely came
// with the version. It also freezes the defaults -- a device written out once
// keeps the old default for ever, because the file now says so, and changing
// the default in a later version has no effect on any machine that has ever
// saved its configuration.
//
// So a value that matches the default is left out. What remains is what
// somebody chose. cue.yaml.example, written beside it, shows the full shape
// with the defaults filled in, so nothing is hidden by being absent.

// alwaysKept are settings written even when they match the default, because
// they are not really defaults at all.
var alwaysKept = map[string]bool{
	// The identifier is generated once and is this device's name for itself
	// for ever. It matching anything by chance would be a disaster, and it is
	// the one thing in the file nobody should have to guess at.
	"device.identifier": true,
}

// withoutDefaults returns the configuration as plain maps with everything
// matching the default removed.
func (self *Configuration) withoutDefaults() (map[string]interface{}, error) {
	mine, err := asTree(self)
	if err != nil {
		return nil, err
	}

	baseline := Default()
	baseline.Normalize()
	theirs, err := asTree(baseline)
	if err != nil {
		return nil, err
	}

	return pruneDefaults(mine, theirs, ""), nil
}

// asTree turns a configuration into plain maps and slices, so that it can be
// compared and pruned without knowing anything about the types.
func asTree(configuration *Configuration) (map[string]interface{}, error) {
	content, err := yaml.Marshal(configuration)
	if err != nil {
		return nil, fmt.Errorf("config: cannot write the configuration: %w", err)
	}
	var tree map[string]interface{}
	if err := yaml.Unmarshal(content, &tree); err != nil {
		return nil, fmt.Errorf("config: cannot read back the configuration: %w", err)
	}
	return tree, nil
}

// pruneDefaults returns what is left of mine once everything matching theirs
// is removed, or nil when nothing is left.
//
// Maps are walked key by key. Everything else -- scalars, and lists -- is kept
// or dropped whole: a list that differs from the default at all is written
// entire, because a playlist with its third item missing would be worse than
// useless.
func pruneDefaults(mine, theirs map[string]interface{}, path string) map[string]interface{} {
	kept := map[string]interface{}{}

	for key, value := range mine {
		here := key
		if path != "" {
			here = path + "." + key
		}

		other, found := theirs[key]
		if alwaysKept[here] {
			kept[key] = value
			continue
		}
		if found && reflect.DeepEqual(value, other) {
			continue
		}

		// A subsection is pruned in turn, so that one changed setting does not
		// drag the whole section into the file.
		if branch, ok := value.(map[string]interface{}); ok {
			otherBranch, _ := other.(map[string]interface{})
			if otherBranch == nil {
				otherBranch = map[string]interface{}{}
			}
			if remaining := pruneDefaults(branch, otherBranch, here); remaining != nil {
				kept[key] = remaining
			}
			continue
		}

		kept[key] = value
	}

	if len(kept) == 0 {
		return nil
	}
	return kept
}
