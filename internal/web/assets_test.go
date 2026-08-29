package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// A request for one of the built files, with whatever headers the case is
// about. These do not go through do(): that one sends a JSON body and a
// content type, which is not what a browser fetching a script does.
func fetch(server *Server, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(""))
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	return response
}

// The name of a file the build actually produced. A checkout that has not run
// `make web` has none, and the test says so rather than passing on nothing.
func anAsset(t *testing.T) string {
	t.Helper()
	builtOnce.Do(func() { builtByPath = prepareBuilt() })
	// Chosen by name alone. Choosing one that had been compressed would mean
	// that switching compression off made these tests skip instead of fail,
	// which is how this helper was written the first time.
	for name := range builtByPath {
		if strings.HasPrefix(name, "assets/") && strings.HasSuffix(name, ".js") {
			return name
		}
	}
	t.Skip("no built interface in this checkout; run make web")
	return ""
}

// The whole reason for this file. The interface is 620 kilobytes of
// JavaScript and it was sent uncompressed, which over a screen's wireless is
// several seconds.
func TestBuiltFilesAreCompressed(t *testing.T) {
	server := newTestServer(t, &config.Configuration{})
	name := anAsset(t)

	response := fetch(server, http.MethodGet, "/"+name, map[string]string{
		"Accept-Encoding": "gzip",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("asking for %s answered %d", name, response.Code)
	}
	if encoding := response.Header().Get("Content-Encoding"); encoding != "gzip" {
		t.Fatalf("%s came back as %q, want gzip", name, encoding)
	}
	if vary := response.Header().Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
		t.Errorf("Vary is %q, and a cache needs to be told the encoding matters", vary)
	}

	// It has to be real gzip of the real file, not just a header.
	reader, err := gzip.NewReader(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatalf("the body is not gzip: %s", err)
	}
	unpacked, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("the body did not unpack: %s", err)
	}
	if !bytes.Equal(unpacked, builtByPath[name].raw) {
		t.Error("what unpacked is not the file")
	}
	if response.Body.Len() >= len(unpacked) {
		t.Errorf("compressed to %d bytes from %d, which is no saving",
			response.Body.Len(), len(unpacked))
	}
	if length := response.Header().Get("Content-Length"); length != strconv.Itoa(response.Body.Len()) {
		t.Errorf("Content-Length is %q and the body is %d bytes", length, response.Body.Len())
	}
}

// A client that cannot unpack it must still get the file.
func TestBuiltFilesArePlainWhenGzipIsNotOffered(t *testing.T) {
	server := newTestServer(t, &config.Configuration{})
	name := anAsset(t)

	response := fetch(server, http.MethodGet, "/"+name, nil)
	if encoding := response.Header().Get("Content-Encoding"); encoding != "" {
		t.Fatalf("sent %q to a client that never asked for it", encoding)
	}
	if !bytes.Equal(response.Body.Bytes(), builtByPath[name].raw) {
		t.Error("the plain answer is not the file")
	}
}

// A file embedded in the executable has no modification time, so net/http had
// nothing to put in Last-Modified and there was no ETag either: every visit
// downloaded the whole interface again.
func TestBuiltFilesCanBeRevalidated(t *testing.T) {
	server := newTestServer(t, &config.Configuration{})
	name := anAsset(t)

	first := fetch(server, http.MethodGet, "/"+name, map[string]string{"Accept-Encoding": "gzip"})
	tag := first.Header().Get("ETag")
	if tag == "" {
		t.Fatal("no ETag, so the browser has nothing to ask about")
	}

	again := fetch(server, http.MethodGet, "/"+name, map[string]string{
		"Accept-Encoding": "gzip",
		"If-None-Match":   tag,
	})
	if again.Code != http.StatusNotModified {
		t.Fatalf("holding the current file answered %d, want 304", again.Code)
	}
	if again.Body.Len() != 0 {
		t.Errorf("a 304 carried %d bytes", again.Body.Len())
	}

	// The compressed and the plain file are different answers and must not
	// share a tag, or something in between can hand back the wrong one.
	plain := fetch(server, http.MethodGet, "/"+name, nil)
	if plain.Header().Get("ETag") == tag {
		t.Error("the compressed and plain files answer with the same ETag")
	}
	if fetch(server, http.MethodGet, "/"+name, map[string]string{"If-None-Match": tag}).Code != http.StatusOK {
		t.Error("the plain file was withheld against the compressed file's tag")
	}
}

// The names under assets/ are hashes of what is in them, so they can be kept
// for ever. The page that names them cannot be kept at all.
func TestBuiltFilesSayHowLongToKeepThem(t *testing.T) {
	server := newTestServer(t, &config.Configuration{})
	name := anAsset(t)

	asset := fetch(server, http.MethodGet, "/"+name, nil).Header().Get("Cache-Control")
	if !strings.Contains(asset, "immutable") || !strings.Contains(asset, "max-age=31536000") {
		t.Errorf("%s is kept as %q, and a hashed name can be kept for ever", name, asset)
	}

	page := fetch(server, http.MethodGet, "/", nil).Header().Get("Cache-Control")
	if page != "no-store" {
		t.Errorf("the page is kept as %q; it names the hashed files and must never be stale", page)
	}

	// A page inside the interface is the same shell, and must be as fresh.
	deeper := fetch(server, http.MethodGet, "/network", nil)
	if deeper.Code != http.StatusOK {
		t.Fatalf("a page of the interface answered %d", deeper.Code)
	}
	if kept := deeper.Header().Get("Cache-Control"); kept != "no-store" {
		t.Errorf("/network is kept as %q", kept)
	}
}

// A browser, a proxy or a health check may ask for the headers alone. It used
// to be told the method was not allowed.
func TestBuiltFilesAnswerHead(t *testing.T) {
	server := newTestServer(t, &config.Configuration{})
	name := anAsset(t)

	for _, path := range []string{"/", "/" + name} {
		response := fetch(server, http.MethodHead, path, map[string]string{"Accept-Encoding": "gzip"})
		if response.Code != http.StatusOK {
			t.Errorf("HEAD %s answered %d", path, response.Code)
		}
		if response.Body.Len() != 0 {
			t.Errorf("HEAD %s carried %d bytes of body", path, response.Body.Len())
		}
		if response.Header().Get("Content-Length") == "" {
			t.Errorf("HEAD %s did not say how long the file is", path)
		}
	}
}

// Compressing a picture spends time to make it bigger.
func TestAlreadyCompressedFilesAreLeftAlone(t *testing.T) {
	if worthCompressing("image/png") || worthCompressing("video/mp4") {
		t.Error("a picture or a video is being compressed again")
	}
	if !worthCompressing("text/javascript; charset=utf-8") || !worthCompressing("image/svg+xml") {
		t.Error("something made of text is not being compressed")
	}
}
