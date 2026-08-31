package web

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// The control has to be on whatever is on the screen, because the situations
// it exists for are the ones where nothing else can be reached.
func TestTheWayBackIsOnEveryPageTheScreenShows(t *testing.T) {
	server, item := serverWithVideoItem(t, false)

	if body := player(t, server, item.Identifier).Body.String(); !strings.Contains(body, "__cueWayBack") {
		t.Error("the player page does not carry the way back")
	}

	welcome := do(server, "GET", "/welcome", nil, nil).Body.String()
	if !strings.Contains(welcome, "__cueWayBack") {
		t.Error("the welcome page does not carry the way back")
	}
}

// It must be hidden until somebody is there, for the same reason the mouse
// cursor is: a wall display with a button permanently on it has that button in
// every photograph of it.
func TestTheWayBackIsHiddenUntilSomebodyIsThere(t *testing.T) {
	script := newTestServer(t, config.Default()).WayBackScript()

	if !strings.Contains(script, "opacity:0") {
		t.Error("the control starts visible")
	}
	for _, sign := range []string{"mousemove", "touchstart", "keydown"} {
		if !strings.Contains(script, sign) {
			t.Errorf("the control does not appear on %s", sign)
		}
	}
	if !strings.Contains(script, "setTimeout(hide") {
		t.Error("the control never hides itself again")
	}
}

// A screen in a lobby must not be resettable by one stray click.
// The mark itself can do nothing at all. Everything it leads to lives in a
// page this daemon served, because this script runs inside whatever is on the
// screen -- usually somebody else's page -- and a page from somewhere else may
// not act on this device however it got here.
func TestTheMarkItselfCanDoNothing(t *testing.T) {
	script := newTestServer(t, config.Default()).WayBackScript()

	for _, forbidden := range []string{"/api/v1/wireless/reset", "/api/v1/menu/restart", "/api/v1/playlist/next"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("the script injected into other people's pages calls %s itself", forbidden)
		}
	}
	if !strings.Contains(script, "/menu") {
		t.Error("the mark does not open the menu")
	}
	if !strings.Contains(script, "location.href = MENU") {
		t.Error("the mark does not send this tab to the menu")
	}
	// Not a tab of its own either, which was the second attempt.
	//
	// The daemon knows the tabs it opened and nothing about one a page opens
	// for itself. It swept that tab up as a stray window; and when the tab
	// closed itself, the browser stopped answering the daemon at all --
	// Runtime.evaluate timing out, the watchdog escalating, and a display
	// frozen on a wall. Navigating a tab the daemon already owns creates and
	// destroys nothing, so its idea of its own tabs never stops being true.
	if strings.Contains(script, "window.open") {
		t.Error("the menu opens a tab the daemon does not own again, which wedged the browser")
	}

	// Not a frame, and this is the interesting half.
	//
	// As a frame the menu was a subresource of whatever page the screen was
	// showing, fetched from an address on the local network -- so Chrome asked
	// the viewer to approve "do you want to allow <that site> to access local
	// network". On a wall display there is nobody to answer it, and the menu
	// never appeared. It also sat inside a tab the playlist rotates, so it
	// could be moved out from under somebody reading it.
	if strings.Contains(script, "iframe") {
		t.Error("the menu is opened in a frame again, which asks the viewer of a wall display " +
			"for permission to reach the local network")
	}
}

// The menu asks before anything that takes the screen away.
func TestTheMenuAsksBeforeTheDisruptiveThings(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	for _, asked := range []string{
		"Restart the browser?",
		"Restart the screen?",
		"Forget this wireless network and show the setup code?",
	} {
		if !strings.Contains(body, asked) {
			t.Errorf("the menu does not ask %q", asked)
		}
	}
}

