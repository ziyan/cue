import { useCallback, useEffect, useState } from "react";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import Dialog from "@mui/material/Dialog";
import DialogActions from "@mui/material/DialogActions";
import DialogContent from "@mui/material/DialogContent";
import DialogContentText from "@mui/material/DialogContentText";
import DialogTitle from "@mui/material/DialogTitle";
import Link from "@mui/material/Link";
import LinearProgress from "@mui/material/LinearProgress";
import Typography from "@mui/material/Typography";

import { api, type UpgradeState } from "../api";
import { Section } from "../components/Section";
import { Readout } from "../components/Readout";
import { ago } from "../format";

const whileRunning = 3000;

export function Upgrade() {
  const [state, setState] = useState<UpgradeState | null>(null);
  const [problem, setProblem] = useState("");
  const [asking, setAsking] = useState(false);
  const [starting, setStarting] = useState(false);

  const look = useCallback(async (quiet = false) => {
    try {
      setState(await api.upgrade());
      if (!quiet) setProblem("");
    } catch (error) {
      // A daemon that has stopped answering is the ordinary case while it is
      // being replaced. Keep the last thing shown rather than replacing it
      // with an error somebody would read as a failure.
      if (!quiet) setProblem(error instanceof Error ? error.message : String(error));
    }
  }, []);

  useEffect(() => { void look(); }, [look]);

  // While one is running the page asks again every few seconds, so the stage
  // moves and the button does not come back.
  useEffect(() => {
    if (!state?.progress.running) return;
    const timer = setInterval(() => void look(true), whileRunning);
    return () => clearInterval(timer);
  }, [state?.progress.running, look]);

  if (problem && !state) return <Alert severity="error">{problem}</Alert>;
  if (!state) return <Typography color="text.secondary">Looking…</Typography>;

  const start = async () => {
    setAsking(false);
    setStarting(true);
    try {
      await api.applyUpgrade();
      await look(true);
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    } finally {
      setStarting(false);
    }
  };

  // An upgrade in progress is the whole page. Nothing else on it can be acted
  // on while the device is replacing itself, and showing the button again is
  // how somebody presses it twice.
  if (state.progress.running) {
    return (
      <Section title={`Updating to ${state.progress.version ?? state.latest}`}>
        <Typography sx={{ mb: 1 }}>{state.progress.stage || "Working"}</Typography>
        {state.progress.startedAt && (
          <Typography variant="body2" color="text.secondary">
            Started {ago(state.progress.startedAt)}
          </Typography>
        )}
        <LinearProgress sx={{ my: 2, borderRadius: 1 }} />
        <Typography variant="body2" color="text.secondary">
          The screen goes blank and comes back on its own. This page stops answering while the
          daemon restarts — it will come back too.
        </Typography>
      </Section>
    );
  }

  return (
    <>
      {state.progress.trouble && (
        <Section title="The last update did not finish">
          <Alert severity="error" sx={{ mb: 1 }}>{state.progress.trouble}</Alert>
          <Typography variant="body2" color="text.secondary">
            This device is still running what it was running before.
          </Typography>
        </Section>
      )}

      <Section title="This device">
        <Readout label="Running">{state.running}</Readout>
        <Readout label="Last checked">
          {state.checkedAt ? ago(state.checkedAt) : "not checked yet"}
        </Readout>
        {state.trouble && (
          <Alert severity="warning" sx={{ mt: 2 }}>
            The last check did not work: {state.trouble}
          </Alert>
        )}
      </Section>

      {!state.latest ? (
        <Section title="Newer releases">
          <Typography color="text.secondary">Nothing is known about newer releases yet.</Typography>
        </Section>
      ) : !state.newer ? (
        <Section title="Up to date">
          <Typography>{state.latest} is the newest release.</Typography>
          {state.url && <ReadItOnGitHub url={state.url} />}
        </Section>
      ) : (
        <>
          <Section title={`${state.latest} is available`}>
            <Typography sx={{ mb: 1 }}>This device is running {state.running}.</Typography>
            {state.publishedAt && (
              <Typography variant="body2" color="text.secondary">
                Released {ago(state.publishedAt)}
              </Typography>
            )}
            {state.notes && <Notes markdown={state.notes} />}
            {state.url && <ReadItOnGitHub url={state.url} />}
          </Section>

          <Section title={state.canApply ? "Install it" : "Install it yourself"}>
            {state.canApply ? (
              <>
                <Typography sx={{ mb: 2 }}>This device can update itself.</Typography>
                <Button variant="contained" disabled={starting} onClick={() => setAsking(true)}>
                  {starting ? "Starting…" : `Update to ${state.latest}`}
                </Button>
              </>
            ) : (
              <>
                <Typography sx={{ mb: 2 }}>{state.whyNot}</Typography>
                <Typography variant="body2" color="text.secondary">On the machine itself:</Typography>
                <Box component="pre" sx={{
                  p: 1.5, mt: 1, borderRadius: 1, overflowX: "auto",
                  bgcolor: "background.default", border: 1, borderColor: "divider",
                  fontFamily: "ui-monospace, monospace", fontSize: "0.85rem",
                }}>
                  docker pull {state.image}
                </Box>
                <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
                  then start it again with the same flags as before, using the new image.
                </Typography>
              </>
            )}
            {problem && <Alert severity="error" sx={{ mt: 2 }}>{problem}</Alert>}
          </Section>

          <Dialog open={asking} onClose={() => setAsking(false)}>
            <DialogTitle>Update to {state.latest}?</DialogTitle>
            <DialogContent>
              <DialogContentText>
                The screen goes blank for about a minute and comes back on its own. The playlist,
                the password and the network settings are kept. If the new version does not start,
                this device puts the old one back by itself.
              </DialogContentText>
            </DialogContent>
            <DialogActions>
              <Button onClick={() => setAsking(false)}>Not now</Button>
              <Button variant="contained" onClick={() => void start()}>Update</Button>
            </DialogActions>
          </Dialog>
        </>
      )}
    </>
  );
}

