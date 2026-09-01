package config

// Configuration is the whole of cue.yaml. It is the single source of truth
// for everything an operator can set: nothing is configured by a command line
// flag that is not also here, and there is no settings table anywhere else.
//
// Both ends write it. A person edits the file and sends SIGHUP; the web
// interface edits it through the API and the daemon rewrites it atomically.
// Store owns that; see store.go.
type Configuration struct {
	Device   Device   `yaml:"device" json:"device"`
	Log      Log      `yaml:"log" json:"log"`
	Paths    Paths    `yaml:"paths" json:"paths"`
	Display  Display  `yaml:"display" json:"display"`
	Browser  Browser  `yaml:"browser" json:"browser"`
	Playlist Playlist `yaml:"playlist" json:"playlist"`
	Watchdog Watchdog `yaml:"watchdog" json:"watchdog"`
	VNC      VNC      `yaml:"vnc" json:"vnc"`
	Web      Web      `yaml:"web" json:"web"`
	Network  Network  `yaml:"network" json:"network"`
	Audio    Audio    `yaml:"audio" json:"audio"`
	Time     Time     `yaml:"time" json:"time"`
	Service  Service  `yaml:"service" json:"service"`
	Upgrade  Upgrade  `yaml:"upgrade" json:"upgrade"`

	// IgnoredSettings are the names in the file that this version has no
	// setting for. They are not fatal — a device already in service has the
	// settings of the version that wrote its file, and refusing to start over
	// one that has since been removed turns an upgrade into a black screen on
	// a machine nobody can reach — but they are not silent either. The
	// interface shows them, because a mistyped key and a setting that does
	// nothing look identical from in front of the screen.
	//
	// Not written back: this is what was read, not what to keep.
	IgnoredSettings []string `yaml:"-" json:"ignoredSettings,omitempty"`
}

// Device is what this screen is, as a human would describe it.
type Device struct {
	// Name is shown in the web interface, in the window title and, if the
	// It is not an identifier:
	// Identifier is.
	Name string `yaml:"name" json:"name"`

	// Identifier is generated once, on the first run, and never changes. The
	// Nothing generates it twice, and regenerating it by hand would make the
	// device look like a different one to anything keeping records about it.
	Identifier string `yaml:"identifier" json:"identifier"`

	// Location is free text an operator can use to say where the screen is.
	Location string `yaml:"location,omitempty" json:"location"`

	// Timezone names the zone used for everything the device displays and
	// logs, for example "Asia/Tokyo". Empty means UTC.
	//
	// This is the daemon's own idea of local time, and the browser's with it,
	// so a dashboard that shows a clock shows this zone. It does not touch the
	// machine outside the container and could not: the host's /etc/localtime
	// is not mounted. Set this to London on a host set to New York and the
	// screen reads London while "date" on the machine reads New York — the
	// same instant, labelled differently. The instant itself comes from the
	// clock, which is shared, and which is what the time section is about.
	Timezone string `yaml:"timezone,omitempty" json:"timezone"`

	// Language is what this screen speaks: the menu somebody opens at the
	// display, and the pages it shows. A language tag such as "en", "zh" or
	// "ja"; empty leaves the browser to decide for itself, which is usually
	// the machine's locale.
	//
	// It is the default rather than the last word. Somebody standing at the
	// screen can pick another from the menu, and that choice is written back
	// here -- so a screen ends up speaking whatever the last person to decide
	// chose, whether they decided in this file or in front of it.
	//
	// It reaches the pages as well as the menu, which it did not at first:
	// Chromium is given --lang and --accept-lang, so a dashboard that speaks
	// more than one language is asked for this one. Chromium reads those once
	// at start-up, so changing this restarts the browser.
	//
	// It is kept here as well as in the browser because a browser profile is
	// not a durable place. Wiping it is one of the things the watchdog does
	// when a screen wedges, and a device that forgot which language it spoke
	// every time it recovered would be a poor thing to live with.
	Language string `yaml:"language,omitempty" json:"language"`
}

