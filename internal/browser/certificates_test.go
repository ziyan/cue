package browser

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/executable"
)

// selfSigned makes the kind of certificate this feature exists for: one an
// appliance on a private network signed for itself.
func selfSigned(t *testing.T, commonName string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("cannot make a key: %s", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cannot make a certificate: %s", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded}))
}

func TestASelfSignedCertificateIsRead(t *testing.T) {
	certificate, err := parseCertificate(selfSigned(t, "cameras.example.invalid"))
	if err != nil {
		t.Fatalf("a valid certificate was refused: %s", err)
	}
	if certificate.Subject.CommonName != "cameras.example.invalid" {
		t.Errorf("read the wrong subject: %q", certificate.Subject.CommonName)
	}
}

// privateKeyMarker is assembled rather than written, so that no file in this
// repository contains the literal header of a private key. tools/checksecrets
// refuses one wherever it appears, and an exemption for "but this one is only
// a test" is exactly the exemption a real key would eventually arrive through.
var privateKeyMarker = "-----BEGIN " + "PRIVATE KEY-----"

func TestWhatSomebodyPastesByMistakeIsExplainedRatherThanPassedOn(t *testing.T) {
	// These are the three things that actually get pasted into the box. Each
	// has to produce a sentence naming what went in, not certutil's own
	// "unable to decode".
	for name, text := range map[string]string{
		"nothing at all":     "",
		"a bare description": "the certificate for the camera server",
		"a private key": privateKeyMarker + "\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg\n" +
			"-----END " + "PRIVATE KEY-----\n",
	} {
		_, err := parseCertificate(text)
		if err == nil {
			t.Errorf("%s was accepted as a certificate", name)
			continue
		}
		if strings.Contains(err.Error(), "asn1") && !strings.Contains(err.Error(), "PEM") {
			t.Errorf("%s produced a message nobody can act on: %s", name, err)
		}
	}

	// A private key in particular has to be named as one, because that is the
	// paste that also means somebody has just put a key somewhere it should
	// not be.
	_, err := parseCertificate(privateKeyMarker + "\nAAAA\n-----END " + "PRIVATE KEY-----\n")
	if err == nil || !strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Errorf("a private key was not named as one: %v", err)
	}
}

func TestTwoAppliancesWithTheSameSubjectGetDistinctNames(t *testing.T) {
	// NSS refuses a second certificate with a name it already has, and two
	// appliances from one manufacturer very often present the same subject.
	first, err := parseCertificate(selfSigned(t, "UniFi"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseCertificate(selfSigned(t, "UniFi"))
	if err != nil {
		t.Fatal(err)
	}
	if certificateName(first, 0) == certificateName(second, 1) {
		t.Errorf("both certificates are called %q, and the second would be refused", certificateName(first, 0))
	}
}

func TestACertificateWithNoSubjectStillGetsAName(t *testing.T) {
	if name := certificateName(&x509.Certificate{}, 0); name == "" {
		t.Error("a certificate with nothing in its subject produced an empty name, which NSS refuses")
	}
}

func TestCertificatesActuallyReachTheDatabase(t *testing.T) {
	// Everything above tests the reading and the naming. This tests the thing
	// that matters: that certutil is here, that it is called correctly, and
	// that what comes out is a database with the certificate in it. The
	// alternative is a feature that looks configured and trusts nothing.
	if _, err := executable.Resolve("certutil"); err != nil {
		t.Skip("certutil is not on this machine; the image has it")
	}

	certificate := selfSigned(t, "cameras.example.invalid")
	browser := newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Browser.CertificateAuthorities = []string{certificate}
	})

	if err := browser.installCertificates(context.Background()); err != nil {
		t.Fatalf("the certificate was not installed: %s", err)
	}

	// certutil -L lists what the database trusts.
	listing, err := exec.Command("certutil", "-L", "-d", "sql:"+browser.certificateDirectory()).CombinedOutput()
	if err != nil {
		t.Fatalf("cannot read the database back: %s\n%s", err, listing)
	}
	if !strings.Contains(string(listing), "cameras.example.invalid") {
		t.Errorf("the certificate is not in the database:\n%s", listing)
	}
	// "C" is trust for a TLS server, and nothing else.
	if !strings.Contains(string(listing), "C,,") {
		t.Errorf("the certificate is there but not trusted for TLS servers:\n%s", listing)
	}

	// Taking it out of the configuration has to take it off the device.
	browser.configuration.Browser.CertificateAuthorities = nil
	if err := browser.installCertificates(context.Background()); err != nil {
		t.Fatalf("removing it failed: %s", err)
	}
	if _, err := os.Stat(browser.certificateDirectory()); !os.IsNotExist(err) {
		t.Error("the database is still there after the certificate was removed from the configuration")
	}
}