// The menu changes the network and the picture, and nothing else.
//
// Those two earn their exception by being the ones that cannot sensibly be
// done from a chair. Reaching the web interface is exactly what the network
// settings are for -- a screen on a wired network with no DHCP server needs a
// fixed address, and without this that means somebody with a laptop, a cable
// and a way in. And whether the picture is the right size or on its side is a
// question you answer by looking at the screen, which is where this is.
//
// Everything else -- the playlist, the timezone, the password -- stays in the
// web interface, where there is room to think about it.
func TestTheMenuChangesTheNetworkThePictureAndNothingElse(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	// The whole configuration is not reachable from here.
	if strings.Contains(body, "/api/v1/configuration") {
		t.Error("the menu can write the whole configuration, which makes it a settings page")
	}

	// Every endpoint it does call is either an action or about the network.
	for _, call := range endpointsIn(body) {
		switch {
		case strings.HasPrefix(call, "/api/v1/menu/network"),
			strings.HasPrefix(call, "/api/v1/menu/display"):
		case strings.HasPrefix(call, "/api/v1/menu/restart"),
			call == "/api/v1/menu/reload",
			// Which language the screen speaks: a preference of the person
			// standing there, and the only other thing they may write.
			call == "/api/v1/menu/language",
			// Replacing the software on the machine. Offered here because the
			// menu asks for this device's password before it offers anything,
			// so the person pressing it has proved what they would have proved
			// by signing in to the web interface. Proximity alone still does
			// not authorise it -- see the pass, which has to be elevated.
			call == "/api/v1/menu/upgrade",
			// Proving who they are, and giving that proof up again. Not
			// /api/v1/session: signing in sets a cookie, and a cookie in the
			// browser bolted to the wall outlives the person who typed it.
			call == "/api/v1/screen/unlock",
			call == "/api/v1/screen/password",
			call == "/api/v1/screen/close",
			call == "/api/v1/playlist/next",
			call == "/api/v1/playlist/hold",
			call == "/api/v1/playlist/release",
			// Said every twenty seconds while the menu is open, so that a menu
			// that dies without closing cannot leave the screen still for ever.
			call == "/api/v1/playlist/keep",
			// Reloading the pages on the way out: somebody who has just
			// changed the network or the screen leaves dashboards behind that
			// loaded before any of it.
			call == "/api/v1/playlist/refresh",
			// Closing: the daemon puts the tab back where it was, rather than
			// the page steering itself through a history it may not have.
			call == "/api/v1/playlist/back",
			call == "/api/v1/wireless/reset",
			// Attaching this screen to an account. It belongs here for the
			// same reason the network does: it is a thing somebody standing
			// in front of a screen with a phone can do and somebody at a desk
			// cannot do for them, because the code has to be visible from
			// where they are. It writes no setting of its own -- what it
			// changes is who the device answers to, and only after somebody
			// authorises it somewhere else.
			call == "/api/v1/screen/link",
			strings.HasPrefix(call, "/api/v1/screen/link/code.svg"):
		default:
			t.Errorf("the menu calls %q, which is neither an action nor the network", call)
		}
	}
}

// endpointsIn finds the API paths a page asks for.
func endpointsIn(body string) []string {
	var found []string
	for _, piece := range strings.Split(body, `"`) {
		if strings.HasPrefix(piece, "/api/v1/") {
			found = append(found, piece)
		}
	}
	return found
}

// Somebody at the screen has a keyboard and a mouse, and the two things they
// might need are a wireless network and a fixed address on a wired one.
func TestTheMenuCanSetUpBothKindsOfNetwork(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	for _, wanted := range []string{
		"/api/v1/menu/network/scan",
		"/api/v1/menu/network/wireless",
		"/api/v1/menu/network/wired",
		"192.0.2.10/24",
	} {
		if !strings.Contains(body, wanted) {
			t.Errorf("the menu does not offer %q", wanted)
		}
	}
}

