# A management interface that works on a phone

## Purpose

After this work, somebody holding a phone can reach every page of a screen's
management interface, read it, and press the things on it — and can do so on a
device across the room whose own screen they cannot see.

Today they cannot. The navigation is a row of tabs that is 167 pixels wider
than a phone, so **Device and Upgrade are off the right-hand edge** with
nothing to say they exist. The interface already knows: it scrolls the current
tab into view on every render, which is a workaround written on top of the
problem rather than a fix for it.

How to see it working:

    open http://<the device>:8080 on a phone, or at a 390px viewport

Every page is reachable without horizontal scrolling, nothing scrolls sideways,
and every control can be hit with a thumb.

## What is wrong now, measured

Measured in a real 390x844 viewport, on a live device:

| page     | scrolling | sideways | worst overflow          |
|----------|-----------|----------|-------------------------|
| Overview | 1.6 screens | none   | nav clipped by 167px    |
| Content  | 4.6 screens | **9px** | page itself overflows  |
| Screen   | 1.0 screens | none   | 74px past the edge      |
| Network  | 2.8 screens | none   | —                       |
| Device   | **7.0 screens** | none | —                    |
| Upgrade  | 1.1 screens | none   | —                       |

Navigation links are 37px tall against a 44px minimum for a thumb, and six
controls per page are under it. The theme follows the operating system with no
way to override it.

## The shape asked for

A left sidebar carrying the main navigation, collapsible. A top bar with the
breadcrumb on the left, immediately right of the control that collapses the
sidebar; and on the right, in this order, language, light/dark, and a user
menu.

**Sidebar.** Vertical navigation has room for as many pages as the program
grows to, which a row of tabs does not — that is the fault above, and it gets
worse with every page added. On a phone it is off-canvas: hidden until the
control in the top bar opens it, over the content, dismissed by choosing
something or by pressing outside it. On anything wider it is a column beside
the content, and collapsing it leaves the icons.

**Breadcrumb.** Which page you are on, in words, in the place where a person
looks for it. The tab row was doing that job badly: the current tab was often
the one off the end of the row.

**Top right.** Language, light and dark, and the user menu — the three things
that are about the person rather than about the device.

## Definitions

**Off-canvas.** A panel that is not in the layout until it is asked for, and
then covers part of the page rather than pushing it aside. What a phone needs,
because there is no room for a column beside anything.

**Collapsed.** The sidebar showing only icons. What a narrow laptop wants,
where there is room for a column but not a wide one.

## Milestones

### 1. The shell: sidebar, top bar, and the page beside them

`internal/web/static/app.js` builds a sidebar and a top bar instead of a
header with tabs, and `style.css` lays them out. Nothing about the pages
themselves changes yet.

Verify at 390px and at 1280px: every page reachable, nothing scrolls sideways,
the sidebar opens and closes, and the current page is named in the breadcrumb.

### 2. Light and dark, chosen rather than inherited

A control in the top bar with three states — light, dark, and follow the
system — remembered in the browser it was set in.

Verify: choosing light gives a light interface with the system set to dark, and
it survives a reload.

### 3. The pages fit a phone

The three faults the audit found: the Content page scrolling sideways by 9px,
the 74px overflow on Screen, and the Device page running to seven screens.
Every control at least 44px.

Verify: the table above, again, with no sideways scrolling and nothing past the
right edge.

### 4. Language

Deliberately last, and see the decision log: what this control should do is not
yet settled.

## Progress

- [x] 1. The shell — 2026-08-28. Sidebar with the six pages, off-canvas on a
  phone and collapsible to icons above 720px; top bar with the breadcrumb and
  the three controls. Measured on a live device at 390px: nothing past the
  right edge, navigation links exactly 44px where the tabs were 37.
- [x] 2. Light and dark — 2026-08-28. Three states, remembered in the browser.
  Measured: system → light (#f6f7f9) → dark (#0b0d10), `data-theme` set, and
  the choice survives a reload.
- [x] 3. The pages fit a phone — 2026-08-28, except the length of the Device
  page. No page scrolls sideways and nothing sits past the right edge. The two
  controls still under 44px are inline links inside sentences, where a 44px
  box would be wrong; the guidance is about standalone targets.
- [ ] 4. Language — the control is built and sets `device.language`. Whether
  this interface should itself be translated is the open question below.

- [x] 5. The Device page becomes six — 2026-08-28. Forty-two fields in ten
  cards on one page became Device, Display, Browser, Health, Access and Logs,
  four to eleven fields each. Seven screens on a phone became one to 2.2. The
  three collapsed boxes that hid settings are plain headed sections; the
  headings say what they hold.

## Decision log

**2026-08-28 — A sidebar rather than a wrapping tab row.** Tabs could be made
to wrap instead of scroll, which is a smaller change. It was not taken because
it fixes the symptom for six pages and fails again at nine: a row that wraps to
three lines on a phone spends a third of the screen on navigation. Vertical
space is what a sidebar has and a tab row does not.

**2026-08-28 — The language control is not decided.** The management interface
is written in English only; the on-screen menu speaks three languages and keeps
the choice in `device.language`. A picker in the top bar can either set that —
in which case it changes the screen on the wall and not the page you are
looking at, which is confusing — or it means translating the management
interface, which is a much larger piece of work than this plan. Not guessed at:
asked.

## Surprises and discoveries

**2026-08-28 — The interface already knew its navigation did not fit.**
`render()` scrolls the active tab into view on every render, with a comment
saying the current tab is often off the end of the row and the page gives no
sign of which page it is. The workaround was written and the cause left alone.

**2026-08-28 — What the audit found, measured rather than eyeballed.** A real
390x844 viewport, on carbon, page by page. The navigation was 167 pixels wider
than the phone, putting Device and Upgrade off the edge with nothing to say
they were there. The Content page scrolled sideways by 9 pixels -- a Remove
button in a flex row that would not wrap, sitting 110 pixels past its own
card. The Screen page had something 74 pixels past the edge. Six controls per
page were under the 44px a thumb needs, including every navigation link at 37.

All of those are gone. The Device page's seven screens are not, and that one
is a question about what belongs on a page rather than about layout.

**2026-08-28 — The interface had already been told about this.** `render()`
scrolled the active tab into view on every render, with a comment explaining
that the current tab was often off the end of the row. The symptom was
described accurately and treated; the cause was left. A row of tabs cannot
hold six items on a phone and would not hold seven.

**2026-08-28 — What the Device page was.** Forty-two fields, ten cards, 3621
pixels, seven screens on a phone. Six collapsed boxes, three of them hiding
settings behind labels that said how often you might want them rather than
what was in them -- "Less often needed" (five fields), "Difficult hardware"
(two), "What it does, in order" (five) -- and one with no label at all.
Finding a setting meant opening every box to see whether it was in there.

The headings were evocative rather than descriptive: "Things people touch",
"When the screen stops changing", "Do something now". Good phrases, and not
findable by anybody scanning for the watchdog.

Splitting it is what the sidebar was for. A row of tabs could not have carried
eleven entries -- it could not carry six, which is how this began.

## Outcomes and retrospective

To be written at each milestone.