// Service is the hosted side this device may be attached to, and the
// credential it holds once it is.
//
// A device works entirely on its own with none of this set, which is the
// normal state and the one everything else in this file assumes. Linking is
// something an operator chooses, from the menu or the interface, and it adds
// somewhere to report to rather than somewhere to be run from.
type Service struct {
	// Address is where the service lives, as a URL a phone could open.
	// DefaultServiceAddress unless this file says otherwise.
	//
	// Settable in the file but not from the web interface, and the difference
	// is deliberate. Ordinary operators have one service and no reason to name
	// it; offering the field made an empty box the first thing on the page and
	// invited somebody to type something that could only be wrong. It stays in
	// the file because a device does sometimes need pointing at a staging
	// service or a deployment that is not the public one, and that is a
	// decision made by whoever installs the device rather than by whoever
	// finds the page.
	Address string `yaml:"address,omitempty" json:"address"`

	// Secret is what the service issued when this device was linked, and what
	// it presents when it connects. Never leaves the device again: it renders
	// as a placeholder everywhere outside this file, and RestoreSecrets is
	// what stops the interface erasing it by saving a form.
	Secret Secret `yaml:"secret,omitempty" json:"secret"`

	// Account and DeviceID are what the service said this device became. They
	// are held for the interface to show -- somebody who has just linked a
	// screen wants to see which account it went to, and somebody looking at it
	// a year later wants to know before they unlink it.
	//
	// Account is masked by the service before it is sent, because this one is
	// displayed on the screen itself: a wall in a lobby is no place for
	// somebody's email address.
	Account  string `yaml:"account,omitempty" json:"account"`
	DeviceID string `yaml:"deviceId,omitempty" json:"deviceId"`

	// Name is what the service calls this device, which is not always what
	// this device calls itself. An account cannot have two devices of the same
	// name, so a second screen called "carbon" is recorded there as "carbon
	// 2". The service's name is the one that matches the two systems up, so it
	// is kept and shown rather than assuming the local name carried.
	Name string `yaml:"name,omitempty" json:"name"`
}

// DefaultServiceAddress is where a device reports to unless its file says
// otherwise. Compiled in rather than asked for: it is the same for every
// device anybody will ever install, and a setting whose right answer is always
// the same is a setting that only offers a way to be wrong.
const DefaultServiceAddress = "https://cue.sh"

// IsConfigured reports whether there is a service to link to at all.
func (self *Service) IsConfigured() bool {
	return self.Address != ""
}

// IsLinked reports whether this device holds a credential.
func (self *Service) IsLinked() bool {
	return self.Secret.IsSet()
}

// Log controls what the daemon writes to its standard error, which in a
// container is what "docker logs" shows.
type Log struct {
	// Level is one of DEBUG, INFO, NOTICE, WARNING, ERROR or CRITICAL.
	Level string `yaml:"level" json:"level"`

	// BrowserOutput passes Chromium's own logging through to the daemon's
	// log. It is off by default: Chromium at any verbosity above the default
	// writes tens of lines a second about WebRTC alone, which on a device
	// with a small disk is the difference between a week of logs and an hour
	// of them.
	BrowserOutput bool `yaml:"browserOutput" json:"browserOutput"`
}

// Paths are the directories the daemon owns. They have working defaults and
// exist mostly so that a developer can run the daemon on their own machine
// without being root.
type Paths struct {
	// State survives a restart: the browser profile, the device identifier
	// is kept.
	State string `yaml:"state" json:"state"`

	// Runtime does not survive a restart: the X socket, the X authority
	// cookie, the browser's disk cache and the sound server's files.
	Runtime string `yaml:"runtime" json:"runtime"`
}

