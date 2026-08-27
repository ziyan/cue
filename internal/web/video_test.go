package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// upload sends a file the way a browser would.
func upload(t *testing.T, server *Server, name, mediaType string, content []byte, session *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="file"; filename="` + name + `"`}
	if mediaType != "" {
		header["Content-Type"] = []string{mediaType}
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/videos", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if session != nil {
		request.AddCookie(session)
	}
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	return response
}

func TestAVideoCanBeUploadedAndPlayedBack(t *testing.T) {
	server := newTestServer(t, config.Default())
	session := signedIn(t, server)

	content := bytes.Repeat([]byte("a test video's bytes"), 1000)
	response := upload(t, server, "promo.mp4", "video/mp4", content, session)
	if response.Code != http.StatusOK {
		t.Fatalf("uploading answered %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body.String(), "promo.mp4") {
		t.Errorf("the answer does not name the file: %s", response.Body)
	}

	videos, err := server.videos.List()
	if err != nil || len(videos) != 1 {
		t.Fatalf("the store holds %v (%v)", videos, err)
	}

	// And it comes back, with ranges, because a browser playing a video asks
	// for them and some players fail outright without.
	back := do(server, "GET", "/videos/"+videos[0].File, nil, session)
	if back.Code != http.StatusOK {
		t.Fatalf("fetching it answered %d", back.Code)
	}
	if !bytes.Equal(back.Body.Bytes(), content) {
		t.Error("what came back is not what went up")
	}
	if got := back.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("the video is served with Accept-Ranges %q, want bytes", got)
	}
	if got := back.Header().Get("Content-Type"); got != "video/mp4" {
		t.Errorf("the video is served as %q", got)
	}
}

// Anything that is not a video has to be refused. The screen would show a
// black rectangle and nobody would know why.
func TestSomethingThatIsNotAVideoIsRefused(t *testing.T) {
	server := newTestServer(t, config.Default())
	session := signedIn(t, server)

	response := upload(t, server, "notes.txt", "text/plain", []byte("not a test video"), session)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Errorf("uploading a text file answered %d, want 415", response.Code)
	}
	if videos, _ := server.videos.List(); len(videos) != 0 {
		t.Errorf("a text file was stored anyway: %v", videos)
	}
}

// One upload must not be able to fill the disk of a machine nobody logs into.
func TestAVideoLargerThanTheLimitIsRefused(t *testing.T) {
	configuration := config.Default()
	configuration.Playlist.MaximumVideoSize = 64
	server := newTestServer(t, configuration)
	session := signedIn(t, server)

	response := upload(t, server, "big.mp4", "video/mp4", bytes.Repeat([]byte("x"), 4096), session)
	if response.Code == http.StatusOK {
		t.Error("a video larger than the limit was accepted")
	}
	if videos, _ := server.videos.List(); len(videos) != 0 {
		t.Errorf("an oversized video was stored anyway: %v", videos)
	}
}

func TestUploadingNeedsASession(t *testing.T) {
	server := newTestServer(t, config.Default())

	// Before the device is set up there is no password to check against, and
	// the answer is that nothing is reachable at all.
	if code := upload(t, server, "promo.mp4", "video/mp4", []byte("a test video"), nil).Code; code != http.StatusForbidden {
		t.Errorf("uploading to a device that is not set up answered %d, want 403", code)
	}

	signedIn(t, server)

	if code := upload(t, server, "promo.mp4", "video/mp4", []byte("a test video"), nil).Code; code != http.StatusUnauthorized {
		t.Errorf("uploading without a session answered %d, want 401", code)
	}
	if videos, _ := server.videos.List(); len(videos) != 0 {
		t.Errorf("a video was stored by somebody with no session: %v", videos)
	}
}

// The browser on the device has no session and must still be able to play the
// video; anybody asking over the network must not.
func TestAVideoIsServedToThisMachineButNotToTheNetwork(t *testing.T) {
	server := newTestServer(t, config.Default())
	session := signedIn(t, server)

	content := []byte("a test video's bytes")
	response := upload(t, server, "promo.mp4", "video/mp4", content, session)
	if response.Code != http.StatusOK {
		t.Fatalf("uploading answered %d", response.Code)
	}
	videos, _ := server.videos.List()

	// httptest requests come from 192.0.2.1 by default, which is the
	// documentation range and stands in for somebody else on the network.
	fromNetwork := do(server, "GET", "/videos/"+videos[0].File, nil, nil)
	if fromNetwork.Code != http.StatusUnauthorized {
		t.Errorf("a video was served to the network without a session: %d", fromNetwork.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/videos/"+videos[0].File, nil)
	request.RemoteAddr = "127.0.0.1:54321"
	local := httptest.NewRecorder()
	server.router.ServeHTTP(local, request)
	if local.Code != http.StatusOK {
		t.Errorf("this machine's own browser was refused the video: %d", local.Code)
	}
}

func TestAVideoThatIsNotStoredIsNotFound(t *testing.T) {
	server := newTestServer(t, config.Default())
	session := signedIn(t, server)

	if code := do(server, "GET", "/videos/0123456789abcdef0123456789abcdef", nil, session).Code; code != http.StatusNotFound {
		t.Errorf("an unknown video answered %d, want 404", code)
	}
	// And a name that tries to walk out of the store.
	if code := do(server, "GET", "/videos/..%2f..%2fetc%2fshadow", nil, session).Code; code == http.StatusOK {
		t.Error("a name that walks out of the store was served")
	}
}

// signedIn sets the device up and returns the session that came back, which is
// what an operator's browser would be carrying.
func signedIn(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	response := do(server, http.MethodPost, "/api/v1/setup", map[string]string{
		"name": "Reception", "password": testPassword,
	}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("setting the device up answered %d: %s", response.Code, response.Body)
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("setting the device up issued no session")
	}
	return cookies[0]
}
