import DashboardOutlinedIcon from "@mui/icons-material/DashboardOutlined";
import SlideshowOutlinedIcon from "@mui/icons-material/SlideshowOutlined";
import MonitorOutlinedIcon from "@mui/icons-material/MonitorOutlined";
import WifiOutlinedIcon from "@mui/icons-material/WifiOutlined";
import BadgeOutlinedIcon from "@mui/icons-material/BadgeOutlined";
import AspectRatioOutlinedIcon from "@mui/icons-material/AspectRatioOutlined";
import PublicOutlinedIcon from "@mui/icons-material/PublicOutlined";
import MonitorHeartOutlinedIcon from "@mui/icons-material/MonitorHeartOutlined";
import SettingsRemoteOutlinedIcon from "@mui/icons-material/SettingsRemoteOutlined";
import ScheduleOutlinedIcon from "@mui/icons-material/ScheduleOutlined";
import DescriptionOutlinedIcon from "@mui/icons-material/DescriptionOutlined";
import SystemUpdateAltOutlinedIcon from "@mui/icons-material/SystemUpdateAltOutlined";
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
    // What is on the screen now, live. A monitor.
    { path: "/screen", title: "Screen", Icon: MonitorOutlinedIcon },
  ],
  [
    // Network belongs with the settings rather than above them: joining a
    // wireless network is the same kind of errand as setting the resolution.
    { path: "/network", title: "Network", Icon: WifiOutlinedIcon },
    // Who this device is -- its name, where it stands. Not a chip: nothing on
    // that page is about the hardware.
    { path: "/device", title: "Device", Icon: BadgeOutlinedIcon },
    // How big the picture is and which way up, which is what that page asks.
    // A second monitor icon beside Screen said the two pages were the same
    // thing, and they are not: one is what is showing, this is its shape.
    { path: "/display", title: "Display", Icon: AspectRatioOutlinedIcon },
    { path: "/browser", title: "Browser", Icon: PublicOutlinedIcon },
    { path: "/health", title: "Health", Icon: MonitorHeartOutlinedIcon },
    // Keyboard, mouse, sound and VNC: the ways in from outside. An aerial
    // read as a second network icon.
    { path: "/access", title: "Access", Icon: SettingsRemoteOutlinedIcon },
    { path: "/time", title: "Time", Icon: ScheduleOutlinedIcon },
  ],
  [
    { path: "/logs", title: "Logs", Icon: DescriptionOutlinedIcon },
    { path: "/upgrade", title: "Upgrade", Icon: SystemUpdateAltOutlinedIcon },
  ],
];

export const allPages: Page[] = groups.flat();
