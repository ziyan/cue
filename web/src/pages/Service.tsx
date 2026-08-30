import { useCallback, useEffect, useRef, useState } from "react";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import Chip from "@mui/material/Chip";
import CircularProgress from "@mui/material/CircularProgress";
import Link from "@mui/material/Link";
import Stack from "@mui/material/Stack";
import Typography from "@mui/material/Typography";
import { Section } from "../components/Section";
import { Readout } from "../components/Readout";
import { useSettings, type Settings } from "../settings";
import { api, type LinkState, type ReportingState } from "../api";
import { ago } from "../format";

// How often the page asks whether the link has completed. Somebody is standing
// over it, so it is short; the daemon asks the service on its own schedule
// regardless, so this only decides how quickly the page catches up.
const askEvery = 1500;

// How long before a code expires to replace it. A ticket is good for ten
// minutes, so a minute is comfortable.
const refreshWhenLeft = 60_000;

// Whether the pictures are getting through.
//
// Shown next to the link because they are the two halves of the same question
// and they fail separately: a device can hold a credential the service is
// perfectly happy with and still be unable to reach it, and somebody wondering
// why the picture on their phone is old needs to be told which of the two it
// is.
function Reporting({ settings }: { settings: Settings }) {
  const reporting = (settings.status as unknown as { reporting?: ReportingState } | null)?.reporting;
  if (!reporting) return null;
  return (
    <Stack direction="row" spacing={1.5} alignItems="center" sx={{ mt: 1.5 }}>
      <Chip
        size="small"
        variant="outlined"
        color={reporting.attached ? "success" : "warning"}
        label={reporting.attached ? "reporting" : "not reporting"}
      />
      <Typography variant="body2" color="text.secondary">
        {reporting.trouble
          ? reporting.trouble
          : reporting.lastReportedAt
            ? `Last picture sent ${ago(reporting.lastReportedAt)}.`
            : "No picture has been sent yet."}
      </Typography>
    </Stack>
  );
}

// Attaching this device to an account on the hosted service.
//
// The page has no settings on it. Where a device reports to is the same for
// every device and is not changed from here, so there is nothing to save and
// no frame of Save and Discard around it: what the page is for is the code,
// and it shows one without being asked. Somebody who opens this page has
// already decided what they came to do, and a button between them and the
// thing they came for is a step that only ever has one answer.
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
          {service.name && (
            <Readout label="Called there">
              {service.name}
              {configuration && service.name !== configuration.device.name && (
                <Typography component="span" variant="body2" color="text.secondary" sx={{ ml: 1 }}>
                  — this screen calls itself {configuration.device.name}
                </Typography>
              )}
            </Readout>
          )}
          {service.deviceId && (
            <Readout label="Known there as" mono>{service.deviceId}</Readout>
          )}
          <Reporting settings={settings} />
          <Typography variant="body2" color="text.secondary" sx={{ mt: 1.5, mb: 2 }}>
            Unlinking forgets the credential and nothing else. The device keeps its
            name, its screens and everything it is showing.
          </Typography>
          <Button color="error" variant="outlined" disabled={working}
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
          <Stack direction="row" spacing={1.5} alignItems="center" sx={{ mb: 1 }}>
            <CircularProgress size={18} />
            <Typography>Authorised. Collecting the credential and checking it works.</Typography>
          </Stack>
          <Typography variant="body2" color="text.secondary">
            The code has done its job — you can put your phone away. This device does not
            call itself linked until the service has answered to the credential it was given.
          </Typography>
        </Section>
      ) : state.pending ? (
        <Section title="Scan to link">
          <Typography sx={{ mb: 2 }}>
            Point a phone at this, sign in, and authorise the device.
          </Typography>
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
      ) : state.error ? (
        <Section title="Not linked">
          <Alert severity="warning" sx={{ mb: 2 }}>{state.error}</Alert>
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
