import { useEffect, useRef, useState } from "react";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import Chip from "@mui/material/Chip";
import Stack from "@mui/material/Stack";
import Typography from "@mui/material/Typography";
import FullscreenIcon from "@mui/icons-material/Fullscreen";
import KeyboardIcon from "@mui/icons-material/Keyboard";
// @ts-expect-error -- noVNC ships no types.
import RFB from "@novnc/novnc";

type Link = "connecting" | "connected" | "disconnected" | "lost";

export function Screen() {
  const canvas = useRef<HTMLDivElement | null>(null);
  const viewer = useRef<HTMLDivElement | null>(null);
  const keyboard = useRef<HTMLInputElement | null>(null);
  const rfb = useRef<{ disconnect: () => void; focus: () => void;
                       sendKey: (key: number, code: string | null, down: boolean) => void } | null>(null);
  const [link, setLink] = useState<Link>("connecting");

  useEffect(() => {
    if (!canvas.current) return;
    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    const connection = new RFB(canvas.current, `${scheme}//${location.host}/api/v1/vnc`, {});
    connection.scaleViewport = true;
    connection.background = "#000";
    // An X server started with no cursor sends no cursor image, and noVNC's
    // fallback dot is invisible, so the pointer would seem not to move at all.
    connection.showDotCursor = true;

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
      connection.disconnect();
      rfb.current = null;
    };
  }, []);

  // A phone has no keyboard until something asks for one. This invisible field
  // is what asks, and what it receives is sent on as key presses.
  const typed = (event: React.FormEvent<HTMLInputElement>) => {
    const field = event.currentTarget;
    for (const character of field.value) {
      const point = character.codePointAt(0);
      if (point === undefined) continue;
      rfb.current?.sendKey(point, null, true);
      rfb.current?.sendKey(point, null, false);
    }
    field.value = "";
  };

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
        <Button size="small" startIcon={<KeyboardIcon />} onClick={() => {
          keyboard.current?.focus();
          rfb.current?.focus();
        }}>
          Keyboard
        </Button>
        <Button size="small" startIcon={<FullscreenIcon />} onClick={() => {
          if (document.fullscreenElement) void document.exitFullscreen();
          else void viewer.current?.requestFullscreen().catch(() => {});
        }}>
          Fullscreen
        </Button>
      </Stack>

      <Box
        ref={viewer}
        sx={{
          bgcolor: "#000", borderRadius: 1, overflow: "hidden",
          border: 1, borderColor: "divider",
          height: { xs: "60vh", md: "72vh" },
        }}
      >
        <Box ref={canvas} sx={{ width: "100%", height: "100%" }} />
      </Box>

      <input
        ref={keyboard}
        type="text"
        autoCapitalize="off"
        autoComplete="off"
        autoCorrect="off"
        spellCheck={false}
        aria-label="Keyboard for the screen"
        onInput={typed}
        onKeyDown={(event) => event.stopPropagation()}
        style={{
          position: "absolute", opacity: 0, pointerEvents: "none",
          width: 1, height: 1, border: 0, padding: 0,
        }}
      />
    </>
  );
}
