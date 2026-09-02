package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ziyan/cue/internal/config"
)

// Asking the service what this device should show.
//
// The same conditional treatment as the profile, and a different answer to the
// same question about emptiness. For a profile, no profile and an empty
// profile mean the same thing: release everything and take nothing new. For a
// playlist that reasoning inverts, and applying it would be a disaster -- a
// playlist is content rather than an overlay, so a device with none assigned
// receiving an empty document would wipe the items somebody set up on it
// locally, on the first poll, on every screen that had never been given one.
//
// So the wire tells them apart:
//
//	204  no playlist is assigned; leave what this screen shows alone
//	200  this playlist is assigned; replace the items with it
//	304  the same playlist this device already has
//
// Which makes the third state expressible: an assigned but empty playlist is a
// 200 with no items, meaning show nothing. It is only expressible because
// having none is not a document.

// devicePlaylist is the document the service serves.
//
// The durations are config.Duration rather than int, and that is not tidiness.
// The same idea has two encodings between these two programs: this device's own
// configuration writes a duration as "45s", because that is what an operator
// types in a file, while this document carries a bare number of seconds. The
// service reads the first and writes the second, and got it wrong in that
// direction -- an int where a string arrives does not produce a wrong duration,
// it fails to unmarshal the whole document, so the first read of a real screen
// returned nothing at all.
//
// config.Duration takes either. So if a service ever sends "45s" here, this
// device takes it, instead of failing to apply the entire playlist and leaving
// a wall showing the old one with only a debug line to say why.
type devicePlaylist struct {
	Interval config.Duration `json:"interval"`
	Items    []deviceItem    `json:"items"`
}

type deviceItem struct {
	Identifier string           `json:"identifier"`
	URL        string           `json:"url"`
	Title      string           `json:"title"`
	Duration   config.Duration  `json:"duration"`
	Reload     bool             `json:"reload"`
	Disabled   bool             `json:"disabled"`
	Media      *deviceItemMedia `json:"media"`
	Login      *config.Login    `json:"login"`
	Dismiss    []config.Dismiss `json:"dismiss"`
}

