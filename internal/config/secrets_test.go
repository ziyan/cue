package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Every secret in the configuration survives being saved back redacted.
//
// Found by walking the type rather than by listing what to check, so a Secret
// added anywhere in the tree tomorrow is covered by this test on the day it is
// added. That is the whole reason the restoring is a walk as well: the
// wireless passphrase was a Secret for months, was not on the list of things
// to restore, and so any save from any page replaced a device's wifi password
// with the placeholder and took it off the network at the next reconcile.
//
// The failure had no symptom at the point it happened. The code compiled, the
// save succeeded, the interface said "Saved.", and the credential was gone.
func TestEverySecretSurvivesBeingSavedBackRedacted(t *testing.T) {
	previous := Default()
	planted := map[string]string{}
	plantSecrets(t, reflect.ValueOf(previous).Elem(), "", planted, 0)

	if len(planted) == 0 {
		t.Fatal("no secrets were found in the configuration, so this test checks nothing")
	}
	t.Logf("checking %d secrets", len(planted))

	// What the interface is served and posts straight back: every secret is
	// the placeholder, and something unrelated has been edited.
	served := previous.Clone()
	redactSecrets(reflect.ValueOf(served).Elem())
	served.Device.Location = "somewhere else"

	RestoreSecrets(served, previous)

	found := map[string]string{}
	readSecrets(reflect.ValueOf(served).Elem(), "", found)
	for where, want := range planted {
		got, present := found[where]
		if !present {
			t.Errorf("%s went missing entirely", where)
			continue
		}
		if got != want {
			t.Errorf("%s came back as %q, want %q", where, got, want)
		}
	}
}

// A list can be reordered in the same request that saves it, so secrets are
// matched by what a thing is called and never by where it sits.
func TestSecretsFollowTheirOwnerWhenAListIsReordered(t *testing.T) {
	previous := Default()
	previous.Playlist.Items = []Item{
		{Identifier: "first", URL: "https://example.com/one",
			Login: &Login{Username: "one", Password: Secret("password one")}},
		{Identifier: "second", URL: "https://example.com/two",
			Login: &Login{Username: "two", Password: Secret("password two")}},
	}

	served := previous.Clone()
	// Turned around, and both passwords redacted, which is exactly what the
	// interface posts after somebody drags one item above another.
	served.Playlist.Items[0], served.Playlist.Items[1] = served.Playlist.Items[1], served.Playlist.Items[0]
	served.Playlist.Items[0].Login.Password = redacted
	served.Playlist.Items[1].Login.Password = redacted

	RestoreSecrets(served, previous)

	for _, item := range served.Playlist.Items {
		want := "password one"
		if item.Identifier == "second" {
			want = "password two"
		}
		if got := item.Login.Password.Reveal(); got != want {
			t.Errorf("%s ended up with %q, want %q -- a secret moved to another item",
				item.Identifier, got, want)
		}
	}
}

// The same, for the list that started this: interfaces are matched by name.
func TestAWirelessPassphraseFollowsItsInterface(t *testing.T) {
	previous := Default()
	previous.Network.Interfaces = []Interface{
		{Name: "wlp4s0", Method: "dhcp",
			Wireless: &Wireless{SSID: "one", Passphrase: Secret("passphrase one")}},
		{Name: "wlp5s0", Method: "dhcp",
			Wireless: &Wireless{SSID: "two", Passphrase: Secret("passphrase two")}},
	}

	served := previous.Clone()
	served.Network.Interfaces[0], served.Network.Interfaces[1] =
		served.Network.Interfaces[1], served.Network.Interfaces[0]
	served.Network.Interfaces[0].Wireless.Passphrase = redacted
	served.Network.Interfaces[1].Wireless.Passphrase = redacted

	RestoreSecrets(served, previous)

	for _, one := range served.Network.Interfaces {
		want := "passphrase one"
		if one.Name == "wlp5s0" {
			want = "passphrase two"
		}
		if got := one.Wireless.Passphrase.Reveal(); got != want {
			t.Errorf("%s ended up with %q, want %q", one.Name, got, want)
		}
	}
}

