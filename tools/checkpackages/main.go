// Command checkpackages verifies that every Debian package the image asks for
// exists for every architecture the image is published for.
//
// It exists because of a failure that would only have appeared at the moment
// of the first release. The image is published for amd64 and arm64, and two of
// the X drivers it installed — the Intel one and the VESA one — are built only
// for x86. The arm64 half of the release build would have stopped with
// "Unable to locate package", after everything else had already succeeded.
//
// Building for another architecture to find that out needs emulation and takes
// twenty minutes. Asking Debian's own package index takes ten seconds, so that
// is what this does: it reads the package lists out of Dockerfile and
// checks each one against the index for each architecture.
//
//	go run ./tools/checkpackages
package main

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// architectures are what the release workflow publishes. Keeping this list and
// the workflow's in step is the one thing this program cannot check itself.
var architectures = []string{"amd64", "arm64"}

// x86Only names the variable in the Dockerfile holding the packages that are
// deliberately x86-only, so that they are checked against x86 and expected to
// be absent elsewhere rather than reported as a fault.
const x86Only = "X86_PACKAGES"

func main() {
	dockerfile := flag.String("dockerfile", "Dockerfile", "the Dockerfile to read the package lists from")
	suite := flag.String("suite", "trixie", "the Debian release the image is built on")
	flag.Parse()

	if err := run(*dockerfile, *suite); err != nil {
		fmt.Fprintf(os.Stderr, "checkpackages: %s\n", err)
		os.Exit(1)
	}
}

func run(dockerfile, suite string) error {
	everywhere, onlyX86, err := readPackageLists(dockerfile)
	if err != nil {
		return err
	}
	if len(everywhere) == 0 {
		return fmt.Errorf("%s lists no packages; has the format changed?", dockerfile)
	}
	fmt.Printf("checking %d packages (and %d x86-only) against %s\n",
		len(everywhere), len(onlyX86), strings.Join(architectures, ", "))

	problems := 0
	for _, architecture := range architectures {
		available, err := index(suite, architecture)
		if err != nil {
			return err
		}

		var missing []string
		for _, name := range everywhere {
			if !available[name] {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)

		for _, name := range missing {
			fmt.Fprintf(os.Stderr, "  %s: %s is not built for this architecture\n", architecture, name)
			problems++
		}

		// The x86-only ones must be there on x86 — otherwise the name is
		// wrong or the package has gone — and are expected to be absent
		// elsewhere, which is the whole reason they are listed separately.
		if architecture == "amd64" {
			for _, name := range onlyX86 {
				if !available[name] {
					fmt.Fprintf(os.Stderr, "  %s: %s is listed as x86-only but is not there either\n", architecture, name)
					problems++
				}
			}
		} else {
			for _, name := range onlyX86 {
				if available[name] {
					fmt.Fprintf(os.Stderr, "  %s: %s is listed as x86-only but exists here; move it to the main list\n",
						architecture, name)
					problems++
				}
			}
		}

		fmt.Printf("  %s: %d packages available\n", architecture, len(available))
	}

	if problems > 0 {
		return fmt.Errorf("%d problem(s); the release build would fail", problems)
	}
	fmt.Println("every package is available for every architecture the image is published for")
	return nil
}

// readPackageLists pulls the two ENV lists out of the Dockerfile. Parsing it
// rather than repeating the list here is the point: a package added to the
// image is checked without anybody remembering to add it twice.
func readPackageLists(dockerfile string) ([]string, []string, error) {
	content, err := os.ReadFile(dockerfile)
	if err != nil {
		return nil, nil, err
	}

	everywhere := namesFrom(string(content), "PACKAGES")
	onlyX86 := namesFrom(string(content), x86Only)
	return everywhere, onlyX86, nil
}

// namesFrom finds `ENV <name>="..."`, following the backslash continuations
// the Dockerfile uses to keep the list readable.
func namesFrom(content, variable string) []string {
	pattern := regexp.MustCompile(`(?s)ENV ` + regexp.QuoteMeta(variable) + `="(.*?)"`)
	match := pattern.FindStringSubmatch(content)
	if match == nil {
		return nil
	}
	cleaned := strings.ReplaceAll(match[1], "\\", " ")
	cleaned = strings.ReplaceAll(cleaned, "\n", " ")

	return strings.Fields(cleaned)
}

// index fetches the package names Debian publishes for one architecture.
func index(suite, architecture string) (map[string]bool, error) {
	// gzip rather than the smaller xz, because Go decompresses gzip in the
	// standard library and this program is not worth a dependency.
	address := fmt.Sprintf("https://deb.debian.org/debian/dists/%s/main/binary-%s/Packages.gz",
		suite, architecture)

	client := &http.Client{Timeout: 3 * time.Minute}
	response, err := client.Get(address)
	if err != nil {
		return nil, fmt.Errorf("cannot fetch %s: %w", address, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", address, response.Status)
	}

	decompressed, err := gzip.NewReader(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", address, err)
	}
	defer func() { _ = decompressed.Close() }()

	available := map[string]bool{}
	scanner := bufio.NewScanner(decompressed)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if name, found := strings.CutPrefix(line, "Package: "); found {
			available[strings.TrimSpace(name)] = true
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("reading %s: %w", address, err)
	}
	return available, nil
}
