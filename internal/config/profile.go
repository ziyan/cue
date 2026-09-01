package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Applying a profile: a sparse set of settings held by the service and applied
// to any number of devices.
//
// The rule is that the profile wins where it speaks and is silent elsewhere. A
// setting it names is authoritative, so a device that has drifted is corrected
// at the next poll -- which is what makes "these forty screens are alike" a
// fact rather than a hope. A setting it does not name is left exactly as the
// device has it, so a name, a network and an identity survive one profile
// applied to forty machines without the profile having to know they exist.
//
// The merge is destructive: the profile's values are written into the device's
// own file. So the device records which keys it took, in Service.ProfileKeys,
// because without that record nothing can ever be un-managed. Once a profile's
// value is in the file the previous one is gone, and a device would keep it for
// ever -- remove a setting from a profile and every screen it ever touched
// stays at that value, because the profile has stopped speaking and nothing is
// listening for the silence.

// refusedByProfile are the keys a profile may never set, whatever it says.
//
// The service strips these when it lifts a profile off a device, and this
// refuses them again on arrival. Both, deliberately: if the stripping is ever
// wrong, applying the result to forty screens gives them all one
// device.identifier -- which is the service's own primary key for a device, so
// forty screens become one. That is not a failure worth trusting a single
// implementation with.
//
// A prefix here refuses the key and everything under it.
var refusedByProfile = []string{
	// This device's name for itself, for ever, and the service's name for it
	// too.
	"device.identifier",

	// Everything about being linked: the address, the credential, the
	// account, and the poll interval that governs asking for the profile at
	// all. A profile that could set the last of those could stop a fleet
	// polling, and the only fix would be per-screen by hand -- which is the
	// situation profiles exist to abolish. The things that govern being
	// managed live inside the section that is never managed.
	"service",

	// The administrator's password and the key their session is signed with.
	"web.passwordHash",
	"web.sessionSecret",

	// Which interfaces this machine has, and how each one gets an address.
	//
	// Refused while a list is managed whole, which is what it is: a profile
	// naming this replaces the device's list outright, and a wireless
	// interface's passphrase lives inside it and never travels. So a profile
	// moving an office to a new SSID would leave every screen it touched with
	// an SSID and no key, unable to join, and the thing that would let
	// somebody fix that remotely is the network it has just lost. It is worse
	// than the wireless case suggests: replacing the list does not care what
	// is in it, so a profile carrying only a wired interface still deletes the
	// wireless one from every device that had it.
	//
	// The rest of the section is fine and is not refused -- manage,
	// onboarding, lostAfter and reconcileInterval are scalars, carry no
	// credential, and are genuinely the same sentence on every machine, which
	// is what a profile is for.
	//
	// Allowing interfaces would mean merging by name and keeping per-device
	// fields the profile does not mention, the way RestoreSecrets already
	// matches slices on Identifier or Name rather than on position. That is a
	// real design with its own questions -- what becomes of an interface the
	// device has and the profile does not name? -- and it should not arrive
	// as a side effect of "the network section can be managed".
	"network.interfaces",

	// Where state is kept, which is fixed by the image's layout.
	"paths",

	// Content, not settings. Without this a profile lifted from a device
	// would carry that device's whole playlist onto every screen it was
	// applied to -- a change of what a wall shows, arriving through a
	// mechanism for how it is configured. The playlist has a route of its
	// own and this must never become a second one.
	"playlist",
}

// ApplyProfile merges a profile into this configuration and records what it
// took, returning whether anything changed.
//
// The profile is the document as it arrived: plain maps, sparse, the same
// shape as the pruned file. An empty one is not nothing -- it releases
// everything this device was managed by, which is how a profile is unapplied.
func (self *Configuration) ApplyProfile(profile map[string]interface{}) (bool, error) {
	tree, err := asTree(self)
	if err != nil {
		return false, err
	}
	before, err := yaml.Marshal(tree)
	if err != nil {
		return false, err
	}

	taking := keysOf(profile, "")
	sort.Strings(taking)

	// Released first, so that a key which is both released and re-taken --
	// which cannot happen today but would be a silent bug if it ever did --
	// ends up taken rather than removed.
	for _, key := range self.Service.ProfileKeys {
		if contains(taking, key) {
			continue
		}
		removeAt(tree, strings.Split(key, "."))
	}

	for _, key := range taking {
		value, found := valueAt(profile, strings.Split(key, "."))
		if !found {
			continue
		}
		setAt(tree, strings.Split(key, "."), value)
	}

	after, err := yaml.Marshal(tree)
	if err != nil {
		return false, err
	}

	// Decoded onto the defaults, not onto a zero value, and this is the
	// difference between releasing a setting and destroying it. A released key
	// is absent from the tree; decoded onto a zero Configuration it would come
	// back as false or 0 rather than as whatever this version's default is, so
	// unapplying a profile that had once set browser.darkMode would leave the
	// screen light rather than returning it to the default. Absent means "the
	// device's own default" everywhere else in this file, and it has to mean
	// that here too.
	//
	// Decoding this way also means a profile naming a setting this version
	// does not have is ignored exactly as an unknown key in the file already
	// is, rather than refusing the whole document.
	merged := Default()
	if err := yaml.Unmarshal(after, merged); err != nil {
		return false, fmt.Errorf("config: the profile does not merge: %w", err)
	}

	*self = *merged
	self.Service.ProfileKeys = taking
	if len(taking) == 0 {
		self.Service.ProfileKeys = nil
	}
	return string(before) != string(after), nil
}

