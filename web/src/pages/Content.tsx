import { useState } from "react";
import Accordion from "@mui/material/Accordion";
import AccordionDetails from "@mui/material/AccordionDetails";
import AccordionSummary from "@mui/material/AccordionSummary";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import Card from "@mui/material/Card";
import CardContent from "@mui/material/CardContent";
import Chip from "@mui/material/Chip";
import IconButton from "@mui/material/IconButton";
import LinearProgress from "@mui/material/LinearProgress";
import Stack from "@mui/material/Stack";
import Tooltip from "@mui/material/Tooltip";
import Typography from "@mui/material/Typography";
import ArrowDownwardIcon from "@mui/icons-material/ArrowDownward";
import ArrowUpwardIcon from "@mui/icons-material/ArrowUpward";
import DeleteOutlineIcon from "@mui/icons-material/DeleteOutline";
import VisibilityOffOutlinedIcon from "@mui/icons-material/VisibilityOffOutlined";
import VisibilityOutlinedIcon from "@mui/icons-material/VisibilityOutlined";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";
import PlayArrowIcon from "@mui/icons-material/PlayArrow";
import UploadFileIcon from "@mui/icons-material/UploadFile";

import { api } from "../api";
import { Section } from "../components/Section";
import { Row, Text, Toggle } from "../components/Fields";
import { SettingsPage, useSettings } from "../settings";
import { asSeconds, secondsOf } from "../seconds";

interface Media { file: string; name: string; kind: string; sound?: boolean }

interface Login {
  whenUrlMatches?: string; whenSelectorExists?: string;
  usernameSelector?: string; passwordSelector?: string; submitSelector?: string;
  username?: string; password?: string; alsoClick?: string[];
  expectUrlMatches?: string; minimumInterval?: string;
}

interface Dismiss { selector?: string; whenTextMatches?: string; hide?: boolean }

interface Item {
  identifier?: string;
  title?: string;
  url?: string;
  duration?: string;
  disabled?: boolean;
  reload?: boolean;
  media?: Media;
  login?: Login;
  dismiss?: Dismiss[];
}

interface Playlist { interval?: string; items?: Item[]; maximumUploadSize?: number }

export function Content() {
  const settings = useSettings();
  return (
    <SettingsPage settings={settings}>
      {(configuration) => {
        const playlist = configuration.playlist as unknown as Playlist;
        const items = playlist.items ?? [];
        const set = (change: (draft: Playlist) => void) =>
          settings.change((draft) => change(draft.playlist as unknown as Playlist));

        return (
          <>
            <Section title="Rotation">
              <Row>
                <Text label="Seconds on each page" type="number" value={secondsOf(playlist.interval)}
                  hint="How long an item stays up when it does not say for itself"
                  onChange={(value) => set((draft) => { draft.interval = asSeconds(value); })} />
                <Text label="Largest upload, in MB" type="number"
                  value={String(Math.round((playlist.maximumUploadSize ?? 0) / (1024 * 1024)))}
                  hint="A video larger than this is refused"
                  onChange={(value) => set((draft) => {
                    draft.maximumUploadSize = Math.max(1, parseInt(value, 10) || 1) * 1024 * 1024;
                  })} />
              </Row>
            </Section>

            <Upload onStored={(stored) => set((draft) => {
              (draft.items ??= []).push({
                title: "", disabled: false,
                media: { file: stored.file, name: stored.name, kind: stored.kind, sound: false },
              });
            })} />

            {items.map((item, index) => (
              <ItemCard key={index} item={item} index={index} total={items.length} set={set} />
            ))}

            <Button
              variant="outlined"
              onClick={() => set((draft) => { (draft.items ??= []).push({ url: "", title: "" }); })}
            >
              Add a page
            </Button>
          </>
        );
      }}
    </SettingsPage>
  );
}

