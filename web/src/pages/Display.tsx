import Chip from "@mui/material/Chip";
import Divider from "@mui/material/Divider";
import Table from "@mui/material/Table";
import TableBody from "@mui/material/TableBody";
import TableCell from "@mui/material/TableCell";
import TableHead from "@mui/material/TableHead";
import TableRow from "@mui/material/TableRow";
import TextField from "@mui/material/TextField";
import Typography from "@mui/material/Typography";

import { Section } from "../components/Section";
import { Row, Choice, Text, Toggle } from "../components/Fields";
import { SettingsPage, useSettings } from "../settings";
import { asSeconds, secondsOf } from "../seconds";

interface Connector {
  name: string;
  connected: boolean;
  monitor?: string;
  modes?: string[];
}

interface Output {
  name: string;
  mode?: string;
  rotate?: string;
  position?: string;
}

interface DisplaySettings {
  outputs?: Output[];
  blankAfter?: string;
  framebuffer?: string;
  cursor?: string;
  cursorIdleTimeout?: string;
  wallpaper?: boolean;
  mirror?: boolean;
  modeline?: string;
  xorgConfiguration?: string;
}

export function Display() {
  const settings = useSettings();
  return (
    <SettingsPage settings={settings}>
      {(configuration, status) => {
        const display = configuration.display as unknown as DisplaySettings;
        const outputs = display.outputs ?? [];
        const connectors = ((status as unknown as { connectors?: Connector[] }).connectors) ?? [];

        const set = (change: (draft: DisplaySettings) => void) =>
          settings.change((draft) => change(draft.display as unknown as DisplaySettings));

        const sockets = ["*", ...connectors.map((one) => one.name)];
        // The modes the monitor on that socket actually advertises. A mode
        // typed by hand that the monitor does not have is a black screen, and
        // the monitor has already said what it can do.
        const modesFor = (name: string) => {
          const modes = ["preferred", "off"];
          for (const connector of connectors.filter((one) => name === "*" || !name || one.name === name)) {
            for (const mode of connector.modes ?? []) if (!modes.includes(mode)) modes.push(mode);
          }
          return modes;
        };

        return (
          <>
            <Section title="Sockets">
              {connectors.length === 0 ? (
                <Typography color="text.secondary">
                  The X server is not reporting any sockets yet.
                </Typography>
              ) : (
                <Table size="small">
                  <TableHead>
                    <TableRow>
                      <TableCell>Socket</TableCell>
                      <TableCell>Monitor</TableCell>
                      <TableCell>Best mode</TableCell>
                      <TableCell align="right">State</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {connectors.map((one) => (
                      <TableRow key={one.name}>
                        <TableCell sx={{ fontFamily: "ui-monospace, monospace" }}>{one.name}</TableCell>
                        <TableCell>{one.monitor || "—"}</TableCell>
                        <TableCell sx={{ fontFamily: "ui-monospace, monospace" }}>
                          {one.modes?.[0] ?? "—"}
                        </TableCell>
                        <TableCell align="right">
                          <Chip size="small" variant="outlined"
                            color={one.connected ? "success" : "default"}
                            label={one.connected ? "plugged in" : "empty"} />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </Section>

            <Section title="Screen">
              <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
                An entry named * applies to every socket that no other entry names, which is why
                this works on a machine nobody has looked at.
              </Typography>

              {outputs.map((output, index) => (
                <Row key={index}>
                  <Choice label="Socket" value={output.name} hint="* means every socket on the machine"
                    options={sockets.map((one) => ({ value: one, label: one }))}
                    onChange={(value) => set((draft) => { draft.outputs![index]!.name = value; })} />
                  <Choice label="Size" value={output.mode ?? "preferred"}
                    hint="preferred is the monitor's own native size, and is nearly always right"
                    options={modesFor(output.name).map((one) => ({ value: one, label: one }))}
                    onChange={(value) => set((draft) => { draft.outputs![index]!.mode = value; })} />
                  <Choice label="Which way up" value={output.rotate ?? "normal"}
                    options={["normal", "left", "right", "inverted"].map((one) => ({ value: one, label: one }))}
                    onChange={(value) => set((draft) => { draft.outputs![index]!.rotate = value; })} />
                </Row>
              ))}

              <Row>
                <Text label="Blank the screen after" type="number" value={secondsOf(display.blankAfter)}
                  hint="Seconds of no input. 0 never blanks, which is what a wall display wants"
                  onChange={(value) => set((draft) => { draft.blankAfter = asSeconds(value, 0); })} />
              </Row>

              <Divider sx={{ my: 2 }} />
              <Typography variant="h2" color="text.secondary" sx={{ mb: 1.5 }}>
                Colour, size and the console
              </Typography>
              <Row>
                <Toggle label="Show the same picture on every screen" checked={display.mirror !== false}
                  hint="On, a second screen plugged in shows what the first one shows, at the largest size they both have. Off lays them out side by side into one wide desktop, which is what a video wall wants and what everything else does not. Where each screen sits is ignored while this is on."
                  onChange={(value) => set((draft) => { draft.mirror = value; })} />
              </Row>
              {display.mirror === false && outputs.map((output, index) => (
                <Row key={index}>
                  <Text label={`Where ${output.name} sits`} value={output.position ?? ""}
                    hint="0x0. Only means anything with more than one screen"
                    onChange={(value) => set((draft) => { draft.outputs![index]!.position = value; })} />
                </Row>
              ))}
              <Row>
                <Text label="Force the drawing surface size" value={display.framebuffer ?? ""}
                  hint="Empty fits the screens; 1920x1080 for a television that lies about its size"
                  onChange={(value) => set((draft) => { draft.framebuffer = value; })} />
                <Choice label="Mouse pointer" value={display.cursor ?? "auto"}
                  hint="Auto shows it while somebody is moving it and hides it again when they stop. Hidden means the screen has no pointer at all, which makes a touchscreen or a mouse impossible to aim."
                  options={["auto", "hidden", "always"].map((one) => ({ value: one, label: one }))}
                  onChange={(value) => set((draft) => { draft.cursor = value; })} />
                {(display.cursor ?? "auto") === "auto" && (
                  <Text label="Hide it again after" type="number" value={secondsOf(display.cursorIdleTimeout)}
                    hint="Seconds of not moving"
                    onChange={(value) => set((draft) => { draft.cursorIdleTimeout = asSeconds(value, 1); })} />
                )}
                <Toggle label="Show the Cue mark before the browser has drawn" checked={!!display.wallpaper}
                  hint="What the screen shows while it is starting, and if the browser goes away. Off leaves whatever the X server does, which on a wall is indistinguishable from a machine that failed to boot."
                  onChange={(value) => set((draft) => { draft.wallpaper = value; })} />
              </Row>

              <Divider sx={{ my: 2 }} />
              <Typography variant="h2" color="text.secondary" sx={{ mb: 1.5 }}>
                Graphics driver and screen size
              </Typography>
              <Row>
                <Text label="Custom modeline" value={display.modeline ?? ""}
                  hint="For a television with a broken EDID, in xrandr --newmode format"
                  onChange={(value) => set((draft) => { draft.modeline = value; })} />
              </Row>
              <TextField
                label="Extra X server configuration"
                multiline minRows={4} fullWidth size="small"
                value={display.xorgConfiguration ?? ""}
                onChange={(event) => set((draft) => { draft.xorgConfiguration = event.target.value; })}
                sx={{ "& textarea": { fontFamily: "ui-monospace, monospace", fontSize: "0.85em" } }}
              />
            </Section>
          </>
        );
      }}
    </SettingsPage>
  );
}
