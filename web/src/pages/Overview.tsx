import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import Chip from "@mui/material/Chip";
import Grid from "@mui/material/Grid";
import Typography from "@mui/material/Typography";

import { api } from "../api";
import type { Status } from "../status";
import { Section } from "../components/Section";
import { Readout } from "../components/Readout";
import { Meter } from "../components/Meter";
import { ago, bytes, duration, percentage } from "../format";

const howOften = 3000;

export function Overview() {
  const [status, setStatus] = useState<Status | null>(null);
  const [problem, setProblem] = useState("");

  useEffect(() => {
    let stopped = false;
    const look = async () => {
      try {
        const next = await api.status() as unknown as Status;
        if (!stopped) { setStatus(next); setProblem(""); }
      } catch (error) {
        if (!stopped) setProblem(error instanceof Error ? error.message : String(error));
      }
    };
    void look();
    const timer = setInterval(look, howOften);
    return () => { stopped = true; clearInterval(timer); };
  }, []);

  if (problem) return <Alert severity="error">{problem}</Alert>;
  if (!status) return <Typography color="text.secondary">Looking…</Typography>;

  return (
    <Grid container spacing={2}>
      <Grid size={{ xs: 12, lg: 6 }}>
        <OnTheScreen status={status} />
        <TheMachine status={status} />
      </Grid>
      <Grid size={{ xs: 12, lg: 6 }}>
        <Programs status={status} />
        <Watchdog status={status} />
        <Display status={status} />
      </Grid>
    </Grid>
  );
}

// The screenshot answers "what is it showing" without a VNC connection and
// without leaving the desk, which makes it the most useful thing on the page.
function OnTheScreen({ status }: { status: Status }) {
  const image = useRef<HTMLImageElement | null>(null);
  const [stale, setStale] = useState(false);
  const [everArrived, setEverArrived] = useState(false);

  useEffect(() => {
    let stopped = false;
    const fetchOne = () => {
      // Decoded before it is shown, so that swapping it in replaces one
      // complete picture with another. A fresh <img> is empty until its
      // picture arrives, and on a 2560x1440 screen that gap was the most
      // obvious thing on the page, three times a minute.
      const incoming = new Image();
      incoming.onload = () => {
        if (stopped || !image.current) return;
        image.current.src = incoming.src;
        setStale(false);
        setEverArrived(true);
      };
      // Keep whatever is already there: a screenshot that failed once is
      // usually a browser restarting, and an empty card says less than the
      // last picture with a note that it is old.
      incoming.onerror = () => { if (!stopped) setStale(true); };
      incoming.src = `/api/v1/screenshot.png?small=1&at=${Date.now()}`;
    };
    fetchOne();
    const timer = setInterval(fetchOne, howOften);
    return () => { stopped = true; clearInterval(timer); };
  }, []);

  const showing = status.browser.currentTitle || status.browser.currentUrl || "nothing yet";

  return (
    <Section title="On the screen">
      {!everArrived && (
        <Alert severity="info" sx={{ mb: 1.5 }}>No picture yet — the browser is still starting.</Alert>
      )}
      {/* The picture is the obvious thing to press when what you want is to
          drive the screen, so it goes there. */}
      <Box
        component={Link}
        to="/screen"
        title="Open the screen"
        sx={{ display: everArrived ? "block" : "none", mb: 1.5 }}
      >
        <Box
          component="img"
          ref={image}
          alt="What the screen is showing"
          sx={{
            width: "100%", display: "block",
            borderRadius: 1, border: 1, borderColor: "divider",
            opacity: stale ? 0.5 : 1, transition: "opacity 0.2s ease, border-color 0.2s ease",
            "&:hover": { borderColor: "primary.main" },
          }}
        />
      </Box>
      <Readout label="Showing">{showing}</Readout>
      {status.browser.currentUrl && (
        <Readout label="Address" mono>{status.browser.currentUrl}</Readout>
      )}
    </Section>
  );
}