function Upload({ onStored }: { onStored: (stored: { file: string; name: string; kind: string }) => void }) {
  const [sending, setSending] = useState<{ name: string; percent: number } | null>(null);
  const [said, setSaid] = useState("");
  const [problem, setProblem] = useState("");

  const send = (file: File) => {
    setProblem(""); setSaid("");
    setSending({ name: file.name, percent: 0 });

    const body = new FormData();
    body.append("file", file);

    // XMLHttpRequest rather than fetch, because this is the one call in the
    // interface where progress matters: a video is hundreds of megabytes over
    // whatever connection a building has, and fetch cannot report upload
    // progress.
    const request = new XMLHttpRequest();
    request.open("POST", "/api/v1/media");
    request.upload.addEventListener("progress", (event) => {
      if (!event.lengthComputable) return;
      setSending({ name: file.name, percent: Math.round((event.loaded / event.total) * 100) });
    });
    request.addEventListener("load", () => {
      setSending(null);
      if (request.status !== 200) {
        let reason = request.statusText;
        try { reason = JSON.parse(request.responseText).error || reason; } catch { /* not JSON */ }
        setProblem(`Could not send that file: ${reason}`);
        return;
      }
      const stored = JSON.parse(request.responseText);
      onStored(stored);
      setSaid(`${stored.name} is on this device. Save to start showing it.`);
    });
    request.addEventListener("error", () => {
      setSending(null);
      setProblem("The upload did not reach the device.");
    });
    request.send(body);
  };

  return (
    <Section title="Add a picture or a video">
      <Button component="label" variant="outlined" startIcon={<UploadFileIcon />} disabled={!!sending}>
        Choose a file
        <input
          type="file"
          accept="video/*,image/*"
          hidden
          onChange={(event) => {
            const file = event.target.files?.[0];
            if (file) send(file);
            event.target.value = "";
          }}
        />
      </Button>
      {sending && (
        <Box sx={{ mt: 2 }}>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 0.5 }}>
            Sending {sending.name}… {sending.percent}%
          </Typography>
          <LinearProgress variant="determinate" value={sending.percent} sx={{ borderRadius: 1 }} />
        </Box>
      )}
      {said && <Alert severity="success" sx={{ mt: 2 }}>{said}</Alert>}
      {problem && <Alert severity="error" sx={{ mt: 2 }}>{problem}</Alert>}
      <Typography variant="body2" color="text.secondary" sx={{ mt: 2 }}>
        It is kept on this device, so the screen goes on showing it with no network at all.
      </Typography>
    </Section>
  );
}

function ItemCard({ item, index, total, set }: {
  item: Item;
  index: number;
  total: number;
  set: (change: (draft: Playlist) => void) => void;
}) {
  const media = item.media ?? null;
  const change = (modify: (draft: Item) => void) =>
    set((draft) => { modify(draft.items![index]!); });

  const move = (offset: number) => set((draft) => {
    const list = draft.items!;
    const target = index + offset;
    if (target < 0 || target >= list.length) return;
    [list[index], list[target]] = [list[target]!, list[index]!];
  });

  return (
    <Card sx={{ mb: 2 }}>
      <CardContent>
        <Stack direction="row" alignItems="center" spacing={1} flexWrap="wrap" useFlexGap sx={{ mb: 2 }}>
          <Typography color="text.secondary">{index + 1}.</Typography>
          <Typography noWrap sx={{
            fontWeight: 600, minWidth: 0, flex: 1,
            // A skipped item is still in the list and is not in the rotation,
            // and should look like it at a glance rather than only in a
            // control somebody has to find.
            opacity: item.disabled ? 0.5 : 1,
            textDecoration: item.disabled ? "line-through" : undefined,
          }}>
            {item.title || media?.name || item.url || "New page"}
          </Typography>
          {media && <Chip size="small" variant="outlined"
            label={media.kind === "picture" ? "Picture" : "Video"} />}
          <Tooltip title={item.disabled ? "Skipped — show it again" : "Skip this for now"}>
            <IconButton
              size="small"
              color={item.disabled ? "warning" : "default"}
              aria-label={item.disabled ? "Show it again" : "Skip this for now"}
              onClick={() => change((draft) => { draft.disabled = !draft.disabled; })}
            >
              {item.disabled
                ? <VisibilityOffOutlinedIcon fontSize="small" />
                : <VisibilityOutlinedIcon fontSize="small" />}
            </IconButton>
          </Tooltip>
          {item.identifier && (
            <Tooltip title="Show now">
              <IconButton size="small" onClick={() => void api.show(item.identifier!).catch(() => {})}>
                <PlayArrowIcon fontSize="small" />
              </IconButton>
            </Tooltip>
          )}
          <IconButton size="small" disabled={index === 0} onClick={() => move(-1)} aria-label="Move up">
            <ArrowUpwardIcon fontSize="small" />
          </IconButton>
          <IconButton size="small" disabled={index === total - 1} onClick={() => move(1)} aria-label="Move down">
            <ArrowDownwardIcon fontSize="small" />
          </IconButton>
          <Tooltip title="Remove">
            <IconButton size="small" color="error" aria-label="Remove"
              onClick={() => set((draft) => { draft.items!.splice(index, 1); })}>
              <DeleteOutlineIcon fontSize="small" />
            </IconButton>
          </Tooltip>
        </Stack>

        {media ? (
          <>
            <Row>
              <Text label="Name (optional)" value={item.title ?? ""} hint={`The file is ${media.name}`}
                onChange={(value) => change((draft) => { draft.title = value; })} />
              {media.kind === "picture" && (
                <Text label="Seconds on screen" type="number" value={secondsOf(item.duration)}
                  hint="Empty uses the rotation setting above"
                  onChange={(value) => change((draft) => { draft.duration = asSeconds(value, 0); })} />
              )}
            </Row>
            {media.kind !== "picture" && (
              <Row>
                <Toggle label="Play this video with its sound" checked={!!media.sound}
                  onChange={(value) => change((draft) => { draft.media!.sound = value; })} />
              </Row>
            )}
            <Typography variant="body2" color="text.secondary">
              {media.kind === "picture"
                ? "It fills the screen for its time and then the screen moves on, like any other item."
                : "It plays full screen and the screen moves on the moment it ends, so it needs no time on screen setting. Sound also needs this device's own sound switched on, on the Access page."}
            </Typography>
          </>
        ) : (
          <>
            <Row>
              <Text label="Address" type="url" value={item.url ?? ""}
                onChange={(value) => change((draft) => { draft.url = value; })} />
              <Text label="Name (optional)" value={item.title ?? ""}
                onChange={(value) => change((draft) => { draft.title = value; })} />
            </Row>
            <Row>
              <Text label="Seconds on screen" type="number" value={secondsOf(item.duration)}
                hint="Empty uses the rotation setting above"
                onChange={(value) => change((draft) => { draft.duration = asSeconds(value, 0); })} />
              <Toggle label="Reload each time it comes round" checked={!!item.reload}
                hint="For a dashboard that stops refreshing itself after a few hours"
                onChange={(value) => change((draft) => { draft.reload = value; })} />
            </Row>

            <Advanced item={item} change={change} />
          </>
        )}
      </CardContent>
    </Card>
  );
}

