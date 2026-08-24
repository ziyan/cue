// Package display is the X client that arranges the picture: it asks the X
// server which physical connectors exist, chooses a mode for each one, sizes
// the drawing surface to fit, and turns off everything that would make the
// screen go dark on its own.
//
// It speaks the X protocol directly rather than running xrandr and xset,
// because the container image contains no shell and no X command line tools —
// and because parsing xrandr's output to find out what happened is worse than
// reading the reply that produced it.
//
// The X vocabulary, once, in plain language:
//
//   - An **output** is a physical connector: HDMI-1, DP-2, eDP-1. It may or
//     may not have a monitor plugged into it.
//   - A **mode** is a resolution and refresh rate a monitor says it accepts,
//     for example 1920x1080 at 60Hz. A monitor advertises a list of them and
//     marks one as preferred.
//   - A **CRTC** is a piece of hardware that scans part of the drawing
//     surface out to one or more outputs. There are fewer CRTCs than outputs,
//     which is why a machine with four connectors can only drive two screens.
//   - The **screen** is the single drawing surface all of this shows part of.
//     Two monitors side by side are two rectangles of one screen.
package display

import (
	"context"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/dpms"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
)

var log = logging.MustGetLogger("display")

// Display is a connection to an X server.
type Display struct {
	connection *xgb.Conn
	root       xproto.Window

	// randrAvailable and dpmsAvailable record which extensions the server
	// actually has. Xvfb, which the development configuration and the smoke
	// test use, has a reduced RandR and no DPMS at all, and the daemon has to
	// work there as well as on real hardware.
	randrAvailable bool
	dpmsAvailable  bool

	// hidden is the empty cursor, made the first time the pointer is hidden
	// and kept because a cursor is a server resource and remaking one every
	// few seconds would leak them.
	hidden xproto.Cursor
}

// Output is one physical connector, as the daemon reports it.
type Output struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Enabled   bool   `json:"enabled"`

	// Width, Height, X and Y are the rectangle of the screen this output is
	// currently showing, in pixels. All zero when the output is off.
	Width  int `json:"width"`
	Height int `json:"height"`
	X      int `json:"x"`
	Y      int `json:"y"`

	// Rotation is "normal", "left", "right" or "inverted".
	Rotation string `json:"rotation"`

	// Primary marks the output the browser window is placed on.
	Primary bool `json:"primary"`

	// CurrentMode and PreferredMode are described as "1920x1080@60".
	CurrentMode   string `json:"currentMode"`
	PreferredMode string `json:"preferredMode"`

	// Modes is everything the monitor said it accepts, largest first.
	Modes []string `json:"modes"`

	// PhysicalWidthMillimetres and PhysicalHeightMillimetres come from the
	// monitor's EDID and are how the interface can say "27 inch".
	PhysicalWidthMillimetres  int `json:"physicalWidthMillimetres"`
	PhysicalHeightMillimetres int `json:"physicalHeightMillimetres"`
}

// Screen is the drawing surface every output shows part of.
type Screen struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Open connects to an X server over its Unix socket, authenticating with the
// cookie the daemon generated when it started the server. The socket is
// addressed directly rather than through the DISPLAY environment variable so
// that nothing about this depends on the daemon's own environment.
//
// The context bounds the whole of it, connection and handshake both. That
// second part matters more than it looks: an X server that accepts the
// connection and then never finishes the handshake — which is what a
// half-wedged one does — would otherwise block forever, and the caller
// blocking forever here is the watchdog, whose entire job is to notice that
// the X server has stopped answering.
func Open(ctx context.Context, displayNumber int, cookie []byte) (*Display, error) {
	socket := SocketPath(displayNumber)

	dialer := net.Dialer{Timeout: handshakeTimeout}
	connection, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("display: cannot reach the X server at %s: %w", socket, err)
	}

	deadline := time.Now().Add(handshakeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("display: %w", err)
	}

	xConnection, err := xgb.NewConnNetWithCookieHex(connection, hexadecimal(cookie))
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("display: the X server at %s refused the connection: %w", socket, err)
	}

	// The handshake is done. Every request after this is answered promptly by
	// a working server and never at all by a broken one, and the caller's own
	// context is what bounds the wait for that.
	if err := connection.SetDeadline(time.Time{}); err != nil {
		xConnection.Close()
		return nil, fmt.Errorf("display: %w", err)
	}

	self := &Display{
		connection: xConnection,
		root:       xproto.Setup(xConnection).DefaultScreen(xConnection).Root,
	}

	if err := randr.Init(xConnection); err == nil {
		if _, err := randr.QueryVersion(xConnection, 1, 5).Reply(); err == nil {
			self.randrAvailable = true
		}
	}
	if err := dpms.Init(xConnection); err == nil {
		if _, err := dpms.GetVersion(xConnection, 1, 1).Reply(); err == nil {
			self.dpmsAvailable = true
		}
	}

	return self, nil
}