// Display is how the picture reaches the screen.
type Display struct {
	// Server is "xorg" to drive real hardware or "xvfb" to render into
	// memory. A development machine and the continuous integration smoke test
	// use xvfb; a device uses xorg.
	Server string `yaml:"server" json:"server"`

	// Number is the X display number, so ":0" for 0. There is no reason to
	// change it except to run two daemons on one machine.
	Number int `yaml:"number" json:"number"`

	// VirtualTerminal is the console the X server draws on, so 2 means
	// /dev/tty2. It is set rather than left to the server to choose because
	// of how that choice interacts with a container: the server asks the
	// kernel for a free console, is told a number, and then opens
	// /dev/tty<number> — which fails unless that exact device was passed
	// through. Naming it here means one device has to be passed through
	// instead of all of them.
	//
	// Zero lets the server choose, which needs the whole of /dev.
	VirtualTerminal int `yaml:"virtualTerminal" json:"virtualTerminal"`

	// Cursor is what the mouse pointer does: "hidden", "auto" or "always".
	//
	// "auto" is the default and is what a screen wants. A pointer parked in
	// the middle of a dashboard looks broken and is the sort of thing people
	// photograph and send to you; a pointer that cannot be made to appear at
	// all makes a screen with a mouse or a touchscreen impossible to use,
	// because there is no way to see where you are. So: nothing until
	// somebody moves it, and nothing again once they stop.
	//
	// "hidden" starts the X server with no cursor at all, which is the old
	// behaviour and cannot be undone while it runs. true and false still read
	// as "always" and "hidden": every device in service has one of those
	// written in its file.
	Cursor CursorMode `yaml:"cursor" json:"cursor"`

	// Wallpaper paints the project's mark on the root window, which is what
	// the screen shows in the seconds before the browser has drawn anything
	// and again if it goes away. Off means whatever the X server leaves
	// behind, which is black on most drivers and the grey stipple pattern
	// from 1987 on some — indistinguishable, on a wall, from a machine that
	// failed to boot.
	Wallpaper bool `yaml:"wallpaper" json:"wallpaper"`

	// CursorIdleTimeout is how long the pointer stays visible after it stops
	// moving, in "auto".
	CursorIdleTimeout Duration `yaml:"cursorIdleTimeout" json:"cursorIdleTimeout"`

	// Framebuffer forces the size of the drawing surface, for example
	// "1920x1080". Empty means the size the outputs need. Set it when a
	// television reports nonsense in its EDID.
	Framebuffer string `yaml:"framebuffer,omitempty" json:"framebuffer"`

	// Modeline adds a display mode the monitor did not offer, in the format
	// xrandr's --newmode takes: a pixel clock followed by eight numbers and
	// two sync polarities. Needed for televisions with a broken EDID.
	Modeline string `yaml:"modeline,omitempty" json:"modeline"`

	// ModeName names the mode Modeline defines, so that an output can select
	// it. Defaults to "cue".
	ModeName string `yaml:"modeName,omitempty" json:"modeName"`

	// Mirror shows the same picture on every screen that is plugged in,
	// rather than laying them out side by side into one wide desktop.
	//
	// On by default, because this is a daemon for showing one page on a
	// screen. A second screen plugged into one of these is almost always
	// another place to show the same thing -- a second monitor in the next
	// room, a projector in a meeting room -- and laying them out side by side
	// gives a page stretched across two screens with half of it on each,
	// which nobody wants and which looks like a fault.
	//
	// It needs a mode both screens have. A laptop panel and a television
	// usually share one -- the panel's native size is normally in the
	// television's list -- and when they share nothing at all this falls back
	// to side by side and says so, rather than blanking one of them.
	//
	// Turn it off for a video wall, where two screens really are one wide
	// picture. Per-output "position" is ignored while this is on: mirroring
	// is what "both at the same place" means.
	Mirror bool `yaml:"mirror" json:"mirror"`

	// Outputs configures the physical connectors. An entry whose Name is "*"
	// applies to every connected output that no other entry names, which is
	// what makes the default configuration work on a machine nobody has
	// looked at yet.
	Outputs []Output `yaml:"outputs" json:"outputs"`

	// BlankAfter turns the screen off after this much idle time. Zero, the
	// default, never blanks: a display that goes dark because nobody has
	// touched its keyboard is a fault report waiting to happen.
	BlankAfter Duration `yaml:"blankAfter" json:"blankAfter"`

	// XorgConfiguration is written verbatim into the X server's configuration
	// directory. It is an escape hatch for the hardware nobody anticipated —
	// a television that needs ModeValidation relaxed, a driver that needs an
	// option — and it is deliberately raw text rather than a set of fields,
	// because generating an xorg.conf is how screens end up black.
	XorgConfiguration string `yaml:"xorgConfiguration,omitempty" json:"xorgConfiguration"`

	// ExtraArguments are appended to the X server's command line.
	ExtraArguments []string `yaml:"extraArguments,omitempty" json:"extraArguments"`

	// ReconcileInterval is how often the daemon compares the outputs it can
	// see against this configuration and fixes any difference. This is what
	// makes unplugging and replugging an HDMI cable work without anybody
	// doing anything.
	ReconcileInterval Duration `yaml:"reconcileInterval" json:"reconcileInterval"`
}

// Output is one physical connector, named as the X server names it: HDMI-1,
// DP-2, eDP-1, and so on. "*" matches any output not named by another entry.
type Output struct {
	Name string `yaml:"name" json:"name"`

	// Mode is "preferred" for whatever the monitor says it wants, "off" to
	// leave the connector dark, or an explicit size such as "1920x1080".
	Mode string `yaml:"mode" json:"mode"`

	// Rate is the refresh rate in hertz to prefer when several modes share a
	// size. Zero takes the highest the mode list offers.
	Rate float64 `yaml:"rate,omitempty" json:"rate"`

	// Position places this output inside the framebuffer, as "0x0". Only
	// interesting with more than one screen.
	Position string `yaml:"position,omitempty" json:"position"`

	// Rotate is "normal", "left", "right" or "inverted". Portrait screens in
	// lobbies are the reason this exists.
	Rotate string `yaml:"rotate,omitempty" json:"rotate"`

	// Primary marks the output the browser window is placed on.
	Primary bool `yaml:"primary,omitempty" json:"primary"`
}