// Joining and configuring reach the device, and carry what was typed.
func TestWhatIsTypedAtTheScreenReachesTheDevice(t *testing.T) {
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)

	session := signedIn(t, server)
	_, port, _ := net.SplitHostPort(server.Address())
	ask := func(path string, body interface{}) int {
		encoded, _ := json.Marshal(body)
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
		request.RemoteAddr = "127.0.0.1:54321"
		request.Header.Set("Origin", "http://127.0.0.1:"+port)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(session)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		return response.Code
	}

	if code := ask("/api/v1/menu/network/wireless", map[string]string{
		"ssid": "the office", "passphrase": "a test passphrase",
	}); code != http.StatusOK {
		t.Fatalf("joining answered %d", code)
	}
	if device.joinedSSID != "the office" || device.joinedPassphrase != "a test passphrase" {
		t.Errorf("the device was asked to join %q with %q", device.joinedSSID, device.joinedPassphrase)
	}

	if code := ask("/api/v1/menu/network/wired", map[string]interface{}{
		"interface": "eth0", "method": "static",
		"address": "192.0.2.10/24", "gateway": "192.0.2.1",
		"nameservers": []string{"192.0.2.53"},
	}); code != http.StatusOK {
		t.Fatalf("configuring answered %d", code)
	}
	if device.wired.Name != "eth0" || device.wired.Method != "static" ||
		device.wired.Address != "192.0.2.10/24" || device.wired.Gateway != "192.0.2.1" {
		t.Errorf("the device was configured as %+v", device.wired)
	}
	if len(device.wired.Nameservers) != 1 || device.wired.Nameservers[0] != "192.0.2.53" {
		t.Errorf("the name servers arrived as %v", device.wired.Nameservers)
	}
}

// And a page the screen merely displays cannot do any of it.
func TestAPageTheScreenDisplaysCannotSetUpTheNetwork(t *testing.T) {
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)
	signedIn(t, server)

	for _, path := range []string{
		"/api/v1/menu/network/scan",
		"/api/v1/menu/network/wireless",
		"/api/v1/menu/network/wired",
		"/api/v1/menu/restart/browser",
	} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		request.RemoteAddr = "127.0.0.1:54321"
		request.Header.Set("Origin", "https://dashboard.example.com")
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)

		if response.Code == http.StatusOK {
			t.Errorf("a page from elsewhere was allowed to call %s", path)
		}
	}
	if device.joinedSSID != "" || device.wired.Name != "" || device.scans != 0 {
		t.Error("a page the screen merely displays reconfigured the network")
	}
}

// While it is open the screen must not rotate out from under whoever is
// reading it.
func TestTheMenuHoldsTheScreenStill(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	if !strings.Contains(body, "/api/v1/playlist/hold") {
		t.Error("the menu does not hold the screen still while it is open")
	}
	if !strings.Contains(body, "/api/v1/playlist/release") {
		t.Error("the menu never lets the screen go again")
	}
}

func menuPage(t *testing.T, server *Server) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/menu", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("the menu answered %d", response.Code)
	}
	return response.Body.String()
}

// Somebody at the screen has demonstrated the access this grants. Somebody on
// the network has not.
func TestResettingIsRefusedToTheNetworkAndAllowedFromTheScreen(t *testing.T) {
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)
	session := signedIn(t, server)

	if code := do(server, "POST", "/api/v1/wireless/reset", nil, nil).Code; code != http.StatusUnauthorized {
		t.Errorf("the network was allowed to reset the wireless: %d", code)
	}
	if device.forgotten != 0 {
		t.Error("the device forgot its network at the request of the network")
	}

	// A page this daemon served, on this device. That is the one case it is
	// for, and the browser sets the Origin itself.
	_, port, _ := net.SplitHostPort(server.Address())
	ours := httptest.NewRequest(http.MethodPost, "/api/v1/wireless/reset", nil)
	ours.RemoteAddr = "127.0.0.1:54321"
	ours.Header.Set("Origin", "http://127.0.0.1:"+port)
	ours.AddCookie(session)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, ours)

	if response.Code != http.StatusOK {
		t.Fatalf("the screen's own browser was refused: %d", response.Code)
	}
	if device.forgotten != 1 {
		t.Errorf("the device was asked to forget its network %d times", device.forgotten)
	}
}

// The browser on this device spends its life showing pages other people wrote,
// and any one of them can ask the loopback for whatever it likes. A dashboard
// that took its own screen off the network would be an unpleasant surprise.
func TestAPageTheScreenMerelyDisplaysCannotResetTheWireless(t *testing.T) {
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)
	signedIn(t, server)

	for _, origin := range []string{
		"https://dashboard.example.com",
		"http://127.0.0.1:9999",  // the loopback, but not this server
		"http://192.0.2.10:8080", // this device, but reached over the network
		"null",                   // a sandboxed frame
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/wireless/reset", nil)
		request.RemoteAddr = "127.0.0.1:54321"
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)

		if response.Code == http.StatusOK {
			t.Errorf("a page from %s was allowed to reset the wireless", origin)
		}
	}
	if device.forgotten != 0 {
		t.Errorf("the device forgot its network %d times at the request of a page "+
			"it merely displays", device.forgotten)
	}
}