// handshakeTimeout bounds connecting and authenticating. Five seconds is
// several times longer than a working server takes and far shorter than a
// person will wait before deciding the screen is broken.
const handshakeTimeout = 5 * time.Second

// SocketPath is where an X server of the given number listens. Every X server
// since the 1980s has used this path, and the browser, the VNC server and the
// daemon all have to agree on it.
func SocketPath(displayNumber int) string {
	return fmt.Sprintf("/tmp/.X11-unix/X%d", displayNumber)
}

// AbstractSocketPath is the other way a client reaches an X server on Linux:
// an abstract socket, named like the file but living in the kernel rather than
// the filesystem. Xlib and xcb try it first, which matters here because the
// abstract namespace belongs to the network namespace, not the mount
// namespace — so a container started with the host's network can reach the
// host's X server even though it cannot see its socket file.
func AbstractSocketPath(displayNumber int) string {
	return "@" + SocketPath(displayNumber)
}

// SomethingIsAnsweringOn reports whether an X server already has this display
// number, and by which socket.
//
// It opens the socket and does not authenticate: the question is whether
// anybody is there, and a server that refuses our cookie is emphatically there.
// Probing with the cookie would answer the opposite question and miss exactly
// the case that matters — somebody else's X server.
//
// Both sockets are tried, and the abstract one first, because that is the one
// a container in the host's network namespace shares with the machine.
func SomethingIsAnsweringOn(displayNumber int) (where string, found bool) {
	for _, candidate := range []string{
		AbstractSocketPath(displayNumber),
		SocketPath(displayNumber),
	} {
		connection, err := net.DialTimeout("unix", candidate, time.Second)
		if err != nil {
			continue
		}
		_ = connection.Close()
		return candidate, true
	}
	return "", false
}

// Name is the value of DISPLAY that reaches this server.
func Name(displayNumber int) string {
	return fmt.Sprintf(":%d", displayNumber)
}

// Close drops the connection.
func (self *Display) Close() {
	if self.connection != nil {
		self.connection.Close()
		self.connection = nil
	}
}

// Ping does one round trip. The watchdog uses it to tell "the browser is
// wedged" from "the thing the browser draws into is wedged", which need
// different remedies.
func (self *Display) Ping() error {
	if _, err := xproto.GetInputFocus(self.connection).Reply(); err != nil {
		return fmt.Errorf("display: the X server did not answer: %w", err)
	}
	return nil
}

// Screen is the current size of the drawing surface.
// Screen is how big the screen is now.
//
// The size is asked of the root window rather than read out of the connection
// setup. The setup block is sent once, when the client connects, and is never
// updated: after RandR resizes the screen it still reports whatever the size
// was at connect time. That is not a corner case here, because resizing the
// screen is the first thing this daemon does — on a machine whose X server
// started at 1152x864 and was set to 1280x1024, the browser was sized to the
// old figure and sat in the corner of the screen with a black band down two
// sides of it. Everything reported success, including the line saying which
// mode had been set.
func (self *Display) Screen() Screen {
	if geometry, err := xproto.GetGeometry(self.connection, xproto.Drawable(self.root)).Reply(); err == nil {
		return Screen{Width: int(geometry.Width), Height: int(geometry.Height)}
	}
	// A server that will not answer for its own root window is one this is
	// about to fail on anyway; the setup values are better than zero.
	setup := xproto.Setup(self.connection).DefaultScreen(self.connection)
	return Screen{Width: int(setup.WidthInPixels), Height: int(setup.HeightInPixels)}
}