// plantSecrets fills in every Secret with a value naming where it is, creating
// whatever pointers and list entries are needed to reach one.
func plantSecrets(t *testing.T, value reflect.Value, path string, planted map[string]string, depth int) {
	t.Helper()
	// A depth cap rather than a cycle check: this fills a configuration in as
	// it walks, and a type reachable from itself would otherwise be built out
	// for ever. Nothing real is anywhere near this deep.
	if depth > 12 {
		return
	}

	if value.Type() == secretType {
		if !value.CanSet() {
			return
		}
		secret := "secret at " + path
		value.Set(reflect.ValueOf(Secret(secret)))
		planted[path] = secret
		return
	}

	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			if !value.CanSet() || !containsSecret(value.Type().Elem(), nil) {
				return
			}
			value.Set(reflect.New(value.Type().Elem()))
		}
		plantSecrets(t, value.Elem(), path, planted, depth+1)
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if !field.IsExported() {
				continue
			}
			plantSecrets(t, value.Field(index), path+"."+field.Name, planted, depth+1)
		}
	case reflect.Slice:
		if !containsSecret(value.Type().Elem(), nil) {
			return
		}
		// One entry is enough to reach the secrets inside, and it needs a name
		// so that restoring can match it.
		if value.Len() == 0 {
			if !value.CanSet() {
				return
			}
			value.Set(reflect.MakeSlice(value.Type(), 1, 1))
		}
		for index := 0; index < value.Len(); index++ {
			element := value.Index(index)
			nameElement(element, fmt.Sprintf("%s-%d", strings.ReplaceAll(path, ".", "-"), index))
			plantSecrets(t, element, fmt.Sprintf("%s[%d]", path, index), planted, depth+1)
		}
	}
}

// nameElement gives a list entry something to be matched by, if it has a field
// for one and it is empty.
func nameElement(element reflect.Value, name string) {
	for element.Kind() == reflect.Pointer {
		if element.IsNil() {
			return
		}
		element = element.Elem()
	}
	if element.Kind() != reflect.Struct {
		return
	}
	for _, field := range []string{"Identifier", "Name"} {
		value := element.FieldByName(field)
		if value.IsValid() && value.Kind() == reflect.String && value.CanSet() && value.String() == "" {
			value.SetString(name)
			return
		}
	}
}

func redactSecrets(value reflect.Value) {
	if value.Type() == secretType {
		if value.CanSet() {
			value.Set(reflect.ValueOf(Secret(redacted)))
		}
		return
	}
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			redactSecrets(value.Elem())
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if value.Type().Field(index).IsExported() {
				redactSecrets(value.Field(index))
			}
		}
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			redactSecrets(value.Index(index))
		}
	}
}

func readSecrets(value reflect.Value, path string, into map[string]string) {
	if value.Type() == secretType {
		into[path] = string(value.Interface().(Secret))
		return
	}
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			readSecrets(value.Elem(), path, into)
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if field.IsExported() {
				readSecrets(value.Field(index), path+"."+field.Name, into)
			}
		}
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			readSecrets(value.Index(index), fmt.Sprintf("%s[%d]", path, index), into)
		}
	}
}

// containsSecret reports whether a type has a Secret anywhere inside it, so
// that planting does not build out parts of the tree that hold none.
func containsSecret(kind reflect.Type, seen map[reflect.Type]bool) bool {
	if seen == nil {
		seen = map[reflect.Type]bool{}
	}
	if seen[kind] {
		return false
	}
	seen[kind] = true

	if kind == secretType {
		return true
	}
	switch kind.Kind() {
	case reflect.Pointer, reflect.Slice:
		return containsSecret(kind.Elem(), seen)
	case reflect.Struct:
		for index := 0; index < kind.NumField(); index++ {
			if kind.Field(index).IsExported() && containsSecret(kind.Field(index).Type, seen) {
				return true
			}
		}
	}
	return false
}