// Browser is how Chromium is started. What it shows is Playlist's business.
type Browser struct {
	// Binary is the browser executable. The image ships Chromium; a developer
	// on a machine with a differently named build can point at it.
	Binary string `yaml:"binary,omitempty" json:"binary"`

	// User is the account Chromium runs as. The daemon and the X server need
	// to be root to touch the graphics hardware, but Chromium must not be:
	// it refuses to enable its own sandbox as root, and a kiosk renders
	// whatever the network serves it. Empty means "do not change user",
	// which is what a developer running the daemon as themselves wants.
	User string `yaml:"user,omitempty" json:"user"`

	// Sandbox keeps Chromium's process sandbox on. Turning it off is
	// sometimes the only way to run inside a restricted container, and it is
	// a real reduction in safety, so it is spelled out here rather than
	// hidden in a list of arguments.
	Sandbox bool `yaml:"sandbox" json:"sandbox"`

	// IgnoreCertificateErrors loads pages whose certificate does not verify.
	// Every appliance on a private network with a self-signed certificate is
	// the reason this is here, and it is off by default because it silently
	// removes the protection TLS was there to give.
	IgnoreCertificateErrors bool `yaml:"ignoreCertificateErrors" json:"ignoreCertificateErrors"`

	// DarkMode asks pages to render dark. It is on by default, because a
	// screen on a wall is usually in a room where a page of white at full
	// brightness is the brightest thing in it — and because a dashboard that
	// blinds people at night gets unplugged.
	//
	// It sets the browser's own preference, which a page reads through the
	// prefers-color-scheme media query. A page with no dark styling of its
	// own is unaffected; a page that has one follows it.
	DarkMode bool `yaml:"darkMode" json:"darkMode"`

	// DeviceScaleFactor is how many device pixels the browser draws for each
	// pixel a page asks for. 1 means the page gets the screen it actually has.
	//
	// Left to itself the browser works this out from the DPI the X server
	// reports, which comes from the physical size of the panel, which comes
	// from whatever the panel says about itself over EDID. On a laptop panel
	// that is a genuinely high number and the browser doubles everything; on a
	// television it is often nonsense. The screen this was found on reported a
	// size that worked out to 72 DPI, so the browser chose 0.75: the window
	// filled the screen, the page laid itself out at 3412x1918, and it was
	// drawn shrunk into the corner with black around two sides. Nothing was
	// broken and nothing said anything.
	//
	// A screen on a wall wants none of that. It has a fixed number of pixels
	// and a dashboard designed for pixels, so the default is 1 and the panel's
	// opinion is not consulted. Set it higher for a screen somebody stands
	// close to.
	DeviceScaleFactor float64 `yaml:"deviceScaleFactor" json:"deviceScaleFactor"`

	// ForceDarkContent darkens pages that have no dark theme of their own, by
	// inverting their colours.
	//
	// DarkMode on its own tells a page we would prefer dark and leaves the
	// page to decide. A page with a theme setting of its own — stored in an
	// account somewhere, defaulting to light — ignores that entirely, and on
	// a wall in a dark room it is the brightest thing in the room. This is
	// the hammer for that case: it is not as good as a page's own dark theme
	// where one exists, which is why it is off by default.
	ForceDarkContent bool `yaml:"forceDarkContent" json:"forceDarkContent"`

	// CertificateAuthorities are PEM certificates the browser should trust in
	// addition to the public ones, for the appliances on private networks
	// that sign their own. This is the answer to a dashboard a browser will
	// not open; IgnoreCertificateErrors is the other answer, and it is worse,
	// because it stops the browser checking anything at all.
	CertificateAuthorities []string `yaml:"certificateAuthorities,omitempty" json:"certificateAuthorities"`

	// CloseUnexpectedTabs closes windows the daemon did not open. A page that
	// calls window.open gets a window of its own and, with no window manager,
	// it is stacked in front of the one on the wall; without this, a screen
	// showing one page stays covered by it until somebody walks over.
	//
	// A window is given one cycle to close itself before it is closed here,
	// and what was closed is always logged.
	CloseUnexpectedTabs bool `yaml:"closeUnexpectedTabs" json:"closeUnexpectedTabs"`

	// EphemeralCache puts the browser's disk cache under the runtime
	// directory and empties it at every start. On by default: a corrupted
	// cache is a fault that survives every restart and presents as a page
	// that will not load for no visible reason, and a dashboard on a local
	// network loses nothing by fetching its assets again.
	EphemeralCache bool `yaml:"ephemeralCache" json:"ephemeralCache"`

	// There is deliberately no setting for the DevTools port. It was one
	// twice, and caused a different failure each time: fixed at 9222 the
	// daemon drove another container's browser, and when the default became 0
	// the devices already deployed went on using the 9222 written into their
	// files — where, by then, nothing could bind. The browser is always asked
	// for 0 and always found through the DevToolsActivePort file in its own
	// profile, which cannot resolve to anybody else's browser.

	// ExtraArguments are appended to the command line, for the settings
	// nobody anticipated. They are applied last and can override anything.
	ExtraArguments []string `yaml:"extraArguments,omitempty" json:"extraArguments"`
}

