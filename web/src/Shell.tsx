import { useState } from "react";
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
const sidebarWidth = 240;

export function Shell({ state, appearance, onAppearance, onSignedOut }: {
  state: SetupState;
  appearance: Appearance;
  onAppearance: (next: Appearance) => void;
  onSignedOut: () => void;
}) {
  const theme = useTheme();
  const wide = useMediaQuery(theme.breakpoints.up("md"));
  const [open, setOpen] = useState(false);

  const here = useLocation().pathname;
  const page = allPages.find((one) => one.path === here);

  const navigation = (
    <Box sx={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <Toolbar sx={{ height: barHeight, minHeight: barHeight, gap: 1.25 }}>
        <Box component="img" src="/favicon.svg" alt="" sx={{ width: 22, height: 22 }} />
        <Typography noWrap sx={{ fontWeight: 600 }}>{state.device.name || "Cue"}</Typography>
      </Toolbar>
      <Divider />
      {groups.map((group, index) => (
        <Box key={index}>
          {index > 0 && <Divider sx={{ my: 0.75 }} />}
          <List sx={{ px: 1, py: 0 }}>
            {group.map((one) => (
              <ListItemButton
                key={one.path}
                component={Link}
                to={one.path}
                selected={here === one.path}
                onClick={() => setOpen(false)}
              >
                <ListItemIcon sx={{ minWidth: 36 }}><one.Icon fontSize="small" /></ListItemIcon>
                <ListItemText primary={one.title} />
              </ListItemButton>
            ))}
          </List>
        </Box>
      ))}
    </Box>
  );

  return (
    <Box sx={{ display: "flex", minHeight: "100dvh" }}>
      <Box component="nav" sx={{ width: { md: sidebarWidth }, flexShrink: { md: 0 } }}>
        <Drawer
          variant={wide ? "permanent" : "temporary"}
          open={wide || open}
          onClose={() => setOpen(false)}
          ModalProps={{ keepMounted: true }}
          sx={{
            "& .MuiDrawer-paper": {
              width: sidebarWidth, boxSizing: "border-box",
              borderRight: 1, borderColor: "divider",
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
            {!wide && (
              <Tooltip title="Menu">
                <IconButton edge="start" onClick={() => setOpen(true)} aria-label="Menu">
                  <MenuIcon />
                </IconButton>
              </Tooltip>
            )}
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
