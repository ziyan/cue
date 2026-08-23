package browser

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ziyan/cue/internal/util/executable"
)

// Trusting a certificate, rather than trusting nothing.
//
// The appliances these screens are pointed at — a camera recorder, a building
// controller, a switch — serve a certificate signed by nobody, and a browser
// refuses the page. The usual answer is --ignore-certificate-errors, which
// this daemon also offers, and which is a bad answer: it does not trust that
// appliance, it stops checking anything, on every page, for the life of the
// process. A device on a network where that is switched on cannot tell its
// dashboard from anything else that answers on the same address.
//
// The answer here is the one the browser is built for. Chromium on Linux reads
// user-added roots out of an NSS database in the browser's own home directory,
// so a specific certificate can be trusted and everything else goes on being
// checked. It costs an operator one paste of a PEM block, which is what the
// Screen page asks for.

// certificateDirectory is where Chromium looks. The path is not a choice: it
// is compiled into the browser.
func (self *Browser) certificateDirectory() string {
	return filepath.Join(self.profileParent(), ".pki", "nssdb")
}

// installCertificates puts the configured certificates into the browser's NSS
// database, replacing whatever was there. Replacing rather than adding is what
// makes the configuration the truth: a certificate removed from the file is
// removed from the device, and does not go on being trusted because it was
// trusted once.
func (self *Browser) installCertificates(ctx context.Context) error {
	authorities := self.configuration.Browser.CertificateAuthorities
	directory := self.certificateDirectory()

	if len(authorities) == 0 {
		// Nothing configured. An empty database is left rather than removed,
		// so that turning the setting off does not leave the browser
		// trusting what it was told to forget.
		if err := os.RemoveAll(directory); err != nil {
			return fmt.Errorf("browser: cannot empty %s: %w", directory, err)
		}
		return nil
	}

	certutil, err := executable.Resolve("certutil")
	if err != nil {
		return fmt.Errorf("browser: %d certificate(s) are configured but certutil is not "+
			"in this image, so none of them can be trusted: %w", len(authorities), err)
	}

	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("browser: cannot empty %s: %w", directory, err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("browser: cannot create %s: %w", directory, err)
	}

	// An empty password, because the only thing this database holds is public
	// certificates and the browser has to open it without being asked.
	if err := run(ctx, certutil, "-N", "--empty-password", "-d", "sql:"+directory); err != nil {
		return fmt.Errorf("browser: cannot create the certificate database: %w", err)
	}

	for index, authority := range authorities {
		if err := self.installOneCertificate(ctx, certutil, directory, index, authority); err != nil {
			// One bad certificate must not stop the others being installed,
			// and must not stop the browser starting: a screen showing a
			// certificate warning is better than a screen showing nothing.
			log.Errorf("%s", err)
		}
	}
	return nil
}

func (self *Browser) installOneCertificate(ctx context.Context, certutil, directory string, index int, authority string) error {
	certificate, err := parseCertificate(authority)
	if err != nil {
		return fmt.Errorf("browser: certificate %d cannot be trusted: %w", index+1, err)
	}

	name := certificateName(certificate, index)
	filename := filepath.Join(directory, fmt.Sprintf("cue-%d.pem", index))
	if err := os.WriteFile(filename, []byte(authority), 0o644); err != nil {
		return fmt.Errorf("browser: cannot write certificate %d: %w", index+1, err)
	}
	defer func() { _ = os.Remove(filename) }()

	// "C,," trusts it for TLS servers and for nothing else — not for signing
	// email, not for signing code.
	if err := run(ctx, certutil, "-A", "-d", "sql:"+directory,
		"-t", "C,,", "-n", name, "-i", filename); err != nil {
		return fmt.Errorf("browser: cannot trust %q: %w", name, err)
	}

	if time.Now().After(certificate.NotAfter) {
		log.Warningf("the certificate %q expired on %s, so the browser will refuse it anyway",
			name, certificate.NotAfter.Format("2006-01-02"))
	} else {
		log.Noticef("trusting the certificate %q until %s", name, certificate.NotAfter.Format("2006-01-02"))
	}
	return nil
}

// parseCertificate reads one PEM block and checks it really is a certificate.
// Somebody pasting into a web form pastes the wrong thing often enough that
// the difference between "this is a private key" and "certutil: unable to
// decode" is worth the few lines.
func parseCertificate(text string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(text)))
	if block == nil {
		return nil, fmt.Errorf("this is not a PEM block: it should start with -----BEGIN CERTIFICATE-----")
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("this is a %q block, not a certificate", block.Type)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("this is a PEM block but not a usable certificate: %w", err)
	}
	return certificate, nil
}

// certificateName is what the certificate is called in the database and in the
// log. NSS needs the names to be distinct, so the position is appended: two
// appliances from the same manufacturer often present the same subject.
func certificateName(certificate *x509.Certificate, index int) string {
	name := certificate.Subject.CommonName
	if name == "" && len(certificate.Subject.Organization) > 0 {
		name = certificate.Subject.Organization[0]
	}
	if name == "" && len(certificate.DNSNames) > 0 {
		name = certificate.DNSNames[0]
	}
	if name == "" {
		name = "certificate"
	}
	return fmt.Sprintf("%s (%d)", name, index+1)
}

func run(ctx context.Context, path string, arguments ...string) error {
	command := exec.CommandContext(ctx, path, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		if message := strings.TrimSpace(string(output)); message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}
	return nil
}