// A request with no Origin is a command line, not a page, and a command line
// has the API and a password. This is not hypothetical: a stray curl during
// development took a device off its network.
func TestARequestWithNoOriginIsRefused(t *testing.T) {
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)
	signedIn(t, server)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/wireless/reset", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)

	if response.Code == http.StatusOK {
		t.Error("a request with no Origin was allowed to reset the wireless")
	}
	if device.forgotten != 0 {
		t.Error("the device forgot its network at the request of something with no page")
	}
}

// Injecting the same script twice must not give a screen two buttons.
func TestTheWayBackOnlyInstallsItselfOnce(t *testing.T) {
	if !strings.Contains(newTestServer(t, config.Default()).WayBackScript(), "if (window.__cueWayBack) return;") {
		t.Error("the script does not guard against being added twice")
	}
}

// Somebody in a room in Osaka should not have to read English to put their
// screen back on the wireless, so the menu speaks three languages and switches
// between them on the spot.
func TestTheMenuSpeaksThreeLanguages(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	// The list is built from the dictionary rather than written in the markup,
	// so that adding a language is one edit. That is what is checked: the
	// dictionary names each language, and the page builds a list from it.
	for _, name := range []string{`"language-name": "English"`, `"language-name": "中文"`, `"language-name": "日本語"`} {
		if !strings.Contains(body, name) {
			t.Errorf("the dictionary does not name %s", name)
		}
	}
	if !strings.Contains(body, "for (const code of Object.keys(SAID))") {
		t.Error("the language list is not built from the dictionary, so adding a " +
			"language would mean remembering to edit the markup as well")
	}
	if !strings.Contains(body, `id="globe"`) {
		t.Error("there is no globe to open the languages with")
	}
	// A few words from each, so that an empty dictionary would fail here.
	for _, words := range []string{"Set up the network", "设置网络", "ネットワークを設定"} {
		if !strings.Contains(body, words) {
			t.Errorf("the menu does not say %q", words)
		}
	}
}

// Every phrase must exist in every language, or switching leaves somebody
// looking at a mixture -- or at the key itself, which is worse.
func TestEveryPhraseExistsInEveryLanguage(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	said := body[strings.Index(body, "const SAID = {"):]
	said = said[:strings.Index(said, "\n  };")]

	keys := map[string]map[string]bool{}
	language := ""
	for _, line := range strings.Split(said, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, name := range []string{"en:", "zh:", "ja:"} {
			if strings.HasPrefix(trimmed, name) {
				language = strings.TrimSuffix(name, ":")
				keys[language] = map[string]bool{}
			}
		}
		if language == "" {
			continue
		}
		for _, piece := range strings.Split(trimmed, `"`) {
			// A key is what appears immediately before a colon.
			if index := strings.Index(trimmed, `"`+piece+`":`); index >= 0 && piece != "" {
				keys[language][piece] = true
			}
		}
	}

	if len(keys) != 3 {
		t.Fatalf("found %d languages, want 3", len(keys))
	}
	for _, language := range []string{"zh", "ja"} {
		for phrase := range keys["en"] {
			if !keys[language][phrase] {
				t.Errorf("%q is missing from %s", phrase, language)
			}
		}
	}
}

// The template's keys and the dictionary have to agree. A key in the markup
// with nothing behind it shows the key itself on the screen.
func TestEveryKeyInTheMarkupHasWordsBehindIt(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	said := body[strings.Index(body, `en: {`):]
	said = said[:strings.Index(said, "\n    },")]

	for _, piece := range strings.Split(body, `data-t="`)[1:] {
		key := piece[:strings.Index(piece, `"`)]
		if !strings.Contains(said, `"`+key+`":`) {
			t.Errorf("the markup asks for %q and the dictionary has no such phrase", key)
		}
	}
}

