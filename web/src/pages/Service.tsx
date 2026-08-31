import { useCallback, useEffect, useRef, useState } from "react";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import CircularProgress from "@mui/material/CircularProgress";
import Link from "@mui/material/Link";
import Stack from "@mui/material/Stack";
import Typography from "@mui/material/Typography";
import { Section } from "../components/Section";
import { useSettings, type Settings } from "../settings";
import { api, type LinkState, type ReportingState } from "../api";

// How often the page asks whether the link has completed. Somebody is standing
// over it, so it is short; the daemon asks the service on its own schedule
// regardless, so this only decides how quickly the page catches up.
const askEvery = 1500;

// How long before a code expires to replace it. A ticket is good for ten
// minutes, so a minute is comfortable.
const refreshWhenLeft = 60_000;

// Said only when something is wrong.
//
// The first version showed a "reporting" badge and when the last picture went
// up. Nobody wants to know that a device did the thing it does every thirty
// seconds thirty seconds ago -- it is the machinery describing itself, and it
// pushed the one line that matters, that a device cannot reach the service it
// is linked to, in among noise that never changes.
function NotReporting({ settings }: { settings: Settings }) {
  const reporting = (settings.status as unknown as { reporting?: ReportingState } | null)?.reporting;
  if (!reporting || reporting.attached) return null;
  return (
    <Alert severity="warning" sx={{ mt: 2 }}>
      {reporting.trouble || "This device cannot reach the service."}
    </Alert>
  );
}

