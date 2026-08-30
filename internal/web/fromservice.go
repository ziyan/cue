package web

import (
	"net/http"

	"github.com/gorilla/mux"
)

// What the hosted service may do to a device that is linked to it.
//
// An allow-list, written out here rather than "the management router minus a
// few". The difference matters: a route added to the interface tomorrow
// appears in the interface and nowhere else, and somebody has to come here and
// decide before it is reachable from across the internet. The other way round,
// every new route would be exposed by default and the decision would be one
// nobody made.
//
// Deliberately absent, and why:
//
//   - The screen itself, over VNC. A websocket inside a websocket, and its own
//     piece of work.
//   - Uploading pictures and videos. Megabytes travelling up through the
//     service to reach a device that has a perfectly good upload form on the
//     network it is already on.
//   - Anything about linking. A service that could unlink a device, or mint a
//     code on it, could hand it to somebody else; the whole authority here
//     comes from the link, so it must not be able to change it.
//   - Setup and the password. Whoever holds the screen owns those.
func (self *Server) fromService() http.Handler {
	router := mux.NewRouter()
	api := router.PathPrefix("/api/v1").Subrouter()

	// Reading what this device is and what it is doing.
	api.Path("/status").Methods(http.MethodGet).HandlerFunc(self.status)
	api.Path("/configuration").Methods(http.MethodGet).HandlerFunc(self.readConfiguration)
	api.Path("/configuration").Methods(http.MethodPut).HandlerFunc(self.writeConfiguration)
	api.Path("/network").Methods(http.MethodGet).HandlerFunc(self.networkState)
	api.Path("/timezones").Methods(http.MethodGet).HandlerFunc(self.timezones)
	api.Path("/logs/xorg").Methods(http.MethodGet).HandlerFunc(self.xorgLog)
	api.Path("/screenshot.png").Methods(http.MethodGet).HandlerFunc(self.screenshot)

	// Making it do something.
	api.Path("/show/{item}").Methods(http.MethodPost).HandlerFunc(self.show)
	api.Path("/navigate").Methods(http.MethodPost).HandlerFunc(self.navigate)
	api.Path("/restart/{program}").Methods(http.MethodPost).HandlerFunc(self.restart)
	api.Path("/playlist/next").Methods(http.MethodPost).HandlerFunc(self.showNext)

	// Enough to tell whether the device is answering at all, which is what
	// anything watching it asks first.
	router.Path("/healthz").Methods(http.MethodGet).HandlerFunc(self.health)

	// Anything else is not refused for lack of a session -- there is no
	// session here -- but because it is not on the list.
	router.NotFoundHandler = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		writeError(response, http.StatusNotFound,
			"this device does not offer "+request.URL.Path+" to the service it is linked to")
	})
	router.MethodNotAllowedHandler = router.NotFoundHandler

	return router
}

// FromService is the interface a linked device offers to the service that owns
// it, served over the tunnel and nowhere else.
//
// There is no session and nothing to authenticate here, which is the whole
// point rather than an omission: this handler is only ever given a connection
// the device received on a websocket it opened itself, to a service that
// verified the device's own credential before accepting it. The authority
// comes from that handshake. Nothing on this machine can reach this handler,
// because it is not on a socket -- there is no port, no loopback listener and
// nothing to connect to.
func (self *Server) FromService() http.Handler {
	return self.fromService()
}