// Outputs reports every connector the server knows about.
func (self *Display) Outputs() ([]Output, error) {
	if !self.randrAvailable {
		// A server without RandR has exactly one screen and no way to ask
		// about connectors. Report the screen as a single output so that
		// everything above this can be written once.
		screen := self.Screen()
		return []Output{{
			Name:        "screen",
			Connected:   true,
			Enabled:     true,
			Width:       screen.Width,
			Height:      screen.Height,
			Rotation:    "normal",
			Primary:     true,
			CurrentMode: fmt.Sprintf("%dx%d", screen.Width, screen.Height),
		}}, nil
	}

	resources, err := randr.GetScreenResourcesCurrent(self.connection, self.root).Reply()
	if err != nil {
		return nil, fmt.Errorf("display: cannot read the screen resources: %w", err)
	}

	modes := indexModes(resources)

	primary := randr.Output(0)
	if reply, err := randr.GetOutputPrimary(self.connection, self.root).Reply(); err == nil {
		primary = reply.Output
	}

	outputs := make([]Output, 0, len(resources.Outputs))
	for _, identifier := range resources.Outputs {
		information, err := randr.GetOutputInfo(self.connection, identifier, resources.ConfigTimestamp).Reply()
		if err != nil {
			log.Warningf("cannot read output %d: %s", identifier, err)
			continue
		}

		output := Output{
			Name:                      string(information.Name),
			Connected:                 information.Connection == randr.ConnectionConnected,
			Primary:                   identifier == primary,
			PhysicalWidthMillimetres:  int(information.MmWidth),
			PhysicalHeightMillimetres: int(information.MmHeight),
			Rotation:                  "normal",
		}

		for index, mode := range information.Modes {
			description, found := modes[mode]
			if !found {
				continue
			}
			output.Modes = append(output.Modes, description.String())
			if index < int(information.NumPreferred) && output.PreferredMode == "" {
				output.PreferredMode = description.String()
			}
		}
		if output.PreferredMode == "" && len(output.Modes) > 0 {
			output.PreferredMode = output.Modes[0]
		}

		if information.Crtc != 0 {
			if crtc, err := randr.GetCrtcInfo(self.connection, information.Crtc, resources.ConfigTimestamp).Reply(); err == nil && crtc.Mode != 0 {
				output.Enabled = true
				output.Width = int(crtc.Width)
				output.Height = int(crtc.Height)
				output.X = int(crtc.X)
				output.Y = int(crtc.Y)
				output.Rotation = rotationName(crtc.Rotation)
				if description, found := modes[crtc.Mode]; found {
					output.CurrentMode = description.String()
				}
			}
		}

		outputs = append(outputs, output)
	}

	sort.Slice(outputs, func(first, second int) bool { return outputs[first].Name < outputs[second].Name })
	return outputs, nil
}

// mode is one entry of the server's mode list.
type mode struct {
	identifier randr.Mode
	name       string
	width      int
	height     int
	rate       float64
}

// String describes a mode the way the interface shows it and the way an
// operator writes it in the configuration.
func (self mode) String() string {
	if self.rate <= 0 {
		return fmt.Sprintf("%dx%d", self.width, self.height)
	}
	return fmt.Sprintf("%dx%d@%.0f", self.width, self.height, self.rate)
}

func indexModes(resources *randr.GetScreenResourcesCurrentReply) map[randr.Mode]mode {
	modes := map[randr.Mode]mode{}
	names := resources.Names
	offset := 0
	for _, information := range resources.Modes {
		name := ""
		if offset+int(information.NameLen) <= len(names) {
			name = string(names[offset : offset+int(information.NameLen)])
		}
		offset += int(information.NameLen)
		modes[randr.Mode(information.Id)] = mode{
			identifier: randr.Mode(information.Id),
			name:       name,
			width:      int(information.Width),
			height:     int(information.Height),
			rate:       refreshRate(information),
		}
	}
	return modes
}

// refreshRate works out how many times a second a mode is drawn. The mode
// carries a pixel clock and the total number of pixels in a frame including
// the blanking intervals, and the rate is one divided by the other; the
// interlace and double-scan flags change what a "frame" means.
func refreshRate(information randr.ModeInfo) float64 {
	total := float64(information.Htotal) * float64(information.Vtotal)
	if total == 0 {
		return 0
	}
	rate := float64(information.DotClock) / total
	const doubleScan = 0x20
	const interlace = 0x10
	if information.ModeFlags&doubleScan != 0 {
		rate /= 2
	}
	if information.ModeFlags&interlace != 0 {
		rate *= 2
	}
	return rate
}

func rotationName(rotation uint16) string {
	switch {
	case rotation&uint16(randr.RotationRotate90) != 0:
		return "left"
	case rotation&uint16(randr.RotationRotate180) != 0:
		return "inverted"
	case rotation&uint16(randr.RotationRotate270) != 0:
		return "right"
	default:
		return "normal"
	}
}

func rotationValue(name string) uint16 {
	switch name {
	case "left":
		return uint16(randr.RotationRotate90)
	case "inverted":
		return uint16(randr.RotationRotate180)
	case "right":
		return uint16(randr.RotationRotate270)
	default:
		return uint16(randr.RotationRotate0)
	}
}

func hexadecimal(data []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(data)*2)
	for _, value := range data {
		out = append(out, digits[value>>4], digits[value&0x0f])
	}
	return string(out)
}

// settingsFor finds the configuration entry that governs one output: an entry
// naming it exactly, or failing that the wildcard entry.
func settingsFor(settings *config.Display, name string) *config.Output {
	var wildcard *config.Output
	for index := range settings.Outputs {
		output := &settings.Outputs[index]
		if output.Name == name {
			return output
		}
		if output.Name == "*" {
			wildcard = output
		}
	}
	return wildcard
}
