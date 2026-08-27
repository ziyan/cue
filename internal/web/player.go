package web

import (
	"html/template"
	"net/http"

	"github.com/gorilla/mux"
)

// The page that plays one video full screen on the device's own display.
//
// It is a page of its own rather than part of the interface because what it
// has to be is a single video filling the screen with nothing round it: no
// controls, no scrollbars, no margins, nothing that could be seen from across
// a room and read as part of the picture.

// play renders the player for one playlist item.
func (self *Server) play(response http.ResponseWriter, request *http.Request) {
	identifier := mux.Vars(request)["item"]

	var found *videoItem
	for _, item := range self.store.Current().Playlist.Items {
		if item.Identifier != identifier || item.Video == nil {
			continue
		}
		found = &videoItem{
			Source: "/videos/" + item.Video.File,
			Name:   item.Video.Name,
			Muted:  !item.Video.Sound,
		}
		break
	}
	if found == nil {
		http.NotFound(response, request)
		return
	}

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	if err := playerTemplate.Execute(response, found); err != nil {
		log.Debugf("cannot render the player: %s", err)
	}
}

type videoItem struct {
	Source string
	Name   string
	Muted  bool
}

// showNext moves the screen to the item after the one on it.
//
// The player page calls this when its video ends, which is what makes a video
// item take exactly as long as its video. Nothing else in the daemon knows how
// long a video is, and asking would mean reading the file's headers -- which
// would still be a guess for a video that is replaced later.
func (self *Server) showNext(response http.ResponseWriter, request *http.Request) {
	browser := self.device.Browser()
	if browser == nil {
		writeError(response, http.StatusServiceUnavailable, "there is no browser to move on")
		return
	}
	if err := browser.ShowNext(request.Context()); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"moved": true})
}

var playerTemplate = template.Must(template.New("player").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{ .Name }}</title>
<style>
  html, body { margin: 0; height: 100%; background: #000; overflow: hidden; }
  video { width: 100%; height: 100%; display: block; object-fit: contain; background: #000; }
  /* A video whose shape does not match the screen gets bars rather than being
     stretched or cropped: a stretched face is more obviously wrong to a person
     walking past than a black band is. */
  #trouble {
    position: absolute; inset: 0; display: none; place-items: center;
    color: #9fb0c5; background: #000; font: 4vmin system-ui, sans-serif;
    text-align: center; padding: 6vmin;
  }
</style>
</head>
<body>
<video id="video" src="{{ .Source }}" autoplay playsinline{{ if .Muted }} muted{{ end }}></video>
<div id="trouble">This video could not be played.<br>Moving on.</div>
<script>
  const video = document.getElementById("video");
  const trouble = document.getElementById("trouble");
  let moved = false;

  // Asked for once and once only. The daemon moves the screen on, which
  // navigates this page away, and a second request racing that navigation
  // would skip the item after this one.
  function moveOn(why) {
    if (moved) return;
    moved = true;
    console.log("[cue] moving on: " + why);
    fetch("/api/v1/playlist/next", { method: "POST" }).catch(() => {});
  }

  video.addEventListener("ended", () => moveOn("the video ended"));

  video.addEventListener("error", () => {
    // Said on the screen before moving on, so that somebody walking past a
    // wall sees why it skipped rather than a black flash.
    trouble.style.display = "grid";
    setTimeout(() => moveOn("the video could not be played"), 4000);
  });

  // A backstop. A video that neither ends nor errors -- a truncated file that
  // stalls, a decoder that gives up quietly -- would otherwise hold the screen
  // for ever. The wait is the video's own length plus a margin once that is
  // known, and a flat five minutes until it is.
  let backstop = setTimeout(() => moveOn("the video did not finish in time"), 5 * 60 * 1000);
  video.addEventListener("loadedmetadata", () => {
    if (!isFinite(video.duration) || video.duration <= 0) return;
    clearTimeout(backstop);
    backstop = setTimeout(() => moveOn("the video did not finish in time"),
      (video.duration + 30) * 1000);
  });

  // Autoplay can still be refused, and a refusal is silent. Chromium here is
  // started with --autoplay-policy=no-user-gesture-required so it should not
  // be, but if it ever is, the screen must not sit on a black rectangle.
  video.play().catch((error) => {
    console.log("[cue] the video would not start: " + error);
    trouble.style.display = "grid";
    setTimeout(() => moveOn("the video would not start"), 4000);
  });
</script>
</body>
</html>
`))
