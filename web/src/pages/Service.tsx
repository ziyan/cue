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
import { Row, Text } from "../components/Fields";
import { SettingsPage, useSettings } from "../settings";
import { api, type LinkState } from "../api";

// How often the page asks whether the link has completed. Somebody is standing
// over it, so it is short; the daemon asks the service on its own schedule
// regardless, so this only decides how quickly the page catches up.
const askEvery = 1500;

// Attaching this device to an account on the hosted service.
//
// Three states, and exactly one of them is shown: not linked and offering to
// start, showing a code and waiting, or linked and saying to what. Somebody
// arriving at the page should be able to tell which without reading.
export function Service() {
  const settings = useSettings();
  const [state, setState] = useState<LinkState | null>(null);
  const [problem, setProblem] = useState("");
  const [said, setSaid] = useState("");
  const [working, setWorking] = useState(false);

  // Held in a ref as well, so the timer can compare against the latest without
  // being torn down and rebuilt every time the answer arrives.
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

  // Linking writes the account and the identifier the service gave into the
  // configuration, and this page loaded its copy before any of that existed.
  // Without reading it again the page says "Linked" over an empty space.
  const wasLinked = useRef(false);
  useEffect(() => {
    if (!state) return;
    if (state.linked && !wasLinked.current) void settings.reload();
    wasLinked.current = state.linked;
  }, [state?.linked, settings.reload]);

  // Only while a code is up. The thing being waited for happens on somebody's
  // phone, and nothing here would otherwise notice; once it has happened, or
  // once there is no code, there is nothing to watch.
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

  return (
    <SettingsPage settings={settings}>
      {(configuration) => {
        const service = configuration.service ?? { address: "" };
        // What the daemon has, not what is in the box. An address typed and
        // not yet saved is not an address the device can link to, and offering
        // the button on the strength of it produced a refusal from the daemon
        // that read as a fault.
        const configured = (service.address ?? "").trim() !== "" && !settings.changed;
        return (
          <>
            <Section title="Service">
              <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
                Where this device reports to. Leave it empty and the device works entirely
                on its own, which is the normal state.
              </Typography>
              <Row>
                <Text label="Address" type="url" value={service.address ?? ""}
                  hint="For example https://example.com"
                  onChange={(value) => settings.change((draft) => {
                    draft.service = { ...(draft.service ?? { address: "" }), address: value };
                  })} />
              </Row>
            </Section>

            {said && <Alert severity="success" sx={{ mb: 2 }}>{said}</Alert>}
            {problem && <Alert severity="error" sx={{ mb: 2 }}>{problem}</Alert>}
            {state?.error && <Alert severity="error" sx={{ mb: 2 }}>{state.error}</Alert>}

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
                    {service.name !== configuration.device.name && (
                      <Typography component="span" variant="body2" color="text.secondary" sx={{ ml: 1 }}>
                        — this screen calls itself {configuration.device.name}
                      </Typography>
                    )}
                  </Readout>
                )}
                {service.deviceId && (
                  <Readout label="Known there as" mono>{service.deviceId}</Readout>
                )}
                <Typography variant="body2" color="text.secondary" sx={{ mt: 1.5, mb: 2 }}>
                  Unlinking forgets the credential and nothing else. The device keeps its
                  name, its screens and everything it is showing.
                </Typography>
                <Button color="error" variant="outlined" disabled={working}
                  onClick={() => void act(async () => {
                    const after = await api.forgetLink();
                    await settings.reload();
                    return after;
                  }, "This device is no longer linked.")}>
                  Unlink
                </Button>
              </Section>
            ) : state.pending && state.checking ? (
              <Section title="Checking">
                <Stack direction="row" spacing={1.5} alignItems="center" sx={{ mb: 1 }}>
                  <CircularProgress size={18} />
                  <Typography>
                    Authorised. Collecting the credential and checking it works.
                  </Typography>
                </Stack>
                <Typography variant="body2" color="text.secondary">
                  The code has done its job — you can put your phone away. This device
                  does not call itself linked until the service has answered to the
                  credential it was given.
                </Typography>
              </Section>
            ) : state.pending ? (
              <Section title="Scan to link">
                <Typography sx={{ mb: 2 }}>
                  Open this on a phone, sign in, and authorise the device.
                </Typography>
                <Box
                  component="img"
                  // The address carries when the attempt expires, because the
                  // code changes when the attempt does and the browser has no
                  // way to know that from the address alone.
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
                <Stack direction="row" spacing={1.5} alignItems="center">
                  <Button disabled={working} onClick={() => void act(api.abandonLink)}>
                    Cancel
                  </Button>
                  <Chip size="small" variant="outlined" icon={<CircularProgress size={12} />}
                    label="waiting for somebody to authorise it" />
                </Stack>
              </Section>
            ) : (
              <Section title="Not linked">
                <Typography sx={{ mb: 2 }}>
                  {configured
                    ? "Linking this device attaches it to an account, so it can be watched from there."
                    : settings.changed
                      ? "Save the address above before linking."
                      : "Set an address above, and save, before linking."}
                </Typography>
                <Button variant="contained" disabled={!configured || working}
                  onClick={() => void act(api.startLink)}>
                  Link this device
                </Button>
              </Section>
            )}
          </>
        );
      }}
    </SettingsPage>
  );
}
