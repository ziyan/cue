import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import Stack from "@mui/material/Stack";
import Typography from "@mui/material/Typography";
import { useBeforeUnload } from "react-router";

import { api, type Configuration } from "./api";
import type { Status } from "./status";

// One load and one save for every settings page, because they are all views of
// the same configuration document: saving on any of them sends the whole
// thing, as it always did.
//
// Whether there is anything to save is decided by comparing against the
// document as it arrived rather than by watching for edits. A field typed into
// and then typed back is not a change, and offering to discard nothing is a
// button that does nothing.
export function useSettings() {
  const [configuration, setConfiguration] = useState<Configuration | null>(null);
  const [status, setStatus] = useState<Status | null>(null);
  const [problem, setProblem] = useState("");
  const [saved, setSaved] = useState("");
  const asItArrived = useRef("");

  const load = useCallback(async () => {
    setProblem("");
    setSaved("");
    try {
      const [next, state] = await Promise.all([api.configuration(), api.status()]);
      asItArrived.current = JSON.stringify(next);
      setConfiguration(next);
      setStatus(state as unknown as Status);
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  // Changing anything replaces the whole document, so React sees a new object
  // and redraws. The callback is given a draft to modify.
  const change = useCallback((modify: (draft: Configuration) => void) => {
    setConfiguration((was) => {
      if (!was) return was;
      const draft = structuredClone(was);
      modify(draft);
      return draft;
    });
    setSaved("");
  }, []);

  const changed = configuration !== null && JSON.stringify(configuration) !== asItArrived.current;

  const save = useCallback(async () => {
    if (!configuration) return;
    setProblem("");
    try {
      const returned = await api.saveConfiguration(configuration);
      asItArrived.current = JSON.stringify(returned);
      setConfiguration(returned);
      setSaved("Saved.");
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    }
  }, [configuration]);

  // A reload or a closed tab with work in hand. Changing page inside the
  // interface is handled by the guard in SettingsPage below.
  useBeforeUnload(useCallback((event: BeforeUnloadEvent) => {
    if (changed) event.preventDefault();
  }, [changed]));

  return { configuration, status, problem, saved, changed, change, save, reload: load };
}

export type Settings = ReturnType<typeof useSettings>;

// The frame every settings page shares: what went wrong, what was saved, the
// page itself, and the two buttons.
export function SettingsPage({ settings, children }: {
  settings: Settings;
  children: (configuration: Configuration, status: Status) => ReactNode;
}) {
  const { configuration, status, problem, saved, changed, save, reload } = settings;

  if (problem && !configuration) return <Alert severity="error">{problem}</Alert>;
  if (!configuration || !status) return <Typography color="text.secondary">Loading…</Typography>;

  return (
    <Box>
      {problem && <Alert severity="error" sx={{ mb: 2 }}>{problem}</Alert>}
      {saved && <Alert severity="success" sx={{ mb: 2 }}>{saved}</Alert>}
      {(status.ignoredSettings ?? []).length > 0 && (
        <Alert severity="warning" sx={{ mb: 2 }}>
          The configuration file has settings this version does not have. They are ignored, and
          will be removed from the file the next time it is written:{" "}
          {(status.ignoredSettings ?? []).join(", ")}. If one of these is a key you meant to set,
          it is mistyped — from in front of the screen a mistyped key and a setting that does
          nothing look exactly the same.
        </Alert>
      )}

      {children(configuration, status)}

      <Stack direction="row" spacing={1.5} sx={{ mt: 2 }}>
        <Button variant="contained" onClick={() => void save()} disabled={!changed}>Save</Button>
        {changed && <Button onClick={() => void reload()}>Discard changes</Button>}
      </Stack>
    </Box>
  );
}
