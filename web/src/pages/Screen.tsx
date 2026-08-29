import { useCallback, useEffect, useRef, useState } from "react";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import Chip from "@mui/material/Chip";
import IconButton from "@mui/material/IconButton";
import Stack from "@mui/material/Stack";
import Tooltip from "@mui/material/Tooltip";
import Typography from "@mui/material/Typography";
import CloseIcon from "@mui/icons-material/Close";
import FullscreenIcon from "@mui/icons-material/Fullscreen";
import FullscreenExitIcon from "@mui/icons-material/FullscreenExit";
// @ts-expect-error -- noVNC ships no types.
import RFB from "@novnc/novnc";

type Link = "connecting" | "connected" | "disconnected" | "lost";

// A small white dot with a dark edge, so it can be seen against a light page
// and a dark one. The hotspot is the middle.
const dot =
  "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='9' height='9'%3E" +
  "%3Ccircle cx='4.5' cy='4.5' r='3.5' fill='%23fff' stroke='%23000' stroke-width='1.5'/%3E" +
  "%3C/svg%3E\") 4 4, default";

// Where the pointer is, when the screen has no pointer of its own.
//
// A screen started with no cursor sends no cursor image, and noVNC answers
// that by asking the browser for no cursor at all -- so moving the mouse over
// the picture shows nothing moving, and it is impossible to aim. noVNC has a
// dot of its own for this, but it only reaches for it while a cursor update is
// being handled, and a screen that never sends one never gets there. So watch
// what noVNC writes on the canvas: when it says "none", say dot instead, and
// when it sends a real cursor leave that alone.
function keepAPointer(holder: HTMLElement): () => void {
  const put = (canvas: HTMLCanvasElement) => {
    if (canvas.style.cursor === "none" || canvas.style.cursor === "") {
      canvas.style.cursor = dot;
    }
  };
  const watcher = new MutationObserver((changes) => {
    for (const change of changes) {
      if (change.type === "attributes" && change.target instanceof HTMLCanvasElement) {
        put(change.target);
      }
      for (const added of change.addedNodes) {
        if (added instanceof HTMLCanvasElement) {
          put(added);
          watcher.observe(added, { attributes: true, attributeFilter: ["style"] });
        }
      }
    }
  });
  watcher.observe(holder, { childList: true, subtree: true });
  for (const canvas of holder.querySelectorAll("canvas")) {
    put(canvas);
    watcher.observe(canvas, { attributes: true, attributeFilter: ["style"] });
  }
  return () => watcher.disconnect();
}

export function Screen() {
  const canvas = useRef<HTMLDivElement | null>(null);
  const viewer = useRef<HTMLDivElement | null>(null);
  const rfb = useRef<{ disconnect: () => void } | null>(null);
  const [link, setLink] = useState<Link>("connecting");
  // Two ways of filling the window, because a phone only has the second one.
  // Safari on iPhone has no fullscreen for anything but a video, so the button
  // did nothing at all there. When the browser will not do it, the page does
  // it itself: the picture is pinned over everything else.
  const [filling, setFilling] = useState(false);

  useEffect(() => {
    if (!canvas.current) return;
    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    const connection = new RFB(canvas.current, `${scheme}//${location.host}/api/v1/vnc`, {});
    connection.scaleViewport = true;
    connection.background = "#000";
    connection.showDotCursor = true;
    const stopWatching = keepAPointer(canvas.current);

    // Rotating the picture to fit a phone held upright was tried and taken out
    // again. Turning the canvas with CSS turns what you see and not where
    // noVNC thinks you touched: it works the pointer position out from the
    // element's bounding box, so every tap would land a quarter turn from
    // where the finger went.

    const connected = () => setLink("connected");
    const gone = (event: CustomEvent<{ clean: boolean }>) =>
      setLink(event.detail.clean ? "disconnected" : "lost");

    connection.addEventListener("connect", connected);
    connection.addEventListener("disconnect", gone);
    rfb.current = connection;

    return () => {
      connection.removeEventListener("connect", connected);
      connection.removeEventListener("disconnect", gone);
      stopWatching();
      connection.disconnect();
      rfb.current = null;
    };
  }, []);

  // The browser's own fullscreen can be left by pressing escape, and the
  // button has to follow that rather than keep claiming it is still on.
  useEffect(() => {
    const changed = () => { if (!document.fullscreenElement) setFilling(false); };
    document.addEventListener("fullscreenchange", changed);
    return () => document.removeEventListener("fullscreenchange", changed);
  }, []);

  useEffect(() => {
    if (!filling) return;
    const leave = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !document.fullscreenElement) setFilling(false);
    };
    window.addEventListener("keydown", leave);
    return () => window.removeEventListener("keydown", leave);
  }, [filling]);

  const fill = useCallback(() => {
    if (document.fullscreenElement) {
      void document.exitFullscreen();
      setFilling(false);
      return;
    }
    if (filling) {
      setFilling(false);
      return;
    }
    setFilling(true);
    // Ask the browser as well. If it can, the picture gets the whole display
    // rather than the whole page; if it cannot, the page is already covered.
    void viewer.current?.requestFullscreen?.().catch(() => {});
  }, [filling]);

  return (
    <>
      <Stack direction="row" spacing={1.5} alignItems="center" sx={{ mb: 1.5 }}>
        <Chip
          size="small"
          variant="outlined"
          color={link === "connected" ? "success" : link === "connecting" ? "default" : "error"}
          label={link === "lost" ? "connection lost" : link}
        />
        <Typography variant="body2" color="text.secondary" sx={{ display: { xs: "none", sm: "block" } }}>
          Click and type to drive the screen.
        </Typography>
        <Box sx={{ ml: "auto" }} />
        <Button
          size="small"
          startIcon={filling ? <FullscreenExitIcon /> : <FullscreenIcon />}
          onClick={fill}
        >
          {filling ? "Leave fullscreen" : "Fullscreen"}
        </Button>
      </Stack>

      <Box
        ref={viewer}
        sx={{
          bgcolor: "#000", overflow: "hidden",
          ...(filling
            ? {
                position: "fixed", inset: 0, zIndex: (theme) => theme.zIndex.modal + 1,
                borderRadius: 0, border: 0,
                // The small viewport unit, so the phone's own bars do not
                // push the bottom of the picture off the display.
                height: "100dvh", width: "100vw",
              }
            : {
                borderRadius: 1, border: 1, borderColor: "divider",
                height: { xs: "60vh", md: "72vh" },
              }),
        }}
      >
        <Box ref={canvas} sx={{ width: "100%", height: "100%" }} />
        {filling && (
          <Tooltip title="Leave fullscreen">
            <IconButton
              onClick={fill}
              aria-label="Leave fullscreen"
              sx={{
                position: "absolute", top: 8, right: 8,
                color: "#fff", bgcolor: "rgba(0,0,0,0.45)",
                "&:hover": { bgcolor: "rgba(0,0,0,0.65)" },
              }}
            >
              <CloseIcon />
            </IconButton>
          </Tooltip>
        )}
      </Box>
    </>
  );
}