function ReadItOnGitHub({ url }: { url: string }) {
  return (
    <Typography sx={{ mt: 1.5 }}>
      <Link href={url} target="_blank" rel="noreferrer noopener">Read it on GitHub</Link>
    </Typography>
  );
}

// The release notes, rendered from the Markdown the release workflow copied
// out of CHANGELOG.md. Three shapes, because those are the three it uses: a
// heading, a list item, and a paragraph.
function Notes({ markdown }: { markdown: string }) {
  const blocks: React.ReactNode[] = [];
  let list: string[] = [];

  const flush = () => {
    if (list.length === 0) return;
    blocks.push(
      <Box component="ul" key={blocks.length} sx={{ pl: 2.5, my: 1 }}>
        {list.map((item, index) => (
          <Typography component="li" key={index} sx={{ mb: 0.5 }}>{item}</Typography>
        ))}
      </Box>,
    );
    list = [];
  };

  for (const raw of markdown.split("\n")) {
    const line = raw.trimEnd();
    if (line.startsWith("### ")) {
      flush();
      blocks.push(
        <Typography key={blocks.length} variant="h2" color="text.secondary" sx={{ mt: 2, mb: 0.5 }}>
          {line.slice(4).trim()}
        </Typography>,
      );
    } else if (line.startsWith("- ") || line.startsWith("* ")) {
      list.push(line.slice(2).trim());
    } else if (line.trim() === "") {
      flush();
    } else if (list.length > 0 && (raw.startsWith("  ") || raw.startsWith("\t"))) {
      // A wrapped continuation belongs to the item above it.
      list[list.length - 1] = `${list[list.length - 1]} ${line.trim()}`;
    } else {
      flush();
      blocks.push(<Typography key={blocks.length} sx={{ mb: 1 }}>{line.trim()}</Typography>);
    }
  }
  flush();

  return <Box sx={{ my: 1.5 }}>{blocks}</Box>;
}