// A language chosen at the screen is written to the device, not only to the
// browser. Wiping the browser profile is one of the things the watchdog does
// when a screen wedges, and a device that forgot its language every time it
// recovered would be a poor thing to live with.
func TestALanguageChosenAtTheScreenIsRememberedByTheDevice(t *testing.T) {
	server := newTestServer(t, config.Default())
	session := signedIn(t, server)

	_, port, _ := net.SplitHostPort(server.Address())
	ask := func(body string) int {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/menu/language", strings.NewReader(body))
		request.RemoteAddr = "127.0.0.1:54321"
		request.Header.Set("Origin", "http://127.0.0.1:"+port)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(session)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		return response.Code
	}

	if code := ask(`{"language":"ja"}`); code != http.StatusOK {
		t.Fatalf("choosing a language answered %d", code)
	}
	if got := server.store.Current().Device.Language; got != "ja" {
		t.Errorf("the device remembers %q, want ja", got)
	}

	// And the menu comes back in that language without being told.
	if !strings.Contains(menuPage(t, server), `if (SAID["ja"]) language = "ja"`) {
		t.Error("the menu does not start in the language the device remembers")
	}
}

// It goes into a file and into a page, so it has to be a tag and not a
// sentence.
func TestOnlyALanguageTagIsAccepted(t *testing.T) {
	server := newTestServer(t, config.Default())

	_, port, _ := net.SplitHostPort(server.Address())
	session := signedIn(t, server)
	for _, attempt := range []string{
		`{"language":"<script>alert(1)</script>"}`,
		`{"language":"../../etc/passwd"}`,
		`{"language":"english please"}`,
		`{"language":"e"}`,
		`{"language":""}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/menu/language", strings.NewReader(attempt))
		request.RemoteAddr = "127.0.0.1:54321"
		request.Header.Set("Origin", "http://127.0.0.1:"+port)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(session)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)

		if response.Code == http.StatusOK {
			t.Errorf("%s was accepted as a language", attempt)
		}
	}
	if got := server.store.Current().Device.Language; got != "" {
		t.Errorf("the device ended up with language %q", got)
	}
}

// screenRequest is what the screen's own browser sends: from this machine, on
// a page this daemon served.
func screenRequest(t *testing.T, server *Server, method, path, body string, session *http.Cookie) int {
	t.Helper()
	_, port, _ := net.SplitHostPort(server.Address())
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:54321"
	request.Header.Set("Origin", "http://127.0.0.1:"+port)
	request.Header.Set("Content-Type", "application/json")
	if session != nil {
		request.AddCookie(session)
	}
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	return response.Code
}

// Being in the room used to be enough on a device with no password, on the
// reasoning that there was nobody to ask. Now there always is: the menu asks
// for a password to be set before it offers anything, so nothing reaches an
// action without one.
//
// The old test for this checked only that the answer was not 401. It is 403 on
// a device with no password, so it went on passing after the behaviour had
// changed completely. This one checks that the action does not happen.
func TestABrandNewDeviceDoesNothingUntilAPasswordIsChosen(t *testing.T) {
	server := newTestServer(t, config.Default())
	_, pass := openMenu(t, server)

	for _, path := range []string{
		"/api/v1/wireless/reset",
		"/api/v1/menu/network/scan",
		"/api/v1/menu/restart/browser",
	} {
		code := passRequest(t, server, http.MethodPost, path, nil, pass)
		if code == http.StatusOK {
			t.Errorf("%s acted on a device that has no password yet", path)
		}
	}

	// Choosing one through the same pass is what opens them.
	code := passRequest(t, server, http.MethodPost, "/api/v1/screen/password",
		map[string]string{"password": "a good long test password"}, pass)
	if code != http.StatusOK {
		t.Fatalf("choosing the first password answered %d", code)
	}
	if code := passRequest(t, server, http.MethodPost, "/api/v1/menu/restart/browser", nil, pass); code != http.StatusOK {
		t.Errorf("after choosing a password the screen still cannot act: %d", code)
	}
	if !server.isSetUp() {
		t.Error("the password was not kept")
	}
}

// openMenu serves the menu the way the screen's own browser does, and returns
// the page and the pass it was given.
func openMenu(t *testing.T, server *Server) (string, string) {
	t.Helper()
	body := menuPage(t, server)
	match := regexp.MustCompile(`const pass = "([^"]+)"`).FindStringSubmatch(body)
	if match == nil {
		t.Fatal("the menu was served without a pass")
	}
	return body, match[1]
}