function Programs({ status }: { status: Status }) {
  const failing = status.programs.filter((one) => one.lastError);
  return (
    <Section title="Programs">
      {status.programs.length === 0 && (
        <Typography color="text.secondary">Nothing is running yet.</Typography>
      )}
      {status.programs.map((program) => (
        <Readout
          key={program.name}
          label={
            <>
              {program.name}
              {program.restarts > 0 && ` · ${program.restarts} restart${program.restarts === 1 ? "" : "s"}`}
            </>
          }
        >
          <Chip
            size="small"
            label={program.state}
            color={program.state === "running" ? "success" : program.state === "backoff" ? "error" : "warning"}
            variant="outlined"
          />
          <Typography component="span" variant="body2" color="text.secondary" sx={{ ml: 1 }}>
            {program.startedAt ? ago(program.startedAt) : ""}
          </Typography>
        </Readout>
      ))}
      {failing.map((program) => (
        <Alert key={program.name} severity="error" sx={{ mt: 1.5 }}>
          {program.name}: {program.lastError}
        </Alert>
      ))}
    </Section>
  );
}

function Watchdog({ status }: { status: Status }) {
  const watchdog = status.watchdog;
  if (!watchdog.enabled) {
    return (
      <Section title="Watchdog">
        <Typography color="text.secondary">Switched off. A frozen screen will stay frozen.</Typography>
      </Section>
    );
  }

  const healthy = watchdog.consecutiveFailures === 0;
  return (
    <Section title="Watchdog">
      <Readout label="Now">
        <Chip
          size="small"
          variant="outlined"
          color={healthy ? "success" : "error"}
          label={watchdog.suspended ? "paused" : healthy ? "answering" : `${watchdog.consecutiveFailures} failed probes`}
        />
      </Readout>
      <Readout label="Last answer">{ago(watchdog.lastSuccessAt)}</Readout>
      <Readout label="Rescued">
        {watchdog.remediesApplied} time{watchdog.remediesApplied === 1 ? "" : "s"}
      </Readout>
      {watchdog.lastRemedy && (
        <Readout label="Last action">{watchdog.lastRemedy}, {ago(watchdog.lastRemedyAt)}</Readout>
      )}
      {watchdog.lastFailure && <Alert severity="error" sx={{ mt: 1.5 }}>{watchdog.lastFailure}</Alert>}
    </Section>
  );
}

function TheMachine({ status }: { status: Status }) {
  const machine = status.machine;
  const hottest = (machine.thermal ?? []).reduce<{ name: string; celsius: number } | null>(
    (best, sensor) => (best && best.celsius >= sensor.celsius ? best : sensor), null);

  return (
    <Section title="This machine">
      <Meter label="Processor" value={`${machine.cpu.usagePercent.toFixed(0)}%`} percent={machine.cpu.usagePercent} />
      <Meter
        label="Memory"
        value={`${bytes(machine.memory.used)} of ${bytes(machine.memory.total)}`}
        percent={percentage(machine.memory.used, machine.memory.total)}
      />
      {(machine.disks ?? []).map((disk) => (
        <Meter
          key={disk.path}
          label={`Disk ${disk.path}`}
          value={`${bytes(disk.used)} of ${bytes(disk.total)}`}
          percent={percentage(disk.used, disk.total)}
        />
      ))}
      <Box sx={{ mt: 1 }}>
        <Readout label="Load">{machine.loadAverage.map((one) => one.toFixed(2)).join("  ")}</Readout>
        {hottest && <Readout label="Temperature">{hottest.celsius.toFixed(0)} °C ({hottest.name})</Readout>}
        <Readout label="Machine up">{duration(machine.uptime)}</Readout>
        <Readout label="Processor">{machine.cpu.model || `${machine.cpu.count} cores`}</Readout>
      </Box>
    </Section>
  );
}

function Display({ status }: { status: Status }) {
  const outputs = (status.outputs ?? []).filter((one) => one.connected);
  const connectors = status.connectors ?? [];

  return (
    <Section title="Display">
      <Readout label="Screen">
        {status.screen.width ? `${status.screen.width} × ${status.screen.height}` : "not up yet"}
      </Readout>
      {outputs.length === 0 ? (
        <Typography color="text.secondary" sx={{ py: 1 }}>
          The X server is not reporting any connected output.
        </Typography>
      ) : outputs.map((output) => (
        <Readout key={output.name} label={<>{output.name}{output.primary ? " · primary" : ""}</>}>
          {output.enabled ? `${output.currentMode} at ${output.x},${output.y}` : "connected, off"}
        </Readout>
      ))}
      {connectors.length > 0 && (
        <Readout label="Sockets">
          {connectors.map((one) => `${one.name}${one.connected ? "" : " (empty)"}`).join(", ")}
        </Readout>
      )}
    </Section>
  );
}