// Signing in and clicking things away. Folded, because most pages need
// neither -- but the summary says what is inside rather than how often you
// might want it.
function Advanced({ item, change }: { item: Item; change: (modify: (draft: Item) => void) => void }) {
  const login = item.login ?? {};
  const dismiss = item.dismiss ?? [];

  const setLogin = (modify: (draft: Login) => void) =>
    change((draft) => { draft.login = draft.login ?? {}; modify(draft.login); });

  return (
    <Box sx={{ mt: 1 }}>
      <Accordion disableGutters elevation={0} sx={{ border: 1, borderColor: "divider", "&:before": { display: "none" } }}>
        <AccordionSummary expandIcon={<ExpandMoreIcon />}>
          <Typography variant="body2">
            Signing in to this page{dismiss.length > 0 ? ", and what to click away" : ""}
          </Typography>
        </AccordionSummary>
        <AccordionDetails>
          <Row>
            <Text label="Recognise the login page by address matching" value={login.whenUrlMatches ?? ""}
              onChange={(value) => setLogin((draft) => { draft.whenUrlMatches = value; })} />
            <Text label="…or by this element existing" value={login.whenSelectorExists ?? ""}
              onChange={(value) => setLogin((draft) => { draft.whenSelectorExists = value; })} />
          </Row>
          <Row>
            <Text label="Username field" value={login.usernameSelector ?? ""}
              onChange={(value) => setLogin((draft) => { draft.usernameSelector = value; })} />
            <Text label="Password field" value={login.passwordSelector ?? ""}
              onChange={(value) => setLogin((draft) => { draft.passwordSelector = value; })} />
            <Text label="Button to click" value={login.submitSelector ?? ""}
              onChange={(value) => setLogin((draft) => { draft.submitSelector = value; })} />
          </Row>
          <Row>
            <Text label="Username" value={login.username ?? ""}
              onChange={(value) => setLogin((draft) => { draft.username = value; })} />
            <Text label="Password" type="password" value={login.password ?? ""}
              onChange={(value) => setLogin((draft) => { draft.password = value; })} />
          </Row>
          <Row>
            <Text label="Signed in when the address matches" value={login.expectUrlMatches ?? ""}
              onChange={(value) => setLogin((draft) => { draft.expectUrlMatches = value; })} />
            <Text label="Wait at least this long between attempts" type="number"
              value={secondsOf(login.minimumInterval)} hint="Seconds"
              onChange={(value) => setLogin((draft) => { draft.minimumInterval = asSeconds(value); })} />
          </Row>

          <Typography variant="h2" color="text.secondary" sx={{ mt: 2, mb: 1 }}>
            Click these away
          </Typography>
          {dismiss.map((rule, at) => (
            <Row key={at}>
              <Text label="Element" value={rule.selector ?? ""}
                onChange={(value) => change((draft) => { draft.dismiss![at]!.selector = value; })} />
              <Text label="Only if its text matches" value={rule.whenTextMatches ?? ""}
                onChange={(value) => change((draft) => { draft.dismiss![at]!.whenTextMatches = value; })} />
              <Toggle label="Hide it instead of clicking" checked={!!rule.hide}
                onChange={(value) => change((draft) => { draft.dismiss![at]!.hide = value; })} />
            </Row>
          ))}
          <Button size="small" onClick={() => change((draft) => { (draft.dismiss ??= []).push({}); })}>
            Add something to click away
          </Button>
        </AccordionDetails>
      </Accordion>
    </Box>
  );
}