// ItemMedia is a picture or a video stored on this device.
type ItemMedia struct {
	// File is what the video is stored under, which is a digest of its own
	// contents. It is not a name anybody chose and not one anybody reads.
	File string `yaml:"file" json:"file"`

	// Name is what the file was called when it was uploaded, so the interface
	// has something to show that is not hexadecimal.
	Name string `yaml:"name,omitempty" json:"name"`

	// Kind is "video" or "picture". A video holds the screen until it ends; a
	// picture holds it for the ordinary rotation time, having no end of its
	// own. It is worked out when the file is uploaded rather than guessed at
	// from the name later.
	Kind string `yaml:"kind,omitempty" json:"kind"`

	// Sound plays this video with its sound. Off by default, and per item on
	// purpose: a screen on a wall that starts making noise because somebody
	// added a video is a bad surprise, and the obvious case -- one promotional
	// video with music among several silent dashboards -- cannot be expressed
	// by a single setting for the whole device.
	//
	// The device's own audio settings still apply. A device with sound
	// switched off stays silent whatever an item asks for. It means nothing
	// for a picture.
	Sound bool `yaml:"sound,omitempty" json:"sound"`
}

// Playlist is what the screen shows.
type Playlist struct {
	// Interval is how long each item is shown when it does not say otherwise.
	// Zero means never rotate, which is what a single-item playlist wants.
	Interval Duration `yaml:"interval" json:"interval"`

	Items []Item `yaml:"items" json:"items"`

	// MaximumUploadSize is the largest picture or video that may be uploaded.
	// It is here rather than fixed in the code because the sensible answer
	// depends on the disk of the machine, and the machine is one nobody logs
	// into to find out that it is full.
	MaximumUploadSize int64 `yaml:"maximumUploadSize" json:"maximumUploadSize"`

	// MaximumVideoSize is what MaximumUploadSize used to be called, still read
	// so that a file written by an older version keeps its setting. Normalize
	// moves it across and clears it, and it is never written back.
	MaximumVideoSize int64 `yaml:"maximumVideoSize,omitempty" json:"-"`
}

// Item is one page in the rotation.
type Item struct {
	// Identifier is generated once so that the interface can reorder and edit
	// items without the daemon losing track of which tab is which.
	Identifier string `yaml:"identifier" json:"identifier"`

	URL string `yaml:"url" json:"url"`

	// Title is what the interface calls this item. Empty means the page's own
	// title is used.
	Title string `yaml:"title,omitempty" json:"title"`

	// Duration overrides Playlist.Interval for this item.
	Duration Duration `yaml:"duration,omitempty" json:"duration"`

	// Reload fetches the page again each time the item comes round.
	// Dashboards that poll for themselves do not need it; ones that quietly
	// stop updating after a few hours do.
	Reload bool `yaml:"reload,omitempty" json:"reload"`

	// Disabled keeps the item in the configuration but out of the rotation,
	// which is what an operator wants while a site is down for maintenance.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled"`

	// Media, when set, makes this item a picture or a video the operator
	// uploaded rather than a page on the web. URL is ignored for such an item:
	// the daemon points the browser at its own player page instead.
	Media *ItemMedia `yaml:"media,omitempty" json:"media"`

	// Video is what Media used to be called, and is still read so that a file
	// written by an older version keeps its items. Normalize moves it into
	// Media and clears it, and it is never written back.
	Video *ItemMedia `yaml:"video,omitempty" json:"-"`

	// Login, when set, keeps this item logged in. See Login.
	Login *Login `yaml:"login,omitempty" json:"login"`

	// Dismiss removes things that appear on top of the page and stay there:
	// a cookie banner, a "what's new" announcement, a survey invitation. On a
	// screen nobody touches, one of these covers the dashboard until somebody
	// walks over and clicks it, which can be weeks.
	Dismiss []Dismiss `yaml:"dismiss,omitempty" json:"dismiss"`
}

