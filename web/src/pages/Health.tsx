import { useState } from "react";
import Alert from "@mui/material/Alert";
import Button from "@mui/material/Button";
import Divider from "@mui/material/Divider";
import Stack from "@mui/material/Stack";
import Typography from "@mui/material/Typography";

import { api } from "../api";
import { Section } from "../components/Section";
import { Row, Text, Toggle } from "../components/Fields";
import { SettingsPage, useSettings } from "../settings";
import { asSeconds, secondsOf } from "../seconds";

interface Watchdog {
  enabled: boolean;
  interval?: string;
  timeout?: string;
  failuresBeforeReload: number;
  failuresBeforeRecreate: number;
  failuresBeforeClearCache: number;
  failuresBeforeRestart: number;
  failuresBeforeRestartDisplay: number;
}

const ladder: { name: keyof Watchdog; label: string; hint: string }[] = [
  { name: "failuresBeforeReload", label: "Reload the page after", hint: "consecutive failures" },
  { name: "failuresBeforeRecreate", label: "Open a fresh tab after", hint: "consecutive failures" },
  { name: "failuresBeforeClearCache", label: "Throw the cache away after", hint: "consecutive failures" },
  { name: "failuresBeforeRestart", label: "Restart the browser after", hint: "consecutive failures" },
  { name: "failuresBeforeRestartDisplay", label: "Restart the X server after", hint: "consecutive failures; 0 never does" },
];

export function Health() {
  const settings = useSettings();
  return (
    <>
      <SettingsPage settings={settings}>
        {(configuration) => {
          const watchdog = configuration.watchdog as unknown as Watchdog;
          const set = (change: (draft: Watchdog) => void) =>
            settings.change((draft) => change(draft.watchdog as unknown as Watchdog));

          return (
            <Section title="Watchdog">
              <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
                The daemon asks the page to prove it is still running — that the X server answers,
                that the page runs a line of JavaScript, and that it is still being drawn. A page
                can look perfect and be dead, so the last of those is the one that matters.
              </Typography>
              <Row>
                <Toggle label="Watch for a frozen screen" checked={watchdog.enabled}
                  onChange={(value) => set((draft) => { draft.enabled = value; })} />
                <Text label="Check every" type="number" value={secondsOf(watchdog.interval)} hint="Seconds"
                  onChange={(value) => set((draft) => { draft.interval = asSeconds(value); })} />
                <Text label="Give up on an answer after" type="number" value={secondsOf(watchdog.timeout)} hint="Seconds"
                  onChange={(value) => set((draft) => { draft.timeout = asSeconds(value); })} />
              </Row>

              {watchdog.enabled && (
                <>
                  <Divider sx={{ my: 2 }} />
                  <Typography variant="h2" color="text.secondary" sx={{ mb: 1.5 }}>
                    What it tries, in order
                  </Typography>
                  <Row>
                    {ladder.map((rung) => (
                      <Text
                        key={rung.name}
                        label={rung.label}
                        type="number"
                        hint={rung.hint}
                        value={String(watchdog[rung.name] ?? 0)}
                        onChange={(value) => set((draft) => {
                          (draft[rung.name] as number) = Math.max(0, parseInt(value, 10) || 0);
                        })}
                      />
                    ))}
                  </Row>
                </>
              )}
            </Section>
          );
        }}
      </SettingsPage>
      <RestartSomething />
    </>
  );
}

// Not part of the form: these happen when pressed, and have nothing to save.
function RestartSomething() {
  const [said, setSaid] = useState("");
  const [problem, setProblem] = useState("");
  const [address, setAddress] = useState("");

  const restart = async (program: string, name: string) => {
    setProblem(""); setSaid("");
    try {
      await api.restart(program);
      setSaid(`Restarted ${name}.`);
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    }
  };

  return (
    <Section title="Restart something">
      {said && <Alert severity="success" sx={{ mb: 2 }}>{said}</Alert>}
      {problem && <Alert severity="error" sx={{ mb: 2 }}>{problem}</Alert>}
      <Stack direction="row" spacing={1.5} flexWrap="wrap" useFlexGap sx={{ mb: 2 }}>
        <Button variant="outlined" onClick={() => void restart("chromium", "the browser")}>Restart the browser</Button>
        <Button variant="outlined" onClick={() => void restart("display", "the X server")}>Restart the X server</Button>
        <Button variant="outlined" onClick={() => void restart("vnc", "the VNC server")}>Restart the VNC server</Button>
      </Stack>
      <Row>
        <Text label="Show an address once, without changing the playlist" type="url"
          placeholder="https://example.com/" value={address} onChange={setAddress} />
      </Row>
      <Button
        variant="outlined"
        disabled={!address}
        onClick={async () => {
          setProblem(""); setSaid("");
          try {
            await api.navigate(address);
            setSaid("Showing it now. The next rotation puts the playlist back.");
          } catch (error) {
            setProblem(error instanceof Error ? error.message : String(error));
          }
        }}
      >
        Show it
      </Button>
    </Section>
  );
}
