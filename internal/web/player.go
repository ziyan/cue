package web

import (
	"html/template"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/ziyan/cue/internal/media"
)

// The page that shows one uploaded picture or video full screen on the
// device's own display.
//
// It is a page of its own rather than part of the interface because what it
// has to be is a single video filling the screen with nothing round it: no
// controls, no scrollbars, no margins, nothing that could be seen from across
// a room and read as part of the picture.

// play renders the player for one playlist item.
func (self *Server) play(response http.ResponseWriter, request *http.Request) {
	identifier := mux.Vars(request)["item"]

	var found *shownItem
	for _, item := range self.store.Current().Playlist.Items {
		if item.Identifier != identifier || item.Media == nil {
			continue
		}
		found = &shownItem{
			Source:  "/media/" + item.Media.File,
			Name:    item.Media.Name,
			Muted:   !item.Media.Sound,
			IsVideo: item.Media.Kind != string(media.KindPicture),
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

type shownItem struct {
	Source string
	Name   string
	Muted  bool

	// IsVideo decides both which element draws it and what says when it is
	// finished. A video holds the screen until it ends; a picture holds it for
	// the ordinary rotation time, having no end of its own.
	IsVideo bool
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
  video, img { width: 100%; height: 100%; display: block; object-fit: contain; background: #000; }
  /* Something whose shape does not match the screen gets bars rather than
     being stretched or cropped: a stretched face is more obviously wrong to a
     person walking past than a black band is. */
  #trouble {
    position: absolute; inset: 0; display: none; place-items: center;
    color: #9fb0c5; background: #000; font: 4vmin system-ui, sans-serif;
    text-align: center; padding: 6vmin;
  }
</style>
</head>
<body>
{{ if .IsVideo }}
<video id="video" src="{{ .Source }}" playsinline preload="auto"{{ if .Muted }} muted{{ end }}></video>
{{ else }}
<img id="picture" src="{{ .Source }}" alt="">
{{ end }}
<div id="trouble">This could not be shown.<br>Moving on.</div>
<script>
  // The playlist keeps one tab open per item and switches between them, so
  // this page exists long before its turn comes and goes on existing
  // afterwards. That shapes everything below.
  const trouble = document.getElementById("trouble");
  const video = document.getElementById("video");
  let moved = false;
  let backstop = null;

  const onScreen = () => document.visibilityState === "visible";

  function moveOn(why) {
    if (moved) return;
    // Only ever from the page somebody is actually looking at. Something that
    // finishes while its tab is in the background would otherwise move the
    // playlist on from whatever *is* on the screen, cutting a dashboard short
    // for no visible reason.
    if (!onScreen()) {
      console.log("[cue] not moving on while off screen: " + why);
      return;
    }
    moved = true;
    console.log("[cue] moving on: " + why);
    fetch("/api/v1/playlist/next", { method: "POST" }).catch(() => {});
  }

  const picture = document.getElementById("picture");
  if (picture) {
    // A picture has no end of its own, so it stays for the ordinary rotation
    // time like every other item and this page does nothing but show it. The
    // only thing worth handling is a picture that will not load, which would
    // otherwise be a black screen until the clock moved on.
    picture.addEventListener("error", () => {
      trouble.style.display = "grid";
      setTimeout(() => moveOn("the picture could not be shown"), 4000);
    });
  } else {
    // The video is deliberately not marked autoplay. With autoplay it started
    // the moment the tab was created, played all the way through while a
    // dashboard was on the screen, and was sitting on its last frame by the
    // time anybody could see it -- which looks exactly like a video that will
    // not play. It starts when this page becomes visible instead, and from the
    // beginning each time, so a video in a rotation plays in full every time
    // round rather than once ever.
    video.addEventListener("ended", () => moveOn("the video ended"));

    video.addEventListener("error", () => {
      // Said on the screen before moving on, so that somebody walking past a
      // wall sees why it skipped rather than a black flash.
      trouble.style.display = "grid";
      setTimeout(() => moveOn("the video could not be played"), 4000);
    });

    // A backstop, armed only while this page is on screen. A video that
    // neither ends nor errors -- a truncated file that stalls, a decoder that
    // gives up quietly -- would otherwise hold the screen for ever. The wait
    // is the video's own length plus a margin once that is known, and a flat
    // five minutes until it is.
    var armBackstop = function () {
      if (backstop) clearTimeout(backstop);
      const known = isFinite(video.duration) && video.duration > 0;
      const wait = known ? (video.duration + 30) * 1000 : 5 * 60 * 1000;
      backstop = setTimeout(() => moveOn("the video did not finish in time"), wait);
    };

    var start = function () {
      moved = false;
      try {
        video.currentTime = 0;
      } catch (error) {
        // Seeking before the metadata has arrived throws; the load handler
        // below starts it again once there is something to seek in.
      }
      armBackstop();
      video.play().catch((error) => {
        console.log("[cue] the video would not start: " + error);
        trouble.style.display = "grid";
        setTimeout(() => moveOn("the video would not start"), 4000);
      });
    };

    document.addEventListener("visibilitychange", () => {
      if (onScreen()) {
        start();
      } else {
        video.pause();
        if (backstop) {
          clearTimeout(backstop);
          backstop = null;
        }
      }
    });

    video.addEventListener("loadedmetadata", () => {
      // Now the length is known, so the backstop can be the right length --
      // and a page that became visible before the metadata arrived can start.
      if (onScreen()) start();
    });

    // The tab may already be the one on screen when this page loads, in which
    // case no visibilitychange is coming and it has to start itself.
    if (onScreen()) start();
  }
</script>
</body>
</html>
`))
