package network

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/atomicfile"
	"github.com/ziyan/cue/internal/util/executable"
)

// ScanStandalone looks for wireless networks on an interface that nothing is
// currently driving.
//
// Scan talks to a running wpa_supplicant, which is the normal case: the daemon
// starts one for every interface it manages. Setting a device up over the air
// is not the normal case. The device has been told to manage nothing, so no
// supplicant is running, and the first thing that happens is a scan -- before
// the radio becomes an access point, because a radio cannot search every
// channel while it is busy advertising on one.
//
// So this starts a supplicant of its own, asks it, and stops it again. The
// configuration it writes has no networks in it and never will: it exists to
// give the driver something to talk to for a few seconds.
func ScanStandalone(ctx context.Context, store *config.Store, interfaceName string) ([]WirelessNetwork, error) {
	// If something is already driving this interface, use it rather than
	// starting a second supplicant, which would fight the first.
	if found, err := Scan(interfaceName); err == nil {
		return found, nil
	}

	if err := os.MkdirAll(controlDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("network: cannot create %s: %w", controlDirectory, err)
	}

	filename := filepath.Join(store.Current().Paths.State, "wpa_supplicant-scan-"+interfaceName+".conf")
	content := "# Written by cue to look for networks before setting this device up.\n" +
		"ctrl_interface=" + controlDirectory + "\n" +
		"update_config=0\n"
	if err := atomicfile.Write(filename, []byte(content), 0o600); err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(filename) }()

	binary, err := executable.Resolve("wpa_supplicant", "/usr/sbin/wpa_supplicant", "/sbin/wpa_supplicant")
	if err != nil {
		return nil, fmt.Errorf("network: this image has no wpa_supplicant: %w", err)
	}

	process := supervise.New(&supervise.Settings{
		Name:          "wpa_supplicant scan " + interfaceName,
		Path:          binary,
		Arguments:     []string{"-i", interfaceName, "-c", filename, "-D", "nl80211", "-C", controlDirectory},
		Restart:       false,
		Ready:         func(context.Context) error { return supplicantReady(interfaceName) },
		ReadyTimeout:  15 * time.Second,
		CaptureOutput: true,
		Environment:   supervise.Inherit(),
	})

	process.Start(ctx)
	defer process.Stop(context.Background())

	readyContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := process.WaitReady(readyContext); err != nil {
		return nil, fmt.Errorf("network: cannot drive %s to look for networks: %w", interfaceName, err)
	}

	// A scan takes a few seconds and the first answer is often the results of
	// no scan at all, so it is asked for repeatedly until something comes back
	// or the time runs out. An empty list is a real answer -- a room with no
	// wireless in it -- so what is waited for is a scan that found something,
	// with an empty result accepted at the end rather than treated as failure.
	deadline := time.Now().Add(12 * time.Second)
	var found []WirelessNetwork
	for time.Now().Before(deadline) {
		found, err = Scan(interfaceName)
		if err == nil && len(found) > 0 {
			return found, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return found, err
}
