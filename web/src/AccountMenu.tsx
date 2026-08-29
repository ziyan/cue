import { useState, type MouseEvent } from "react";
import Box from "@mui/material/Box";
import Divider from "@mui/material/Divider";
import IconButton from "@mui/material/IconButton";
import ListItemIcon from "@mui/material/ListItemIcon";
import ListItemText from "@mui/material/ListItemText";
import Menu from "@mui/material/Menu";
import MenuItem from "@mui/material/MenuItem";
import Tooltip from "@mui/material/Tooltip";
import Typography from "@mui/material/Typography";
import AccountCircleOutlinedIcon from "@mui/icons-material/AccountCircleOutlined";
import LogoutOutlinedIcon from "@mui/icons-material/LogoutOutlined";
import { api, type SetupState } from "./api";

export function AccountMenu({ state, onSignedOut }: {
  state: SetupState;
  onSignedOut: () => void;
}) {
  const [anchor, setAnchor] = useState<null | HTMLElement>(null);

  return (
    <>
      <Tooltip title="Account">
        <IconButton edge="end" onClick={(event: MouseEvent<HTMLElement>) => setAnchor(event.currentTarget)}
          aria-label="Account">
          <AccountCircleOutlinedIcon />
        </IconButton>
      </Tooltip>
      <Menu anchorEl={anchor} open={!!anchor} onClose={() => setAnchor(null)}>
        <Box sx={{ px: 2, py: 1 }}>
          <Typography sx={{ fontWeight: 600 }}>{state.device.name || "Cue"}</Typography>
          <Typography variant="body2" color="text.secondary" sx={{ fontFamily: "ui-monospace, monospace" }}>
            {state.device.identifier}
          </Typography>
        </Box>
        <Divider />
        <MenuItem onClick={async () => {
          setAnchor(null);
          try { await api.signOut(); } finally { onSignedOut(); }
        }}>
          <ListItemIcon><LogoutOutlinedIcon fontSize="small" /></ListItemIcon>
          <ListItemText>Sign out</ListItemText>
        </MenuItem>
      </Menu>
    </>
  );
}