// deviceItemMedia names a file two ways. mediaId is the service's own name for
// it and is what this device fetches with; file is a digest of the bytes and is
// what this device stores under, because the store is content-addressed and
// that is what makes "do I already have this?" a question about the local disk
// with no round trip.
type deviceItemMedia struct {
	File    string `json:"file"`
	MediaID string `json:"mediaId"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Sound   bool   `json:"sound"`
}

// pollPlaylistOnce asks what this device should show and takes it.
func (self *Reporter) pollPlaylistOnce(ctx context.Context, client *http.Client) error {
	asking, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(asking, http.MethodGet,
		"http://"+tunnelHost+"/api/v1/device/playlist", nil)
	if err != nil {
		return err
	}
	if tag := self.playlistTag(); tag != "" {
		request.Header.Set("If-None-Match", tag)
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusNoContent:
		// No playlist is assigned. This device's own items are its own, and
		// nothing here may touch them. Not an error and not a release: the
		// common case for a screen nobody has put on a playlist yet, which is
		// every screen in service today.
		//
		// Said at debug, because otherwise this answer leaves no trace at all
		// and "the items are unchanged" cannot be told apart from "nothing
		// ever asked". That distinction is the whole of the test for this
		// behaviour, and the first time it was checked it had to be settled
		// from the service's access log because the device had nothing to say.
		log.Debugf("the service has no playlist for this device; its own items are left alone")
		return nil

	case http.StatusNotModified:
		return nil

	case http.StatusOK:
		// Handled below.

	case http.StatusUnauthorized:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("service: the playlist was refused; nothing changed")

	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("service: asking for the playlist answered %s", response.Status)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, largestProfile))
	if err != nil {
		return err
	}

	var served devicePlaylist
	if err := json.Unmarshal(body, &served); err != nil {
		return fmt.Errorf("service: the playlist is not a playlist document: %w", err)
	}

	// Fetched before the playlist is applied, so a screen switches to a
	// playlist it can already show rather than to one whose videos arrive over
	// the next few minutes. The old playlist goes on showing meanwhile, which
	// is the right thing for it to be doing.
	// Every file first, and the playlist only if they all arrived.
	//
	// The swap is all or nothing on purpose. A playlist applied with a file
	// still missing puts an item on the screen that shows nothing, and the
	// earlier version of this did exactly that on the grounds that one blank
	// item beats a playlist that never arrives. That is the wrong trade for a
	// wall: a screen showing the playlist it had is a screen doing its job,
	// while a screen showing a gap where a video should be is one somebody has
	// to be told about. Waiting costs a poll interval; swapping early costs
	// whatever is on the wall until the file turns up.
	//
	// Each file is already atomic on its own -- the store writes to a
	// temporary name, hashes what arrives, and only then renames it into
	// place, so a half-fetched video is never something an item could point at.
	// What was missing was the same guarantee across the set.
	if missing := self.fetchMedia(ctx, client, served.Items); missing > 0 {
		// Deliberately without remembering the version. The next poll asks for
		// this document again and tries the files again, rather than being
		// told 304 for ever about a playlist it never applied.
		log.Warningf("not changing what this screen shows yet: %d file(s) in the new "+
			"playlist are not on this device, and it will keep showing what it has "+
			"until they are", missing)
		return nil
	}

	items := make([]config.Item, 0, len(served.Items))
	for _, slide := range served.Items {
		items = append(items, itemOf(slide))
	}

	changed := false
	err = self.store.Update(func(configuration *config.Configuration) error {
		if sameItems(configuration.Playlist.Items, items) &&
			(served.Interval <= 0 || configuration.Playlist.Interval == served.Interval) {
			return nil
		}
		changed = true
		configuration.Playlist.Items = items
		if served.Interval > 0 {
			configuration.Playlist.Interval = served.Interval
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("service: cannot apply the playlist: %w", err)
	}

	// The version is remembered only when every file arrived. A device that
	// remembered it with a file missing would be told 304 for ever after and
	// never try again, so one failed transfer would leave a permanently blank
	// item that nothing retries.
	self.setPlaylistTag(response.Header.Get("ETag"))

	if changed {
		log.Noticef("the service's playlist changed what this device shows: %d item(s)", len(items))
	}
	return nil
}

// itemOf turns a slide into an item, field for field.
//
// The identifier is carried across rather than minted here, and it is
// load-bearing: internal/browser/playlist.go keys browser tabs by it, so an
// identifier that changed between polls would tear down and rebuild every tab,
// visibly reloading the wall and restarting anything part-way through.
func itemOf(slide deviceItem) config.Item {
	item := config.Item{
		Identifier: slide.Identifier,
		URL:        slide.URL,
		Title:      slide.Title,
		Duration:   slide.Duration,
		Reload:     slide.Reload,
		Disabled:   slide.Disabled,
		Login:      slide.Login,
		Dismiss:    slide.Dismiss,
	}
	if slide.Media != nil {
		item.Media = &config.ItemMedia{
			// The digest, not the service's identifier: the store is
			// content-addressed, so the same bytes shared by several items or
			// several playlists are one file on a disk that is not large.
			File:  slide.Media.File,
			Name:  slide.Media.Name,
			Kind:  slide.Media.Kind,
			Sound: slide.Media.Sound,
		}
	}
	return item
}

// sameItems reports whether the playlist is already what was served, so that
// an unchanged document does not rewrite the file.
//
// Compared rather than trusted to the ETag alone, because the ETag is the
// service's answer about its own document and says nothing about what this
// device did with it -- a device whose file was edited locally has to be
// corrected at the next poll, which is the whole point of being managed.
func sameItems(mine, theirs []config.Item) bool {
	if len(mine) != len(theirs) {
		return false
	}
	for index := range mine {
		left, err := json.Marshal(mine[index])
		if err != nil {
			return false
		}
		right, err := json.Marshal(theirs[index])
		if err != nil {
			return false
		}
		if string(left) != string(right) {
			return false
		}
	}
	return true
}

func (self *Reporter) playlistTag() string {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.lastPlaylistTag
}

func (self *Reporter) setPlaylistTag(tag string) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.lastPlaylistTag = tag
}
