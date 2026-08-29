import { createTheme, type Theme } from "@mui/material/styles";

// The two accents are the ones the interface already used, and they were
// chosen by measurement rather than by eye: the mark is a warm gradient, and
// which end of it stays legible depends on the background. On white the
// magenta end gives 6.97:1 where the orange end gives 2.75:1 and fails; on a
// dark surface the magenta end is the one that fails, at 2.56:1, and orange is
// also the only colour in the mark that stays clear of both the warning amber
// and the error red.
const accent = { light: "#a72258", dark: "#f57915" };

export type Appearance = "light" | "dark" | "system";

export function themeFor(mode: "light" | "dark"): Theme {
  return createTheme({
    palette: {
      mode,
      primary: { main: mode === "dark" ? accent.dark : accent.light },
      background: {
        default: mode === "dark" ? "#0b0d10" : "#f6f7f9",
        paper: mode === "dark" ? "#14181d" : "#ffffff",
      },
      divider: mode === "dark" ? "#262d36" : "#dde1e7",
    },
    shape: { borderRadius: 10 },
    typography: {
      fontFamily: [
        "system-ui", "-apple-system", "Segoe UI", "Roboto",
        "Noto Sans CJK SC", "Noto Sans CJK JP", "sans-serif",
      ].join(","),
      h2: { fontSize: "0.8rem", fontWeight: 600, letterSpacing: "0.06em", textTransform: "uppercase" },
    },
    components: {
      // A screen is often looked after from a phone, standing in front of it.
      // Everything that gets pressed is at least the size of a thumb.
      MuiButton: { defaultProps: { disableElevation: true } },
      MuiListItemButton: { styleOverrides: { root: { minHeight: 44, borderRadius: 8 } } },
      MuiMenuItem: { styleOverrides: { root: { minHeight: 44 } } },
      MuiCard: { defaultProps: { variant: "outlined" } },
    },
  });
}
