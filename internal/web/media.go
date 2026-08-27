package web

import (
	"fmt"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"

	"github.com/ziyan/cue/internal/media"
)

// Uploading pictures and videos, serving them back, and the page that shows
// one.
//
// They are kept on the device itself so that a screen goes on showing them
// with no network at all, which is the point: a promotional loop should not
// stop because a web server somewhere else did.

// uploadMedia takes a picture or a video the operator chose on their own
// machine.
//
// The body is read as a stream and not through ParseMultipartForm, which
// buffers the whole thing either into memory or into a temporary file of its
// own choosing. These are hundreds of megabytes and the device is small.
func (self *Server) uploadMedia(response http.ResponseWriter, request *http.Request) {
	if self.uploads == nil {
		writeError(response, http.StatusServiceUnavailable, "this device cannot store uploads")
		return
	}

	limit := self.store.Current().Playlist.MaximumUploadSize
	if limit <= 0 {
		limit = 4 << 30
	}
	if request.ContentLength > limit {
		writeError(response, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("that file is larger than the %s this device will accept",
				describeSize(limit)))
		return
	}

	reader, err := request.MultipartReader()
	if err != nil {
		writeError(response, http.StatusBadRequest, "that is not an upload")
		return
	}

	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}

		name := part.FileName()
		mediaType := part.Header.Get("Content-Type")
		if mediaType == "" || mediaType == "application/octet-stream" {
			// Some browsers say nothing useful about the type, so it is worked
			// out from the name instead.
			mediaType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		}
		if !media.Playable(mediaType) {
			_ = part.Close()
			writeError(response, http.StatusUnsupportedMediaType,
				fmt.Sprintf("%q is not a picture or a video this device can show", name))
			return
		}

		// The limit is applied to the bytes as well as to the declared length,
		// because a declared length is something a client says rather than
		// something it has proved.
		stored, err := self.uploads.Add(name, mediaType, http.MaxBytesReader(response, part, limit))
		_ = part.Close()
		if err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(response, http.StatusOK, stored)
		return
	}
	writeError(response, http.StatusBadRequest, "there was no file in that upload")
}

// serveMedia sends a stored file's bytes.
//
// http.ServeContent rather than io.Copy, because a browser playing a video
// asks for byte ranges -- to start, and again every time something seeks --
// and a server that ignores them gives some players a video they cannot play
// at all.
func (self *Server) serveMedia(response http.ResponseWriter, request *http.Request) {
	if self.uploads == nil {
		http.NotFound(response, request)
		return
	}

	file := mux.Vars(request)["file"]
	path, err := self.uploads.Path(file)
	if err != nil {
		http.NotFound(response, request)
		return
	}

	if details, err := self.uploads.Details(file); err == nil && details.Type != "" {
		response.Header().Set("Content-Type", details.Type)
	}
	// The name is a digest of the contents, so the contents can never change
	// under it. It is worth caching hard: the browser re-fetches the file
	// every time the item comes round otherwise.
	response.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(response, request, path)
}

// listMedia is what the interface shows when choosing among what is already
// uploaded.
func (self *Server) listMedia(response http.ResponseWriter, request *http.Request) {
	if self.uploads == nil {
		writeJSON(response, http.StatusOK, map[string]interface{}{"media": []interface{}{}})
		return
	}
	videos, err := self.uploads.List()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"media": videos})
}

// fromThisMachine reports whether a request came from the device itself.
//
// The browser showing the screen has no session and never will: it is a kiosk
// that nobody signs in on. It still has to be able to fetch the player page
// and the video, exactly as it fetches /welcome today. Anybody else is asking
// over the network for a file an operator uploaded, and that needs a password
// like everything else.
func fromThisMachine(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// localOrSession allows this machine's own browser through, and asks everybody
// else for a session.
func (self *Server) localOrSession(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if fromThisMachine(request) {
			next(response, request)
			return
		}
		if !self.isSetUp() {
			writeError(response, http.StatusForbidden, "this device has not been set up yet")
			return
		}
		if !self.hasSession(request) {
			writeError(response, http.StatusUnauthorized, "sign in first")
			return
		}
		next(response, request)
	}
}

func describeSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.0f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(bytes)/float64(1<<20))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
