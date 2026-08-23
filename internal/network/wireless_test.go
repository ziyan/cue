package network

import (
	"strings"
	"testing"
)

// The reply wpa_supplicant gives to SCAN_RESULTS. Tab separated, one header
// line, and the same access point appearing more than once.
const scanReply = "bssid / frequency / signal level / flags / ssid\n" +
	"00:11:22:33:44:50\t2462\t-45\t[WPA2-PSK-CCMP][ESS]\tOffice\n" +
	"00:11:22:33:44:51\t5180\t-38\t[WPA2-PSK-CCMP][WPS][ESS]\tOffice\n" +
	"00:11:22:33:44:52\t2437\t-71\t[ESS]\tGuest\n" +
	"00:11:22:33:44:53\t2412\t-60\t[WPA2-EAP-CCMP][ESS]\tCorporate\n" +
	"00:11:22:33:44:54\t2412\t-30\t[WPA2-PSK-CCMP][ESS]\t\n"

func TestScanResultsAreOneEntryPerNameStrongestFirst(t *testing.T) {
	networks := parseScanResults(scanReply)

	var names []string
	for _, one := range networks {
		names = append(names, one.SSID)
	}
	if got, want := strings.Join(names, ","), "Office,Corporate,Guest"; got != want {
		t.Fatalf("networks were %q, want %q", got, want)
	}

	// Office appeared twice. The 5GHz radio is the stronger of the two, and
	// is the one an operator should be choosing.
	if networks[0].SignalStrength != -38 {
		t.Errorf("kept the weaker Office radio: %d dBm", networks[0].SignalStrength)
	}
	if networks[0].BSSID != "00:11:22:33:44:51" || networks[0].Frequency != 5180 {
		t.Errorf("kept the wrong Office radio: %+v", networks[0])
	}
}

func TestAHiddenNetworkIsNotOffered(t *testing.T) {
	// The last line of the reply has an empty name and the strongest signal
	// of the lot, so it would sort first if it were kept at all. There is
	// nothing an operator could pick from it.
	for _, one := range parseScanResults(scanReply) {
		if one.SSID == "" {
			t.Fatalf("a hidden network was offered: %+v", one)
		}
	}
}

func TestMalformedScanLinesAreSkippedRatherThanPanicking(t *testing.T) {
	reply := "bssid / frequency / signal level / flags / ssid\n" +
		"truncated\n" +
		"00:11:22:33:44:55\tnot-a-number\talso-not\t[ESS]\tOnly\n" +
		"\n"

	networks := parseScanResults(reply)
	if len(networks) != 1 {
		t.Fatalf("got %d networks, want 1: %+v", len(networks), networks)
	}
	if networks[0].SSID != "Only" || networks[0].Frequency != 0 || networks[0].SignalStrength != 0 {
		t.Errorf("unparseable numbers should read as zero, got %+v", networks[0])
	}
}

func TestSecurityIsWhatAnOperatorCanActuallyType(t *testing.T) {
	for _, one := range []struct {
		flags string
		want  string
	}{
		{"[WPA2-PSK-CCMP][ESS]", SecurityPreShared},
		{"[WPA3-SAE-CCMP][ESS]", SecurityPreShared},
		{"[WPA2-EAP-CCMP][ESS]", SecurityEnterprise},
		{"[WEP][ESS]", SecurityEnterprise},
		{"[ESS]", SecurityOpen},
		{"", SecurityOpen},
	} {
		if got := securityFrom(one.flags); got != one.want {
			t.Errorf("securityFrom(%q) = %q, want %q", one.flags, got, one.want)
		}
	}
}

func TestNetworkIdentifiersSkipTheHeader(t *testing.T) {
	reply := "network id / ssid / bssid / flags\n" +
		"0\tOffice\tany\t[CURRENT]\n" +
		"3\tGuest\tany\t\n"

	got := networkIdentifiers(reply)
	if len(got) != 2 || got[0] != "0" || got[1] != "3" {
		t.Fatalf("identifiers were %q, want [0 3]", got)
	}
	if identifiers := networkIdentifiers("network id / ssid / bssid / flags\n"); len(identifiers) != 0 {
		t.Errorf("an empty list read as %q", identifiers)
	}
}

func TestAQuotedValueCannotEndTheStringEarly(t *testing.T) {
	// The name of a wireless network is chosen by whoever runs it, and reaches
	// the supplicant's configuration as text. A name containing a quote must
	// not be able to become a second directive.
	for _, one := range []struct{ value, want string }{
		{`Office`, `"Office"`},
		{`Bob"s AP`, `"Bob\"s AP"`},
		{`back\slash`, `"back\\slash"`},
		{`"`, `"\""`},
		{``, `""`},
	} {
		if got := quote(one.value); got != one.want {
			t.Errorf("quote(%q) = %s, want %s", one.value, got, one.want)
		}
	}
}

func TestKeyValuesReadTheStatusReply(t *testing.T) {
	values := parseKeyValues("bssid=00:11:22:33:44:55\nssid=Office\nwpa_state=COMPLETED\nip_address=192.0.2.10\n\n")

	if values["ssid"] != "Office" || values["wpa_state"] != "COMPLETED" {
		t.Fatalf("status read as %v", values)
	}
	// A value can itself contain an equals sign; only the first one separates.
	values = parseKeyValues("key=a=b\n")
	if values["key"] != "a=b" {
		t.Errorf("only the first equals sign separates, got %q", values["key"])
	}
}
