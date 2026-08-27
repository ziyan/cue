package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

func serverWithVideoItem(t *testing.T, sound bool) (*Server, config.Item) {
	t.Helper()
	item := config.Item{
		Identifier: "promo",
		Title:      "The promo",
		Video:      &config.ItemVideo{File: "0123456789abcdef0123456789abcdef", Name: "promo.mp4", Sound: sound},
	}
	configuration := config.Default()
	configuration.Playlist.Items = []config.Item{item}
	return newTestServer(t, configuration), item
}

func player(t *testing.T, server *Server, identifier string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/play/"+identifier, nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	return response
}

// The page has to be one video and nothing else. Anything visible around it is
// visible from across a room and reads as part of the picture.
func TestThePlayerIsOneVideoFillingTheScreen(t *testing.T) {
	server, item := serverWithVideoItem(t, false)

	body := player(t, server, item.Identifier).Body.String()

	if !strings.Contains(body, `src="/videos/`+item.Video.File+`"`) {
		t.Error("the page does not point at the stored video")
	}
	for _, wanted := range []string{"autoplay", "playsinline", "object-fit: contain"} {
		if !strings.Contains(body, wanted) {
			t.Errorf("the page does not use %q", wanted)
		}
	}
	if strings.Contains(body, "controls") {
		t.Error("the video has playback controls, which would be on the wall")
	}
}

// Sound is off unless the item asks for it, because a screen that starts
// making noise is a bad surprise and the person who added the video may not be
// in the room.
func TestAVideoIsSilentUnlessTheItemAsksForSound(t *testing.T) {
	silent, item := serverWithVideoItem(t, false)
	if body := player(t, silent, item.Identifier).Body.String(); !strings.Contains(body, " muted") {
		t.Error("a video with sound switched off is not muted")
	}

	loud, item := serverWithVideoItem(t, true)
	if body := player(t, loud, item.Identifier).Body.String(); strings.Contains(body, " muted") {
		t.Error("a video asked to play with sound is muted anyway")
	}
}

// The page must ask the daemon to move on when the video ends, because that is
// the only thing that knows when it did.
func TestThePlayerMovesTheScreenOnWhenTheVideoEnds(t *testing.T) {
	server, item := serverWithVideoItem(t, false)

	body := player(t, server, item.Identifier).Body.String()
	for _, wanted := range []string{`"ended"`, "/api/v1/playlist/next", `"error"`} {
		if !strings.Contains(body, wanted) {
			t.Errorf("the page does not handle %q", wanted)
		}
	}
	// And a backstop, or a video that neither ends nor fails holds the screen
	// for ever.
	if !strings.Contains(body, "setTimeout") {
		t.Error("the page has no backstop for a video that never finishes")
	}
}

func TestPlayingSomethingThatIsNotAVideoItemIsNotFound(t *testing.T) {
	server, _ := serverWithVideoItem(t, false)

	if code := player(t, server, "nothing-by-that-name").Code; code != http.StatusNotFound {
		t.Errorf("an unknown item answered %d, want 404", code)
	}
}

// The player is for this device's own screen. Anybody else asking over the
// network is asking about an operator's content and needs a password.
func TestThePlayerIsNotServedToTheNetworkWithoutASession(t *testing.T) {
	server, item := serverWithVideoItem(t, false)
	signedIn(t, server)

	if code := do(server, "GET", "/play/"+item.Identifier, nil, nil).Code; code != http.StatusUnauthorized {
		t.Errorf("the player answered the network with %d, want 401", code)
	}
}
