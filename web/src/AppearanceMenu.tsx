import { useState, type MouseEvent } from "react";
import IconButton from "@mui/material/IconButton";
import ListItemIcon from "@mui/material/ListItemIcon";
import ListItemText from "@mui/material/ListItemText";
import Menu from "@mui/material/Menu";
import MenuItem from "@mui/material/MenuItem";
import Tooltip from "@mui/material/Tooltip";
import CheckIcon from "@mui/icons-material/Check";
import DarkModeOutlinedIcon from "@mui/icons-material/DarkModeOutlined";
import DesktopWindowsOutlinedIcon from "@mui/icons-material/DesktopWindowsOutlined";
import LightModeOutlinedIcon from "@mui/icons-material/LightModeOutlined";
import type { Appearance } from "./theme";

// A menu naming the three, rather than a button that cycles them. A cycling
// button never says what the next press will do, and "same as this device" is
// not a state anybody guesses is hidden in there.
const choices = [
  { key: "light" as const, name: "Light", Icon: LightModeOutlinedIcon },
  { key: "dark" as const, name: "Dark", Icon: DarkModeOutlinedIcon },
  { key: "system" as const, name: "Same as this device", Icon: DesktopWindowsOutlinedIcon },
];

export function AppearanceMenu({ appearance, onChange }: {
  appearance: Appearance;
  onChange: (next: Appearance) => void;
}) {
  const [anchor, setAnchor] = useState<null | HTMLElement>(null);
  const showing = choices.find((one) => one.key === appearance) ?? choices[2]!;

  return (
    <>
      <Tooltip title="Light or dark">
        <IconButton onClick={(event: MouseEvent<HTMLElement>) => setAnchor(event.currentTarget)}
          aria-label="Light or dark">
          <showing.Icon />
        </IconButton>
      </Tooltip>
      <Menu anchorEl={anchor} open={!!anchor} onClose={() => setAnchor(null)}>
        {choices.map((one) => (
          <MenuItem key={one.key} onClick={() => { onChange(one.key); setAnchor(null); }}>
            <ListItemIcon><one.Icon fontSize="small" /></ListItemIcon>
            <ListItemText>{one.name}</ListItemText>
            {appearance === one.key && <CheckIcon fontSize="small" sx={{ ml: 1.5 }} />}
          </MenuItem>
        ))}
      </Menu>
    </>
  );
}
