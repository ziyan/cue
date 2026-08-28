import { useCallback, useState } from "react";
import { Link, Outlet, useLocation } from "react-router";
import AppBar from "@mui/material/AppBar";
import Box from "@mui/material/Box";
import Divider from "@mui/material/Divider";
import Drawer from "@mui/material/Drawer";
import IconButton from "@mui/material/IconButton";
import List from "@mui/material/List";
import ListItemButton from "@mui/material/ListItemButton";
import ListItemIcon from "@mui/material/ListItemIcon";
import ListItemText from "@mui/material/ListItemText";
import Stack from "@mui/material/Stack";
import Toolbar from "@mui/material/Toolbar";
import Tooltip from "@mui/material/Tooltip";
import Typography from "@mui/material/Typography";
import useMediaQuery from "@mui/material/useMediaQuery";
import { useTheme } from "@mui/material/styles";
import ChevronRightIcon from "@mui/icons-material/ChevronRight";
import MenuIcon from "@mui/icons-material/Menu";
import MenuOpenIcon from "@mui/icons-material/MenuOpen";

import { allPages, groups } from "./pages";
import { AppearanceMenu } from "./AppearanceMenu";
import { LanguageMenu } from "./LanguageMenu";
import { AccountMenu } from "./AccountMenu";
import type { Appearance } from "./theme";
import type { SetupState } from "./api";

// One height for the top bar and for the sidebar's own header, so the rule
// under the logo and the rule under the top bar are the same line across the
// window rather than two that nearly agree.
const barHeight = 64;
const openWidth = 240;
// Wide enough for a 40px target with the same 12px either side the open
// sidebar gives it, so an icon does not shift when the words go.
const railWidth = 72;

const remembered = "cue.sidebar";

export function Shell({ state, appearance, onAppearance, onSignedOut }: {
  state: SetupState;
  appearance: Appearance;
  onAppearance: (next: Appearance) => void;
  onSignedOut: () => void;
}) {
  const theme = useTheme();
  const wide = useMediaQuery(theme.breakpoints.up("md"));
  const [open, setOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem(remembered) === "collapsed";
    } catch {
      return false;
    }
  });

  const here = useLocation().pathname;
  const page = allPages.find((one) => one.path === here);
  const rail = wide && collapsed;
  const width = rail ? railWidth : openWidth;

  // On a wide screen the control collapses the sidebar to its icons and
  // remembers it; on a phone there is no room for a rail, so it opens and
  // closes the drawer instead.
  const toggle = useCallback(() => {
    if (!wide) {
      setOpen((was) => !was);
      return;
    }
    setCollapsed((was) => {
      const next = !was;
      try {
        localStorage.setItem(remembered, next ? "collapsed" : "open");
      } catch {
        // A private window. It just will not be remembered.
      }
      return next;
    });
  }, [wide]);

  const navigation = (
    <Box sx={{ display: "flex", flexDirection: "column", height: "100%", overflowX: "hidden" }}>
      <Toolbar
        disableGutters
        sx={{
          height: barHeight, minHeight: barHeight, flexShrink: 0,
          px: rail ? 0 : 2, gap: 1.25,
          justifyContent: rail ? "center" : "flex-start",
        }}
      >
        <Box component="img" src="/favicon.svg" alt="" sx={{ width: 22, height: 22, flexShrink: 0 }} />
        {!rail && (
          <Typography noWrap sx={{ fontWeight: 600 }}>{state.device.name || "Cue"}</Typography>
        )}
      </Toolbar>
      <Divider />

      {groups.map((group, index) => (
        <Box key={index}>
          {index > 0 && <Divider />}
          {/* The padding is what keeps the selected row from sitting against
              the rule above it. Without it the highlight and the divider touch
              and read as one shape. */}
          <List sx={{ px: rail ? 1 : 1.25, py: 1 }}>
            {group.map((one) => {
              const row = (
                <ListItemButton
                  key={one.path}
                  component={Link}
                  to={one.path}
                  selected={here === one.path}
                  onClick={() => setOpen(false)}
                  sx={{
                    minHeight: 44,
                    justifyContent: rail ? "center" : "flex-start",
                    px: rail ? 0 : 1.25,
                    "&.Mui-selected": {
                      bgcolor: "primary.main",
                      color: "primary.contrastText",
                      "& .MuiListItemIcon-root": { color: "inherit" },
                      "&:hover": { bgcolor: "primary.main" },
                    },
                  }}
                >
                  <ListItemIcon sx={{ minWidth: rail ? 0 : 36, justifyContent: "center" }}>
                    <one.Icon fontSize="small" />
                  </ListItemIcon>
                  {!rail && <ListItemText primary={one.title} />}
                </ListItemButton>
              );
              // Collapsed, the only thing naming the row is the tooltip.
              return rail
                ? <Tooltip key={one.path} title={one.title} placement="right">{row}</Tooltip>
                : row;
            })}
          </List>
        </Box>
      ))}
    </Box>
  );

  return (
    <Box sx={{ display: "flex", minHeight: "100dvh" }}>
      <Box component="nav" sx={{ width: { md: width }, flexShrink: { md: 0 } }}>
        <Drawer
          variant={wide ? "permanent" : "temporary"}
          open={wide || open}
          onClose={() => setOpen(false)}
          ModalProps={{ keepMounted: true }}
          sx={{
            "& .MuiDrawer-paper": {
              width: wide ? width : openWidth,
              boxSizing: "border-box",
              borderRight: 1, borderColor: "divider",
              overflowX: "hidden",
              transition: theme.transitions.create("width", {
                easing: theme.transitions.easing.sharp,
                duration: theme.transitions.duration.shorter,
              }),
            },
          }}
        >
          {navigation}
        </Drawer>
      </Box>

      <Box sx={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
        <AppBar
          position="sticky"
          color="default"
          elevation={0}
          sx={{ borderBottom: 1, borderColor: "divider", bgcolor: "background.paper" }}
        >
          <Toolbar sx={{ height: barHeight, minHeight: barHeight, gap: 1 }}>
            <Tooltip title={rail ? "Show the names" : wide ? "Just the icons" : "Menu"}>
              <IconButton edge="start" onClick={toggle} aria-label="Menu">
                {rail ? <MenuIcon /> : <MenuOpenIcon />}
              </IconButton>
            </Tooltip>

            <Stack direction="row" alignItems="center" spacing={0.5} sx={{ minWidth: 0, mr: "auto" }}>
              {wide && (
                <>
                  <Typography noWrap color="text.secondary">{state.device.name || "Cue"}</Typography>
                  <ChevronRightIcon sx={{ fontSize: 16, color: "text.disabled" }} />
                </>
              )}
              <Typography noWrap sx={{ fontWeight: 600 }}>{page?.title ?? ""}</Typography>
            </Stack>

            <LanguageMenu language={state.device.language ?? ""} />
            <AppearanceMenu appearance={appearance} onChange={onAppearance} />
            <AccountMenu state={state} onSignedOut={onSignedOut} />
          </Toolbar>
        </AppBar>

        <Box component="main" sx={{ p: { xs: 1.5, sm: 2.5 }, maxWidth: 1100, width: "100%", mx: "auto" }}>
          <Outlet />
        </Box>
      </Box>
    </Box>
  );
}