// Dismiss is one thing to get rid of when it appears.
type Dismiss struct {
	// Selector is the element to act on. For a dialog, this is its close
	// button or its "got it" button — the thing a person would click.
	Selector string `yaml:"selector" json:"selector"`

	// WhenTextMatches, when set, is a regular expression the element's text
	// must match before it is touched. It exists so that a selector as broad
	// as "button" can still be aimed at one particular dialog.
	WhenTextMatches string `yaml:"whenTextMatches,omitempty" json:"whenTextMatches"`

	// Hide covers the case where there is nothing to click: instead of
	// clicking, the element is given "display: none". Blunter, and it does
	// not tell the page the notice was seen, so the notice usually comes
	// back on the next load — but it works on things that cannot be closed.
	Hide bool `yaml:"hide,omitempty" json:"hide"`
}

// Login describes how to get a page past a login form, and — more
// importantly — how to notice that it has been thrown back to one.
//
// The case this exists for: a camera dashboard whose session expires every
// few hours, after which the tab sits on a login page until somebody walks
// over with a keyboard. A login performed once when the tab opens does not
// help. So this is a rule the daemon re-evaluates on a timer: when the page
// looks like the login page, fill it in and submit it.
type Login struct {
	// WhenURLMatches is a regular expression tested against the tab's current
	// address. When it matches, the page is considered to need logging in.
	// A dashboard that redirects to "/login?redirect=/dashboard" is matched
	// by "/login".
	WhenURLMatches string `yaml:"whenUrlMatches,omitempty" json:"whenUrlMatches"`

	// WhenSelectorExists is a CSS selector that only the login page has. Use
	// it instead of, or as well as, WhenURLMatches for a page that logs in
	// without changing its address.
	WhenSelectorExists string `yaml:"whenSelectorExists,omitempty" json:"whenSelectorExists"`

	// UsernameSelector and PasswordSelector are the fields to fill.
	// UsernameSelector may be empty, for the forms that ask only for a
	// password: an appliance where the account is implicit, or a form that
	// has already remembered who is signing in.
	UsernameSelector string `yaml:"usernameSelector,omitempty" json:"usernameSelector"`
	PasswordSelector string `yaml:"passwordSelector" json:"passwordSelector"`

	// SubmitSelector is what to click afterwards. Empty presses Enter in the
	// password field instead, which is what forms with no obvious button
	// respond to.
	SubmitSelector string `yaml:"submitSelector,omitempty" json:"submitSelector"`

	// AlsoClick are elements clicked after the fields are filled and before
	// the form is submitted. The one this exists for is a "remember my
	// credentials" checkbox: ticking it makes the session last far longer,
	// which on a screen nobody visits is the difference between signing in
	// every few hours and every few weeks.
	AlsoClick []string `yaml:"alsoClick,omitempty" json:"alsoClick"`

	Username string `yaml:"username" json:"username"`
	Password Secret `yaml:"password" json:"password"`

	// ExpectURLMatches, when set, is a regular expression the address must
	// match for the attempt to be counted as a success. Without it the daemon
	// only knows it typed something in.
	ExpectURLMatches string `yaml:"expectUrlMatches,omitempty" json:"expectUrlMatches"`

	// MinimumInterval is the shortest time between two attempts on the same
	// item. It stops a wrong password from being submitted in a loop, which
	// is how an account gets locked out.
	MinimumInterval Duration `yaml:"minimumInterval,omitempty" json:"minimumInterval"`
}

