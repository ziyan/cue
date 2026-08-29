import { useCallback, useEffect, useState } from "react";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import FormControlLabel from "@mui/material/FormControlLabel";
import Stack from "@mui/material/Stack";
import Switch from "@mui/material/Switch";
import Typography from "@mui/material/Typography";

import { api, type LogEntry } from "../api";
import { Section } from "../components/Section";

const colourOf = (severity?: string) =>
  severity === "error" ? "error.main" : severity === "warning" ? "warning.main" : "text.primary";

// The server stamps its lines with the kernel's monotonic clock. The daemon
// converts them where it can, using the one line that prints a wall clock
// beside one; where there is no such line -- a tail that starts past the
// header -- the raw reading is shown rather than an invented time.
function timeOf(entry: LogEntry): string {
  if (entry.at) {
    const when = new Date(entry.at);
    return `${when.toLocaleTimeString(undefined, { hour12: false })}.${String(when.getMilliseconds()).padStart(3, "0")}`;
  }
  if (entry.monotonic) return `+${entry.monotonic.toFixed(3)}`;
  return "";
}

export function Logs() {
  const [entries, setEntries] = useState<LogEntry[] | null>(null);
  const [problem, setProblem] = useState("");
  const [onlyProblems, setOnlyProblems] = useState(false);

  // Read on the way in. This was behind a button when the log was one card
  // among ten on a page about everything; on a page whose only subject is the
  // log, asking somebody to press "read the log" after opening the log is a
  // step that does nothing but delay.
  const load = useCallback(async () => {
    setProblem("");
    try {
      setEntries(await api.xorgLog());
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const shown = (entries ?? []).filter((one) =>
    !onlyProblems || one.severity === "error" || one.severity === "warning");

  return (
    <Section title="X server log">
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        When a screen stays black, the reason is in here. The server's own timestamps are seconds
        since the machine booted; these are the real times.
      </Typography>

      <Stack direction="row" spacing={2} alignItems="center" sx={{ mb: 2 }}>
        <Button variant="outlined" size="small" onClick={() => void load()}>Refresh</Button>
        <FormControlLabel
          control={<Switch size="small" checked={onlyProblems}
            onChange={(event) => setOnlyProblems(event.target.checked)} />}
          label="Warnings and errors only"
        />
      </Stack>

      {problem && <Alert severity="error">{problem}</Alert>}
      {!problem && entries === null && <Typography color="text.secondary">Reading…</Typography>}
      {entries !== null && shown.length === 0 && (
        <Typography color="text.secondary">
          {onlyProblems ? "Nothing the server called a warning or an error." : "The X server has not written a log yet."}
        </Typography>
      )}

      {shown.length > 0 && (
        <Box sx={{
          maxHeight: "60vh", overflow: "auto",
          bgcolor: "background.default", border: 1, borderColor: "divider",
          borderRadius: 1, p: 1,
          fontFamily: "ui-monospace, Menlo, Consolas, monospace", fontSize: "0.78rem",
        }}>
          {shown.map((entry, index) => (
            <Box key={index} sx={{ display: "flex", gap: 1.5, py: 0.15 }}>
              <Box component="span" sx={{ color: "text.disabled", flexShrink: 0 }}>{timeOf(entry)}</Box>
              {entry.severity && (
                <Box component="span" sx={{ color: colourOf(entry.severity), flexShrink: 0 }}>
                  {entry.severity}
                </Box>
              )}
              <Box component="span" sx={{ color: colourOf(entry.severity), overflowWrap: "anywhere" }}>
                {entry.text}
              </Box>
            </Box>
          ))}
        </Box>
      )}
    </Section>
  );
}
