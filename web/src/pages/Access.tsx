import Typography from "@mui/material/Typography";
import { Section } from "../components/Section";
import { Readout } from "../components/Readout";
import { Row, Choice, Text, Toggle } from "../components/Fields";
import { SettingsPage, useSettings } from "../settings";
import { asSeconds, secondsOf } from "../seconds";

interface InputDevice {
  name: string;
  keyboard?: boolean;
  pointer?: boolean;
  touch?: boolean;
  direct?: boolean;
}

interface SoundDevice {
  identifier: string;
  name?: string;
  alsaName?: string;
  playback?: boolean;
  capture?: boolean;
}

function describe(one: InputDevice): string {
  const kinds: string[] = [];
  if (one.touch && one.direct) kinds.push("touchscreen");
  else if (one.touch) kinds.push("touchpad");
  if (one.keyboard) kinds.push("keyboard");
  if (one.pointer && !one.touch) kinds.push("pointer");
  return kinds.join(", ") || "other";
}

export function Access() {
  const settings = useSettings();
  return (
    <SettingsPage settings={settings}>
      {(configuration, status) => {
        const audio = configuration.audio as { enabled: boolean; sink: string; volume: number };
        const vnc = configuration.vnc as { listen: string; password: string; viewOnly: boolean };
        const web = configuration.web as { listen: string; sessionLifetime: string };
        const log = configuration.log as { level: string };
        const inputs = ((status as unknown as { input?: InputDevice[] }).input ?? [])
          .filter((one) => one.keyboard || one.pointer || one.touch);
        const sound = (status as unknown as { sound?: SoundDevice[] }).sound ?? [];

        return (
          <>
            <Section title="Keyboard, mouse and touch">
              {inputs.length === 0 ? (
                <Typography color="text.secondary">
                  The kernel reports no keyboard, pointer or touchscreen. Inside a container that
                  usually means /dev/input was not passed through.
                </Typography>
              ) : inputs.map((one) => (
                <Readout key={one.name} label={one.name}>{describe(one)}</Readout>
              ))}
            </Section>

            <Section title="Sound">
              <Row>
                <Toggle label="Play sound" checked={audio.enabled}
                  onChange={(value) => settings.change((draft) => {
                    (draft.audio as { enabled: boolean }).enabled = value;
                  })} />
                <Text label="Sound card" value={audio.sink}
                  hint={sound.length
                    ? `Empty lets ALSA choose. Available: ${sound.map((one) => one.alsaName || `plughw:${one.identifier}`).join(", ")}`
                    : "This machine reports no sound cards"}
                  onChange={(value) => settings.change((draft) => {
                    (draft.audio as { sink: string }).sink = value;
                  })} />
                <Text label="Volume" type="number" value={audio.volume} hint="0 to 100"
                  onChange={(value) => settings.change((draft) => {
                    (draft.audio as { volume: number }).volume =
                      Math.min(100, Math.max(0, parseInt(value, 10) || 0));
                  })} />
              </Row>
              {sound.map((one) => (
                <Readout key={one.identifier} label={one.name || one.identifier}>
                  plughw:{one.identifier} {one.playback ? "out" : ""}{one.capture ? " in" : ""}
                </Readout>
              ))}
            </Section>

            <Section title="Remote access">
              <Row>
                <Text label="VNC listens on" value={vnc.listen}
                  hint="127.0.0.1:5900 keeps it behind this interface"
                  onChange={(value) => settings.change((draft) => {
                    (draft.vnc as { listen: string }).listen = value;
                  })} />
                <Text label="VNC password" type="password" value={vnc.password}
                  hint="Only needed if you move it off the loopback address"
                  onChange={(value) => settings.change((draft) => {
                    (draft.vnc as { password: string }).password = value;
                  })} />
                <Toggle label="VNC viewers may only watch, not type" checked={vnc.viewOnly}
                  onChange={(value) => settings.change((draft) => {
                    (draft.vnc as { viewOnly: boolean }).viewOnly = value;
                  })} />
              </Row>
              <Row>
                <Choice label="Log level" value={(log.level || "NOTICE").toUpperCase()}
                  hint="NOTICE is right for a screen. INFO includes everything the X server says, which is a great deal."
                  options={["DEBUG", "INFO", "NOTICE", "WARNING", "ERROR", "CRITICAL"]
                    .map((one) => ({ value: one, label: one }))}
                  onChange={(value) => settings.change((draft) => {
                    (draft.log as { level: string }).level = value;
                  })} />
                <Text label="This interface listens on" value={web.listen}
                  hint="Changing this needs a restart of the container"
                  onChange={(value) => settings.change((draft) => {
                    (draft.web as { listen: string }).listen = value;
                  })} />
                <Text label="Stay signed in for" type="number" value={secondsOf(web.sessionLifetime)}
                  hint="Seconds. Long, because signing in to a screen on a wall is a trip across the building"
                  onChange={(value) => settings.change((draft) => {
                    (draft.web as { sessionLifetime: string }).sessionLifetime = asSeconds(value, 60);
                  })} />
              </Row>
            </Section>
          </>
        );
      }}
    </SettingsPage>
  );
}