// passRequest is a call the menu itself would make: no cookie, no Origin, just
// the pass it was handed.
func passRequest(t *testing.T, server *Server, method, path string, body interface{}, pass string) int {
	t.Helper()
	encoded := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		encoded = string(raw)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(encoded))
	request.RemoteAddr = "127.0.0.1:54321"
	request.Header.Set("Content-Type", "application/json")
	if pass != "" {
		request.Header.Set(passHeader, pass)
	}
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	return response.Code
}

// Once somebody has set a password, that password says who may change the
// device -- not proximity to the mouse. A screen in a lobby, a waiting room or
// a shop window is somewhere strangers stand.
func TestADeviceWithAnOwnerAsksAtTheScreenToo(t *testing.T) {
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)
	session := signedIn(t, server)

	for _, path := range []string{
		"/api/v1/wireless/reset",
		"/api/v1/menu/network/scan",
		"/api/v1/menu/network/wireless",
		"/api/v1/menu/network/wired",
		"/api/v1/menu/restart/browser",
		"/api/v1/menu/language",
	} {
		if code := screenRequest(t, server, http.MethodPost, path, `{"language":"ja"}`, nil); code != http.StatusUnauthorized {
			t.Errorf("%s answered %d at the screen with no password, want 401", path, code)
		}
	}
	if device.forgotten != 0 || device.scans != 0 || device.joinedSSID != "" {
		t.Error("a device with an owner was changed by somebody who did not know its password")
	}

	// And with the password, the same requests are allowed.
	if code := screenRequest(t, server, http.MethodPost, "/api/v1/menu/network/scan", "{}", session); code != http.StatusOK {
		t.Errorf("the password did not open the menu: %d", code)
	}
}

// The two things the screen does by itself must keep working without a
// password, or a video would never move the playlist on.
func TestWhatTheScreenDoesByItselfIsNotGated(t *testing.T) {
	server := newTestServer(t, config.Default())
	signedIn(t, server)

	for _, path := range []string{
		"/api/v1/playlist/next",
		"/api/v1/playlist/hold",
		"/api/v1/playlist/release",
	} {
		if code := screenRequest(t, server, http.MethodPost, path, "{}", nil); code == http.StatusUnauthorized {
			t.Errorf("%s now needs a password, so a video would never move the screen on", path)
		}
	}
}

// The menu says so on the page, rather than only refusing when a button is
// pressed.
func TestTheMenuAsksForThePasswordUpFront(t *testing.T) {
	owned := newTestServer(t, config.Default())
	signedIn(t, owned)
	body := menuPage(t, owned)
	if !strings.Contains(body, "const hasWord = true") {
		t.Error("a device with an owner does not ask at the screen")
	}
	for _, words := range []string{"password to change it", "密码", "パスワード"} {
		if !strings.Contains(body, words) {
			t.Errorf("the menu does not say %q in every language", words)
		}
	}
}

// A device with no password is not a device nobody owns. It is one somebody
// never finished setting up -- which is the state a device stays in for as
// long as nobody visits its web interface, and some never do. Letting the next
// passer-by change its network on that basis was the hole; asking them to
// choose a password closes it in one step.
func TestADeviceWithNoPasswordIsAskedToChooseOne(t *testing.T) {
	fresh := newTestServer(t, config.Default())
	body := menuPage(t, fresh)

	if !strings.Contains(body, "const hasWord = false") {
		t.Error("a brand new device asks for a password it does not have")
	}
	if !strings.Contains(body, `"choose-explain"`) {
		t.Error("the menu does not offer to set a password")
	}
	// Twice, so that a mistyped one is not the password from then on.
	if !strings.Contains(body, `id="word-again"`) {
		t.Error("the new password is asked for only once")
	}
}

// The mark is added to every page the browser shows, and the menu is a page
// the browser shows. Without this it appeared on the menu itself, where
// pressing it opened a second menu over the first.
func TestTheMarkDoesNotAppearOnTheMenuItself(t *testing.T) {
	script := newTestServer(t, config.Default()).WayBackScript()

	for _, page := range []string{"/menu", "/upgrading"} {
		if !strings.Contains(script, page) {
			t.Errorf("the script does not exclude %s, so the mark appears there", page)
		}
	}
	// It must still install itself on the pages the screen actually shows,
	// including the daemon's own player.
	if !strings.Contains(script, "location.href.indexOf") {
		t.Error("the exclusion is not written as a check on the address")
	}
}