// Attaching this device to an account on the hosted service.
//
// The page has no settings on it and shows a code without being asked.
// Somebody who opens it has already decided what they came to do, and a button
// between them and that is a step with one answer.
export function Service() {
  const settings = useSettings();
  const [state, setState] = useState<LinkState | null>(null);
  const [problem, setProblem] = useState("");
  const [said, setSaid] = useState("");
  const [working, setWorking] = useState(false);

  const latest = useRef<LinkState | null>(null);
  const remember = useCallback((next: LinkState) => {
    latest.current = next;
    setState(next);
  }, []);

  const ask = useCallback(async () => {
    try {
      remember(await api.link());
      setProblem("");
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    }
  }, [remember]);

  useEffect(() => { void ask(); }, [ask]);

  // Only while a code is up. The thing being waited for happens on somebody's
  // phone, and nothing here would otherwise notice.
  useEffect(() => {
    if (!state?.pending) return;
    const timer = setInterval(() => { void ask(); }, askEvery);
    return () => clearInterval(timer);
  }, [state?.pending, ask]);

  const act = useCallback(async (
    what: () => Promise<LinkState>,
    saying?: string,
  ) => {
    setWorking(true);
    setProblem("");
    setSaid("");
    try {
      remember(await what());
      if (saying) setSaid(saying);
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    } finally {
      setWorking(false);
    }
  }, [remember]);

  // A code, without being asked for one.
  //
  // Started once per visit rather than whenever the conditions hold: a
  // previous attempt that ended badly leaves an error to read, and minting a
  // fresh code over the top of it would hide what went wrong and loop for as
  // long as the page stayed open. After a failure there is a button.
  const started = useRef(false);
  useEffect(() => {
    if (!state || started.current) return;
    if (state.linked || state.pending || state.error) return;
    started.current = true;
    void act(api.startLink);
  }, [state, act]);

  // A code that expires under somebody's nose is a dead end: they scan it, the
  // page they land on is about an attempt the device has given up, and nothing
  // says why. So while this page is open the code is replaced shortly before
  // it runs out.
  //
  // It is done here, and not in the daemon, on purpose. The expiry exists so a
  // code left on a screen by somebody who wandered off stops working, and a
  // daemon that renewed on its own would keep one alive for ever on a device
  // nobody is looking at. This page being open is the evidence that somebody
  // is.
  //
  // The cost is a scan that was already in flight: somebody who photographed
  // the old code and is still signing in lands on an attempt that has just
  // been replaced. That is a minute's window against ten, and the alternative
  // is a code that certainly stops working rather than one that occasionally
  // does.
  const refreshedFor = useRef("");
  useEffect(() => {
    if (!state?.pending || state.checking || !state.expiresAt) return;
    if (refreshedFor.current === state.expiresAt) return;
    if (new Date(state.expiresAt).getTime() - Date.now() > refreshWhenLeft) return;
    refreshedFor.current = state.expiresAt;
    void act(api.startLink);
  }, [state, act]);

  const askAgain = useCallback(() => {
    started.current = true;
    void act(api.startLink);
  }, [act]);

  // Linking writes the account and the identifier the service gave into the
  // configuration, and this page loaded its copy before any of that existed.
  const wasLinked = useRef(false);
  useEffect(() => {
    if (!state) return;
    if (state.linked && !wasLinked.current) void settings.reload();
    wasLinked.current = state.linked;
  }, [state?.linked, settings.reload]);

  const configuration = settings.configuration;
  const service = configuration?.service ?? { address: "" };

  return (
    <>
      {settings.problem && <Alert severity="error" sx={{ mb: 2 }}>{settings.problem}</Alert>}
      {said && <Alert severity="success" sx={{ mb: 2 }}>{said}</Alert>}
      {problem && <Alert severity="error" sx={{ mb: 2 }}>{problem}</Alert>}

      {state === null ? (
        <Section title="Link"><CircularProgress size={20} /></Section>
      ) : state.linked ? (
        <Section title="Linked">
          <Typography sx={{ mb: 1 }}>
            This device is attached to {state.account || "an account"}.
          </Typography>
          {/* Only when the service calls it something else. An account cannot
              hold two devices of one name, so a second screen called "carbon"
              is recorded there as "carbon 2" -- and that is worth saying,
              because it is how somebody matches the two up. When the names
              agree there is nothing to tell anybody.

              The identifier the service knows it by is not shown: it is this
              device's own identifier now, which is already on the Device
              page. */}
          {service.name && configuration && service.name !== configuration.device.name && (
            <Typography variant="body2" color="text.secondary">
              Called {service.name} there.
            </Typography>
          )}
          <NotReporting settings={settings} />

          <Button color="error" variant="outlined" disabled={working} sx={{ mt: 2 }}
            onClick={() => void act(async () => {
              const after = await api.forgetLink();
              await settings.reload();
              started.current = false;
              return after;
            }, "This device is no longer linked.")}>
            Unlink
          </Button>
        </Section>
      ) : state.pending && state.checking ? (
        <Section title="Checking">
          <Stack direction="row" spacing={1.5} alignItems="center">
            <CircularProgress size={18} />
            <Typography>Authorised. Checking the credential works.</Typography>
          </Stack>
        </Section>
      ) : state.pending ? (
        <Section title="Scan to link">
          <Typography sx={{ mb: 2 }}>Point a phone at this to link it.</Typography>
          <Box
            component="img"
            // The address carries when the attempt expires, because the code
            // changes when the attempt does and the browser has no way to know
            // that from the address alone.
            src={`/api/v1/link/code.svg?at=${encodeURIComponent(state.expiresAt ?? "")}`}
            alt="Linking code"
            sx={{
              display: "block", width: 220, height: 220, bgcolor: "#fff",
              p: 1.5, borderRadius: 1, border: 1, borderColor: "divider", mb: 2,
            }}
          />
          {state.url && (
            <Typography variant="body2" sx={{ mb: 2, wordBreak: "break-all" }}>
              <Link href={state.url} target="_blank" rel="noreferrer">{state.url}</Link>
            </Typography>
          )}
          {/* A spinner crammed into a chip's icon slot: the chip sizes for a
              glyph, not a moving ring, and it sat under the code fighting it
              for attention. The waiting is not a status worth a badge -- it is
              the ordinary state of this page, and a quiet line says so. */}
          <Typography variant="body2" color="text.secondary">
            Waiting for somebody to authorise it…
          </Typography>
        </Section>
      ) : state.error || problem ? (
        // Either the attempt ended badly or asking for one failed. Without
        // the second of those, a device that could not be reached showed an
        // error above a spinner that turned for ever: the message said what
        // had happened and the page went on pretending it was still working
        // on it, with nothing to press.
        <Section title="Not linked">
          {state.error && <Alert severity="warning" sx={{ mb: 2 }}>{state.error}</Alert>}
          <Button variant="contained" disabled={working} onClick={askAgain}>
            Show a new code
          </Button>
        </Section>
      ) : (
        <Section title="Not linked">
          <Stack direction="row" spacing={1.5} alignItems="center">
            <CircularProgress size={18} />
            <Typography color="text.secondary">Getting a code…</Typography>
          </Stack>
        </Section>
      )}
    </>
  );
}
