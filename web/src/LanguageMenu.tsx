import { useState, type MouseEvent } from "react";
import IconButton from "@mui/material/IconButton";
import ListItemText from "@mui/material/ListItemText";
import ListSubheader from "@mui/material/ListSubheader";
import Menu from "@mui/material/Menu";
import MenuItem from "@mui/material/MenuItem";
import Tooltip from "@mui/material/Tooltip";
import CheckIcon from "@mui/icons-material/Check";
import TranslateIcon from "@mui/icons-material/Translate";
import { api } from "./api";

// The language the screen speaks -- the one shown in the menu somebody opens
// standing at it. This interface is in English only, so the control says what
// it changes rather than implying it changes this page.
const languages = [
  { tag: "", name: "English" },
  { tag: "zh", name: "中文" },
  { tag: "ja", name: "日本語" },
];

export function LanguageMenu({ language }: { language: string }) {
  const [anchor, setAnchor] = useState<null | HTMLElement>(null);
  const [chosen, setChosen] = useState(language);

  const choose = async (tag: string) => {
    setAnchor(null);
    const previous = chosen;
    setChosen(tag);
    try {
      const configuration = await api.configuration();
      configuration.device.language = tag;
      await api.saveConfiguration(configuration);
    } catch {
      setChosen(previous);
    }
  };

  return (
    <>
      <Tooltip title="Language on the screen">
        <IconButton onClick={(event: MouseEvent<HTMLElement>) => setAnchor(event.currentTarget)}
          aria-label="Language on the screen">
          <TranslateIcon />
        </IconButton>
      </Tooltip>
      <Menu anchorEl={anchor} open={!!anchor} onClose={() => setAnchor(null)}>
        <ListSubheader sx={{ lineHeight: 2, bgcolor: "transparent" }}>
          What the screen itself speaks
        </ListSubheader>
        {languages.map((one) => (
          <MenuItem key={one.tag || "en"} onClick={() => void choose(one.tag)}>
            <ListItemText>{one.name}</ListItemText>
            {chosen === one.tag && <CheckIcon fontSize="small" sx={{ ml: 1.5 }} />}
          </MenuItem>
        ))}
      </Menu>
    </>
  );
}