// keysOf flattens a profile into dotted paths.
//
// A map is descended into; anything else is a leaf. That includes sequences,
// on purpose and following pruneDefaults, which descends into maps only: a
// list is managed whole. display.outputs, playlist.items and
// network.interfaces are each one key, because half a managed list is not a
// state anybody can reason about, and there is no sensible way to address an
// element by a dotted path.
func keysOf(node map[string]interface{}, path string) []string {
	var keys []string
	for key, value := range node {
		here := key
		if path != "" {
			here = path + "." + key
		}
		if refusesProfile(here) {
			continue
		}
		if branch, ok := value.(map[string]interface{}); ok && len(branch) > 0 {
			keys = append(keys, keysOf(branch, here)...)
			continue
		}
		keys = append(keys, here)
	}
	return keys
}

// refusesProfile reports whether a key, or a section above it, may never be
// set by a profile.
func refusesProfile(key string) bool {
	for _, refused := range refusedByProfile {
		if key == refused || strings.HasPrefix(key, refused+".") {
			return true
		}
	}
	return secretPaths()[key]
}

// secretPaths is every place in the configuration a Secret can be, as dotted
// key paths, asked of the schema rather than remembered in a list.
//
// A list falls behind the struct, and the consequence for a secret is the
// worst kind. Every Secret serialises as the redaction mask in JSON, so a
// profile lifted from a device carries "********" and applying it writes that
// literal string on every screen it touches: for a wireless passphrase, a
// fleet off the air; for the VNC password, a working password that anybody who
// has read this repository knows, on a service that hands over control of the
// screen. That last one was reachable by a profile until this existed.
//
// A secret inside a list makes the whole list refused, because a list is
// managed whole -- there is no taking the parts of playlist.items that are not
// a password.
func secretPaths() map[string]bool {
	paths := map[string]bool{}
	collectSecretPaths(reflect.TypeOf(Configuration{}), "", paths)
	return paths
}

// collectSecretPaths walks a struct and reports whether a Secret was found
// anywhere beneath it.
func collectSecretPaths(structure reflect.Type, prefix string, into map[string]bool) bool {
	if structure.Kind() == reflect.Pointer {
		structure = structure.Elem()
	}
	if structure.Kind() != reflect.Struct {
		return false
	}

	found := false
	for index := range structure.NumField() {
		field := structure.Field(index)
		name := yamlName(field)
		if name == "" {
			continue
		}
		here := name
		if prefix != "" {
			here = prefix + "." + name
		}

		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}

		switch {
		case fieldType == reflect.TypeOf(Secret("")):
			into[here] = true
			found = true

		case fieldType.Kind() == reflect.Slice:
			// A list is one key. If anything inside it is a secret, the key is
			// the list, and the list is refused entire.
			if collectSecretPaths(fieldType.Elem(), here, map[string]bool{}) {
				into[here] = true
				found = true
			}

		case fieldType.Kind() == reflect.Struct:
			if collectSecretPaths(fieldType, here, into) {
				found = true
			}
		}
	}
	return found
}

// yamlName is the key a field is written under, or "" for one that is not
// written at all.
func yamlName(field reflect.StructField) string {
	tag := field.Tag.Get("yaml")
	if tag == "-" {
		return ""
	}
	if comma := strings.Index(tag, ","); comma >= 0 {
		tag = tag[:comma]
	}
	if tag == "" {
		return strings.ToLower(field.Name)
	}
	return tag
}

func valueAt(node map[string]interface{}, path []string) (interface{}, bool) {
	for index, step := range path {
		value, found := node[step]
		if !found {
			return nil, false
		}
		if index == len(path)-1 {
			return value, true
		}
		branch, ok := value.(map[string]interface{})
		if !ok {
			return nil, false
		}
		node = branch
	}
	return nil, false
}

func setAt(node map[string]interface{}, path []string, value interface{}) {
	for index, step := range path {
		if index == len(path)-1 {
			node[step] = value
			return
		}
		branch, ok := node[step].(map[string]interface{})
		if !ok {
			branch = map[string]interface{}{}
			node[step] = branch
		}
		node = branch
	}
}

// removeAt deletes a key, and any section it leaves empty behind it. An empty
// section written back into the file would be a section somebody chose, which
// is not what a released setting means.
func removeAt(node map[string]interface{}, path []string) {
	if len(path) == 0 {
		return
	}
	if len(path) == 1 {
		delete(node, path[0])
		return
	}
	branch, ok := node[path[0]].(map[string]interface{})
	if !ok {
		return
	}
	removeAt(branch, path[1:])
	if len(branch) == 0 {
		delete(node, path[0])
	}
}

func contains(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}
