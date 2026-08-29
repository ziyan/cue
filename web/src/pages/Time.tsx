import Chip from "@mui/material/Chip";
import { Section } from "../components/Section";
import { Readout } from "../components/Readout";
import { Row, Text, Toggle } from "../components/Fields";
import { SettingsPage, useSettings } from "../settings";

interface Clock {
  enabled?: boolean;
  synchronised?: boolean;
  reference?: string;
  offsetSeconds?: number;
}

export function Time() {
  const settings = useSettings();
  return (
    <SettingsPage settings={settings}>
      {(configuration, status) => {
        const time = configuration.time as { enabled: boolean; servers?: string[] };
        const clock = (status as unknown as { clock?: Clock }).clock ?? {};
        return (
          <Section title="Time">
            <Row>
              <Text label="Timezone" value={configuration.device.timezone}
                hint="What the screen and these logs call the time. It does not change the machine's own setting, which lives outside the container."
                onChange={(value) => settings.change((draft) => { draft.device.timezone = value; })} />
              <Toggle
                label="Keep this device's clock"
                checked={time.enabled}
                hint="On by default: a clock wrong by minutes makes every HTTPS dashboard refuse to load. Turn it off where the machine already runs chrony or systemd-timesyncd."
                onChange={(value) => settings.change((draft) => {
                  (draft.time as { enabled: boolean }).enabled = value;
                })}
              />
            </Row>
            {time.enabled && (
              <Row>
                <Text label="Time servers" value={(time.servers ?? []).join(", ")}
                  hint="Separated by commas. Empty uses pool.ntp.org."
                  onChange={(value) => settings.change((draft) => {
                    (draft.time as { servers: string[] }).servers =
                      value.split(",").map((one) => one.trim()).filter(Boolean);
                  })} />
              </Row>
            )}
            <Readout label="Clock">
              {clock.enabled ? (
                <Chip size="small" variant="outlined"
                  color={clock.synchronised ? "success" : "warning"}
                  label={clock.synchronised ? `synchronised with ${clock.reference}` : "not synchronised yet"} />
              ) : (
                <Chip size="small" variant="outlined" label="not managed here" />
              )}
            </Readout>
            {clock.enabled && clock.synchronised && (
              <Readout label="Off by">{((clock.offsetSeconds ?? 0) * 1000).toFixed(0)} ms</Readout>
            )}
          </Section>
        );
      }}
    </SettingsPage>
  );
}