// A screen that already belongs to somebody must ask for its password before
// it will show a linking code.
//
// The code is what attaches the device to an account, so showing one to
// whoever walks up to a lobby screen would be handing the device away to
// anybody with a phone.
func TestLinkingAtTheScreenNeedsThePasswordFirst(t *testing.T) {
	configuration := config.Default()
	configuration.Service.Address = "https://example.com"
	server := newTestServer(t, configuration)

	// Give the device an owner, so that a live pass is not enough on its own.
	if code := passRequest(t, server, http.MethodPost, "/api/v1/setup",
		map[string]string{"password": "an example password"}, ""); code != http.StatusOK {
		t.Fatalf("setting a password answered %d", code)
	}

	_, pass := openMenu(t, server)

	// A pass that has not had the password proved through it gets nothing.
	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/link", nil, pass); code != http.StatusForbidden {
		t.Errorf("starting a link without the password answered %d, want 403", code)
	}
	if state := server.device.Linker().State(); state.Pending {
		t.Error("a code was minted for somebody who had not given the password")
	}

	// And no pass at all gets nothing either.
	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/link", nil, ""); code != http.StatusForbidden {
		t.Errorf("starting a link with no pass answered %d, want 403", code)
	}

	// Once the password is proved, it works.
	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/unlock",
		map[string]string{"password": "an example password"}, pass); code != http.StatusOK {
		t.Fatalf("unlocking answered %d", code)
	}
	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/link", nil, pass); code != http.StatusOK {
		t.Fatalf("starting a link after unlocking answered %d", code)
	}
	if state := server.device.Linker().State(); !state.Pending || state.URL == "" {
		t.Errorf("no code was minted after unlocking: %+v", state)
	}

	// The picture is served to the same pass, and refused without one.
	if code := passRequest(t, server, http.MethodGet, "/api/v1/screen/link/code.svg", nil, pass); code != http.StatusOK {
		t.Errorf("the code picture answered %d", code)
	}
	if code := passRequest(t, server, http.MethodGet, "/api/v1/screen/link/code.svg", nil, ""); code != http.StatusForbidden {
		t.Errorf("the code picture was served without a pass: %d", code)
	}
}

// Unlinking is not offered at the screen. It is not urgent, and a stranger
// doing it leaves a device that quietly stops reporting.
func TestForgettingALinkIsNotOfferedAtTheScreen(t *testing.T) {
	server := newTestServer(t, config.Default())
	_, pass := openMenu(t, server)

	if code := passRequest(t, server, http.MethodPost, "/api/v1/link/forget", nil, pass); code == http.StatusOK {
		t.Error("a page on the screen could forget the device's link")
	}
}

// The code the screen shows cannot be fetched by pointing an img at it.
//
// It is served only to a page holding this screen's pass, and the pass travels
// in a header. An img element cannot send a header, so a src pointing at this
// asks without one and is refused -- which is what happened: the box drew on
// the screen and the code inside it never did, every time, for as long as the
// feature existed.
//
// The fix is on the page, which fetches the picture and puts it in the img
// itself. This is the reason that has to be done, written down where somebody
// tempted to simplify it back will see it.
func TestTheScreensCodeIsRefusedWithoutTheHeader(t *testing.T) {
	configuration := config.Default()
	configuration.Service.Address = "https://example.com"
	server := newTestServer(t, configuration)
	defer func() { _ = server.device.Linker().Close() }()

	_, pass := openMenu(t, server)
	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/link", nil, pass); code != http.StatusOK {
		t.Fatalf("starting a link answered %d", code)
	}

	// With the header, as the page's own fetch sends it.
	if code := passRequest(t, server, http.MethodGet,
		"/api/v1/screen/link/code.svg", nil, pass); code != http.StatusOK {
		t.Errorf("the picture was refused to a page holding the pass: %d", code)
	}

	// Without it, as an img element asks.
	if code := passRequest(t, server, http.MethodGet,
		"/api/v1/screen/link/code.svg", nil, ""); code == http.StatusOK {
		t.Error("the picture is served to a request with no pass, so it is public")
	}
}
