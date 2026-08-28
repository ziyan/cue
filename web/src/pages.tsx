import DashboardOutlinedIcon from "@mui/icons-material/DashboardOutlined";
import SlideshowOutlinedIcon from "@mui/icons-material/SlideshowOutlined";
import MonitorOutlinedIcon from "@mui/icons-material/MonitorOutlined";
import WifiOutlinedIcon from "@mui/icons-material/WifiOutlined";
import MemoryOutlinedIcon from "@mui/icons-material/MemoryOutlined";
import TvOutlinedIcon from "@mui/icons-material/TvOutlined";
import LanguageOutlinedIcon from "@mui/icons-material/LanguageOutlined";
import MonitorHeartOutlinedIcon from "@mui/icons-material/MonitorHeartOutlined";
import SettingsInputAntennaOutlinedIcon from "@mui/icons-material/SettingsInputAntennaOutlined";
import ScheduleOutlinedIcon from "@mui/icons-material/ScheduleOutlined";
import DescriptionOutlinedIcon from "@mui/icons-material/DescriptionOutlined";
import UpgradeOutlinedIcon from "@mui/icons-material/UpgradeOutlined";
import type { SvgIconComponent } from "@mui/icons-material";

export interface Page {
  path: string;
  title: string;
  Icon: SvgIconComponent;
}

// The pages, in groups. Eleven entries in one undivided list is a wall; a rule
// between them says these belong together and those do not, and the rows
// already say what they are.
export const groups: Page[][] = [
  [
    { path: "/", title: "Overview", Icon: DashboardOutlinedIcon },
    { path: "/content", title: "Content", Icon: SlideshowOutlinedIcon },
    { path: "/screen", title: "Screen", Icon: MonitorOutlinedIcon },
  ],
  [
    // Network belongs with the settings rather than above them: joining a
    // wireless network is the same kind of errand as setting the resolution.
    { path: "/network", title: "Network", Icon: WifiOutlinedIcon },
    { path: "/device", title: "Device", Icon: MemoryOutlinedIcon },
    { path: "/display", title: "Display", Icon: TvOutlinedIcon },
    { path: "/browser", title: "Browser", Icon: LanguageOutlinedIcon },
    { path: "/health", title: "Health", Icon: MonitorHeartOutlinedIcon },
    { path: "/access", title: "Access", Icon: SettingsInputAntennaOutlinedIcon },
    { path: "/time", title: "Time", Icon: ScheduleOutlinedIcon },
  ],
  [
    { path: "/logs", title: "Logs", Icon: DescriptionOutlinedIcon },
    { path: "/upgrade", title: "Upgrade", Icon: UpgradeOutlinedIcon },
  ],
];

export const allPages: Page[] = groups.flat();
