package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// The built interface, ready to send.
//
// It used to be handed to http.FileServerFS straight off the embedded
// filesystem, and that was slow in two ways that only show up away from a
// desk. Nothing was compressed, so a phone fetched 620 kilobytes of
// JavaScript where 190 would do. And a file embedded in the executable has no
// modification time, so net/http had nothing to put in Last-Modified and the
// browser had nothing to ask about -- there was no ETag either, so every
// visit downloaded the whole interface again. Over a screen's wireless that
// is several seconds, every time, for bytes the browser already had.
//
// The contents are fixed at build time, so all of this is worked out once and
// kept: the bytes, the compressed bytes, and a tag to answer with.
type builtFile struct {
	contentType string
	raw         []byte
	// Empty when compressing did not pay for itself, which is the case for
	// anything already compressed.
	gzipped []byte
	tag     string
	// The tag for the compressed form. A cache that has been told to Vary on
	// the encoding still should not be able to confuse the two.
	gzippedTag string
}

var builtOnce sync.Once
var builtByPath map[string]*builtFile

// Only what is worth the processor time. Everything the interface is made of
// is text; a picture or a font that arrives later is already compressed, and
// gzipping it again spends time to make it bigger.
func worthCompressing(contentType string) bool {
	for _, kind := range []string{
		"text/", "application/javascript", "text/javascript",
		"application/json", "image/svg+xml", "application/manifest+json",
	} {
		if strings.HasPrefix(contentType, kind) {
			return true
		}
	}
	return false
}

func tagFor(content []byte) string {
	sum := sha256.Sum256(content)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// Read once, on the first request, rather than at startup: a daemon that
// never serves a page should not spend the time, and a build with no
// interface in it should not be an error until somebody asks for one.
func prepareBuilt() map[string]*builtFile {
	prepared := map[string]*builtFile{}

	content, err := fs.Sub(builtFiles, "dist")
	if err != nil {
		return prepared
	}

	_ = fs.WalkDir(content, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(content, name)
		if err != nil {
			return nil
		}

		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = http.DetectContentType(raw)
		}

		file := &builtFile{contentType: contentType, raw: raw, tag: tagFor(raw)}

		if worthCompressing(contentType) {
			var squeezed bytes.Buffer
			writer, err := gzip.NewWriterLevel(&squeezed, gzip.BestCompression)
			if err == nil {
				if _, err := writer.Write(raw); err == nil && writer.Close() == nil {
					// A saving of a tenth or less is not worth a second copy
					// in memory, nor the decompressing at the other end.
					if squeezed.Len() < len(raw)-len(raw)/10 {
						file.gzipped = squeezed.Bytes()
						file.gzippedTag = tagFor(file.gzipped)
					}
				}
			}
		}

		prepared[name] = file
		return nil
	})

	return prepared
}

func builtFileAt(name string) *builtFile {
	builtOnce.Do(func() { builtByPath = prepareBuilt() })
	return builtByPath[name]
}

// How long the browser may keep it without asking.
//
// Everything under assets/ is named after a hash of what is in it, so a
// changed file is a changed name and the old name can be kept for ever. The
// page itself names those files, so it must never be stale. Anything else --
// the icon -- may be kept but is checked, which costs one small request and
// no bytes when it has not changed.
func howLongToKeep(name string) string {
	switch {
	case name == "index.html":
		return "no-store"
	case strings.HasPrefix(name, "assets/"):
		return "public, max-age=31536000, immutable"
	default:
		return "public, max-age=0, must-revalidate"
	}
}

func (self *builtFile) send(response http.ResponseWriter, request *http.Request, name string) {
	body, tag := self.raw, self.tag
	if len(self.gzipped) > 0 && strings.Contains(request.Header.Get("Accept-Encoding"), "gzip") {
		body, tag = self.gzipped, self.gzippedTag
		response.Header().Set("Content-Encoding", "gzip")
	}
	if len(self.gzipped) > 0 {
		// Said whether or not this answer is the compressed one: it is what
		// tells anything in between that the two are different answers to the
		// same address.
		response.Header().Set("Vary", "Accept-Encoding")
	}

	response.Header().Set("Content-Type", self.contentType)
	response.Header().Set("Cache-Control", howLongToKeep(name))
	response.Header().Set("ETag", tag)

	// The browser saying which one it already has. If it is this one, it is
	// told so and nothing is sent.
	for _, held := range strings.Split(request.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(held) == tag {
			response.WriteHeader(http.StatusNotModified)
			return
		}
	}

	response.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if request.Method == http.MethodHead {
		response.WriteHeader(http.StatusOK)
		return
	}
	_, _ = response.Write(body)
}