// Watchdog is how the daemon decides the display has stopped working. A frozen
// screen looks exactly like a working one, so it has to be asked.
type Watchdog struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Interval is how often the probe runs.
	Interval Duration `yaml:"interval" json:"interval"`

	// Timeout is how long a probe may take before it counts as a failure.
	Timeout Duration `yaml:"timeout" json:"timeout"`

	// The recovery ladder. Each threshold is a number of consecutive failed
	// probes, and each step is tried at most once before the next threshold
	// is reached, so a page that is merely slow is not restarted in a loop.
	FailuresBeforeReload     int `yaml:"failuresBeforeReload" json:"failuresBeforeReload"`
	FailuresBeforeRecreate   int `yaml:"failuresBeforeRecreate" json:"failuresBeforeRecreate"`
	FailuresBeforeClearCache int `yaml:"failuresBeforeClearCache" json:"failuresBeforeClearCache"`
	FailuresBeforeRestart    int `yaml:"failuresBeforeRestart" json:"failuresBeforeRestart"`

	// FailuresBeforeRestartDisplay restarts the X server as well, for the
	// case where the browser cannot come back because the server it draws
	// into is the thing that is wedged.
	FailuresBeforeRestartDisplay int `yaml:"failuresBeforeRestartDisplay" json:"failuresBeforeRestartDisplay"`
}

// VNC is the remote view of the screen.
type VNC struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Listen is where x11vnc binds. The default is the loopback address,
	// because the web interface reaches it from inside the same container and
	// exposes it to a browser through an authenticated WebSocket. Binding it
	// to the network puts an unauthenticated view of the screen on the LAN
	// unless Password is also set, and the daemon says so loudly at start-up.
	Listen string `yaml:"listen" json:"listen"`

	// Password protects a listener that is exposed. The web interface's own
	// viewer does not need it.
	Password Secret `yaml:"password,omitempty" json:"password"`

	// ViewOnly refuses keyboard and mouse input from every viewer.
	ViewOnly bool `yaml:"viewOnly" json:"viewOnly"`
}

// Web is the interface an operator uses.
type Web struct {
	// Listen is the address the interface is served on.
	Listen string `yaml:"listen" json:"listen"`

	// PasswordHash is the argon2id hash of the administrator password. Empty
	// means the device has not been set up yet, and every page redirects to
	// the onboarding wizard.
	PasswordHash string `yaml:"passwordHash,omitempty" json:"-"`

	// SessionSecret signs the session cookies. Generated once on first run.
	SessionSecret Secret `yaml:"sessionSecret,omitempty" json:"-"`

	// SessionLifetime is how long a login lasts.
	SessionLifetime Duration `yaml:"sessionLifetime" json:"sessionLifetime"`

	// TrustedOrigins are additional origins allowed to open the VNC
	// WebSocket. The device's own address is always allowed; this is for a
	// reverse proxy in front of it.
	TrustedOrigins []string `yaml:"trustedOrigins,omitempty" json:"trustedOrigins"`
}

// Network is the machine's own network interfaces.
//
// It is empty by default and empty is the right answer for most devices: a
// screen plugged into a wired network gets an address without being asked,
// and there is nothing to configure. It exists for the two cases that cannot
// work that way — a screen on a wireless network, which has to be told which
// one and given the password, and a screen that has to be at a fixed address.
//
// Nothing here is touched unless an interface is named. An interface with no
// entry is left exactly as the machine set it up.
type Network struct {
	// Manage turns the whole of it on. Off by default, because a daemon that
	// reconfigures the network of a machine it was only asked to put a
	// picture on is a surprise nobody wants.
	Manage bool `yaml:"manage" json:"manage"`

	// ReconcileInterval is how often the interfaces are compared with this
	// configuration and any difference put right. It is what brings a screen
	// back after a cable is replugged or a wireless network returns.
	ReconcileInterval Duration `yaml:"reconcileInterval" json:"reconcileInterval"`

	// Onboarding is whether a device with no network may run a temporary
	// wireless network of its own so that somebody can set it up from a phone
	// by scanning a code off its screen.
	//
	// "auto" does it only when the device has no network and none has been
	// configured, which is the state a device is in straight out of a box.
	// "always" does it whenever the hardware allows, which is for trying the
	// thing out on a device that already has a network. "off" never does it.
	Onboarding OnboardingMode `yaml:"onboarding" json:"onboarding"`

	// LostAfter is how long a device may have no usable address before it
	// decides its network is gone and offers itself for setup again.
	//
	// It is a compromise between healing quickly and reacting to a router
	// being rebooted. It can be short because falling back is not final: while
	// the setup network is up the device keeps trying the one it was told
	// about, and goes quietly back to it the moment that works.
	LostAfter Duration `yaml:"lostAfter" json:"lostAfter"`

	Interfaces []Interface `yaml:"interfaces,omitempty" json:"interfaces"`
}

// OnboardingMode is when to offer setting this device up over the air.
type OnboardingMode string

