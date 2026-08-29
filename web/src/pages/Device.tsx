import Typography from "@mui/material/Typography";
import { Section } from "../components/Section";
import { Readout } from "../components/Readout";
import { Row, Text } from "../components/Fields";
import { SettingsPage, useSettings } from "../settings";

export function Device() {
  const settings = useSettings();
  return (
    <SettingsPage settings={settings}>
      {(configuration, status) => (
        <Section title="This device">
          <Row>
            <Text label="Name" value={configuration.device.name}
              onChange={(value) => settings.change((draft) => { draft.device.name = value; })} />
            <Text label="Where it is" value={configuration.device.location}
              onChange={(value) => settings.change((draft) => { draft.device.location = value; })} />
          </Row>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
            The language the screen itself speaks is in the top bar; the timezone is on the
            Time page.
          </Typography>
          <Readout label="Identifier" mono>{status ? status.device.identifier : "…"}</Readout>
          <Readout label="Version">{status ? status.device.version : "…"}</Readout>
          <Readout label="Daemon up">{status ? status.device.uptime : "…"}</Readout>
        </Section>
      )}
    </SettingsPage>
  );
}
