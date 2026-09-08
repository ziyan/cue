package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ziyan/cue/internal/media"
)

// Fetching the files a playlist refers to.
//
// A file is fetched once, ever, and kept. That is what lets a screen with no
// uplink go on showing what it was showing -- which was already true when
// media was uploaded to the device by hand, and pulling from a service must
// not quietly give it up.
//
// "Do I already have this?" is a question about the local disk with no round
// trip, because the store is content-addressed: an item names the digest of
// the bytes it wants, so the same file in several items, or in several
// playlists, is one file. That is the property the service's own naming gives
// up -- two uploads of identical bytes are two identifiers there -- and it
// matters more on a screen than on a server, because the disk is smaller and
// nobody logs in to tidy it.

// largestMedia is the most that will be read for one file. A wall's video is
// tens of megabytes; this is where a mistake or something hostile stops.
const largestMedia = 512 << 20

// WithMedia gives the reporter somewhere to keep the files a playlist refers
// to. Without it a playlist still arrives and its pages still show; only
// uploaded pictures and videos are missing, which is what a build with no
// store should do rather than refusing to run.
func (self *Reporter) WithMedia(store *media.Store) *Reporter {
	self.media = store
	return self
}

// fetchMedia makes sure every file this playlist refers to is on the disk, and
// returns how many are still missing.
//
// A file that will not fetch does not hold up the rest: the playlist is applied
// either way, and the item with the missing file shows nothing while every
// other item works. One blank item is a much smaller failure than a playlist
// that never arrives, and it is visible on the wall rather than only in a log.
func (self *Reporter) fetchMedia(ctx context.Context, client *http.Client, items []deviceItem) int {
	if self.media == nil {
		return 0
	}

	missing := 0
	for _, slide := range items {
		if slide.Media == nil || slide.Media.File == "" {
			continue
		}
		if _, err := self.media.Details(slide.Media.File); err == nil {
			// Already held. No request, whatever the playlist has done since:
			// reordering items or changing a duration fetches nothing.
			continue
		}
		if slide.Media.MediaID == "" {
			log.Warningf("the service named a file this device does not have (%s) "+
				"without saying what to fetch it by", slide.Media.File)
			missing++
			continue
		}

		if err := self.fetchOne(ctx, client, slide.Media); err != nil {
			log.Warningf("cannot fetch %s: %s", describeMedia(slide.Media), err)
			missing++
		}
	}
	return missing
}

// fetchOne downloads one file and stores it, checking that the bytes are the
// ones that were asked for.
func (self *Reporter) fetchOne(ctx context.Context, client *http.Client, wanted *deviceItemMedia) error {
	fetching, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	request, err := http.NewRequestWithContext(fetching, http.MethodGet,
		"http://"+tunnelHost+"/api/v1/device/media/"+wanted.MediaID, nil)
	if err != nil {
		return err
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("the service answered %s", response.Status)
	}

	// Streamed straight into the store, which hashes as it writes rather than
	// needing to know anything before the first byte goes out. So a body
	// arriving over the tunnel behaves exactly as a file on disk would, and
	// nothing has to be held in memory to find out what it is.
	stored, err := self.media.Add(wanted.Name, response.Header.Get("Content-Type"),
		io.LimitReader(response.Body, largestMedia))
	if err != nil {
		return err
	}

	// The name the store gave it is a digest of what actually arrived. If that
	// is not what the playlist asked for, the transfer was truncated or the
	// bytes are not the ones meant, and keeping them would mean a screen
	// playing half a video for ever with everything reporting success.
	if stored.File != wanted.File {
		if err := self.media.Remove(stored.File); err != nil {
			log.Debugf("cannot remove a file that arrived wrong: %s", err)
		}
		return fmt.Errorf("the bytes that arrived are %s, not %s", stored.File, wanted.File)
	}

	// The service also sends the whole digest, of which the name is the first
	// half. Checked when it is there, because half a digest is a weaker
	// promise than a whole one and the whole one costs nothing to compare.
	if full := strings.TrimSpace(response.Header.Get("Digest")); full != "" {
		if err := sameDigest(full, stored.File); err != nil {
			if err := self.media.Remove(stored.File); err != nil {
				log.Debugf("cannot remove a file that arrived wrong: %s", err)
			}
			return err
		}
	}

	log.Noticef("fetched %s from the service (%d bytes)", describeMedia(wanted), stored.Size)
	return nil
}

// sameDigest checks the full digest the service sent against the name the
// store chose, which is its first thirty-two characters.
func sameDigest(full, name string) error {
	full = strings.ToLower(full)
	if _, err := hex.DecodeString(full); err != nil {
		// Not something to fail a good transfer over: a header this device
		// cannot read says nothing about the bytes.
		log.Debugf("the service sent a digest that is not hexadecimal: %q", full)
		return nil
	}
	if len(full) < len(name) {
		log.Debugf("the service sent a digest shorter than a file name: %q", full)
		return nil
	}
	if full[:len(name)] != name {
		return fmt.Errorf("the digest says %s and the bytes are %s", full[:len(name)], name)
	}
	return nil
}

func describeMedia(one *deviceItemMedia) string {
	if one.Name != "" {
		return fmt.Sprintf("%q (%s)", one.Name, one.File)
	}
	return one.File
}