const (
	// OnboardingAuto offers it only on a device that has no network and has
	// not been told about one.
	OnboardingAuto OnboardingMode = "auto"

	// OnboardingAlways offers it whenever the hardware allows. This is for
	// trying it out, and for a device somebody wants to keep settable from a
	// phone; it means anybody who can see the screen can reconfigure it.
	OnboardingAlways OnboardingMode = "always"

	// OnboardingOff never offers it.
	OnboardingOff OnboardingMode = "off"
)

// Valid reports whether this is one of the three modes.
func (self OnboardingMode) Valid() bool {
	switch self {
	case OnboardingAuto, OnboardingAlways, OnboardingOff:
		return true
	}
	return false
}

// Interface is how one network interface should be set up.
type Interface struct {
	// Name is what the kernel calls it: eth0, enp2s0, wlan0, wlp4s0.
	// "cue display probe" and the Network page both list them.
	Name string `yaml:"name" json:"name"`

	// Method is "dhcp" to ask a server for an address, or "static" to be
	// told one here.
	Method string `yaml:"method" json:"method"`

	// Address is the address and prefix length, for example
	// "192.0.2.10/24". Only for the static method.
	Address string `yaml:"address,omitempty" json:"address"`

	// Gateway is the router that reaches everything else.
	Gateway string `yaml:"gateway,omitempty" json:"gateway"`

	// Nameservers override whatever the network offers. Setting these on a
	// screen that shows an internal dashboard is often the difference
	// between it resolving and not.
	Nameservers []string `yaml:"nameservers,omitempty" json:"nameservers"`

	SearchDomain string `yaml:"searchDomain,omitempty" json:"searchDomain"`

	// Wireless is the network to join, for a wireless interface.
	Wireless *Wireless `yaml:"wireless,omitempty" json:"wireless"`
}

// Wireless is the network a wireless interface should join.
type Wireless struct {
	SSID string `yaml:"ssid" json:"ssid"`

	// Passphrase is empty for an open network. Anything else is stored here
	// in the clear, like every other credential this file holds, and the file
	// is written 0600.
	Passphrase Secret `yaml:"passphrase,omitempty" json:"passphrase"`
}

// The ways an interface can be given an address.
const (
	AddressMethodDHCP   = "dhcp"
	AddressMethodStatic = "static"
)

// Audio is sound. A screen showing a camera feed usually wants none; one
// showing a video usually wants some.
type Audio struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Sink names the output to use, as the sound server names it. Empty lets
	// the sound server choose, which on a machine with one HDMI output is
	// right.
	Sink string `yaml:"sink,omitempty" json:"sink"`

	// Source names the input to use, for a screen with a microphone attached
	// that is used for a video call.
	Source string `yaml:"source,omitempty" json:"source"`

	// Volume is the output volume as a percentage, applied at start-up and
	// whenever the chosen sink changes.
	Volume int `yaml:"volume" json:"volume"`
}

// Time keeps the clock right. A browser cannot validate a certificate with a
// wrong clock, so a device whose battery has died shows an error page until
// this has done its work.
type Time struct {
	// Enabled runs a time client on the device. On by default, because a
	// clock that is wrong by more than a few minutes makes every HTTPS
	// dashboard refuse to load, and a screen showing a certificate error is
	// the most common way one of these fails.
	//
	// Turn it off where the machine already keeps its own time — a host
	// running chrony or systemd-timesyncd already does. Two time daemons on
	// one clock is not a small conflict: they correct against each other, and
	// the clock is shared with the machine because it is the machine's.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Servers are the NTP servers to use.
	Servers []string `yaml:"servers" json:"servers"`
}

// Upgrade is what this device may do about a newer release of cue.
//
// Finding out is not configurable: it reads a public API, changes nothing, and
// a screen that cannot say whether it is out of date is a screen nobody
// upgrades. Acting on it is, because it needs the Docker socket.
type Upgrade struct {
	// AllowApply lets the web interface replace this container with one built
	// from a newer release.
	//
	// Off unless somebody turns it on, and useless on its own: the daemon also
	// has to be able to reach /var/run/docker.sock, which means whoever
	// started the container mounted it deliberately. Two separate acts, both
	// required, because the socket is not a small permission. The other
	// capabilities this container asks for let it do more to the machine it is
	// already on; the socket lets it become any process on that machine. A
	// screen in a lobby has a web interface reachable by everybody on the
	// network, and granting this makes that interface's password the password
	// to the host.
	//
	// A device with this off still sees that a release exists, reads its
	// notes, and is shown the commands to upgrade by hand.
	AllowApply bool `yaml:"allowApply" json:"allowApply"`
}
