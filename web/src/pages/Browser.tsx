import Typography from "@mui/material/Typography";
import TextField from "@mui/material/TextField";
import { Section } from "../components/Section";
import { Row, Text, Toggle } from "../components/Fields";
import { SettingsPage, useSettings } from "../settings";

interface BrowserSettings {
  darkMode?: boolean;
  forceDarkContent?: boolean;
  sandbox?: boolean;
  ignoreCertificateErrors?: boolean;
  ephemeralCache?: boolean;
  deviceScaleFactor?: number;
  closeUnexpectedTabs?: boolean;
  certificateAuthorities?: string[];
}

export function Browser() {
  const settings = useSettings();
  return (
    <SettingsPage settings={settings}>
      {(configuration) => {
        const browser = configuration.browser as unknown as BrowserSettings;
        const set = (change: (draft: BrowserSettings) => void) =>
          settings.change((draft) => change(draft.browser as unknown as BrowserSettings));

        return (
          <Section title="Browser">
            <Row>
              <Toggle label="Ask pages for their dark version" checked={!!browser.darkMode}
                hint="A dashboard on a wall in a dark room at full brightness is what people complain about first. Pages that offer a dark theme are asked for it."
                onChange={(value) => set((draft) => { draft.darkMode = value; })} />
              <Toggle label="Keep the browser sandbox on" checked={!!browser.sandbox}
                hint="Leave on. Off removes the boundary between a page and this machine, and is only for a container that cannot be given the privileges the sandbox needs."
                onChange={(value) => set((draft) => { draft.sandbox = value; })} />
            </Row>
            <Row>
              <Toggle label="Darken pages that ignore it" checked={!!browser.forceDarkContent}
                hint="Some pages have a theme of their own and take no notice of what the browser prefers. This inverts their colours anyway. It is not as good as a page's own dark theme, so leave it off unless the screen is still bright."
                onChange={(value) => set((draft) => { draft.forceDarkContent = value; })} />
              <Toggle label="Accept certificates that do not verify" checked={!!browser.ignoreCertificateErrors}
                hint="For an appliance on a private network with its own certificate. It removes the protection TLS was there to give, on every page, so turn it on only for a network you control."
                onChange={(value) => set((draft) => { draft.ignoreCertificateErrors = value; })} />
            </Row>
            <Row>
              <Toggle label="Forget everything on restart" checked={!!browser.ephemeralCache}
                hint="Starts with an empty profile every time. It cures a corrupted cache permanently, at the cost of signing in again after every restart."
                onChange={(value) => set((draft) => { draft.ephemeralCache = value; })} />
              <Toggle label="Close windows this daemon did not open" checked={!!browser.closeUnexpectedTabs}
                hint="A page that opens a window gets one stacked in front of the dashboard, and with no window manager it stays there. Windows are given a moment to close themselves first, and what was closed is written to the log."
                onChange={(value) => set((draft) => { draft.closeUnexpectedTabs = value; })} />
            </Row>
            <Row>
              <Text label="Scale" type="number" value={browser.deviceScaleFactor ?? 1}
                hint="1 gives a page the pixels the screen actually has. Raise it for a screen somebody stands close to. What the browser decides from what the panel claims to be is often nonsense."
                onChange={(value) => set((draft) => {
                  draft.deviceScaleFactor = Math.max(0.5, Math.min(4, parseFloat(value) || 1));
                })} />
            </Row>

            <TextField
              label="Certificates this device trusts"
              multiline minRows={3} fullWidth size="small" sx={{ mt: 1 }}
              value={(browser.certificateAuthorities ?? []).join("\n")}
              onChange={(event) => set((draft) => {
                draft.certificateAuthorities = event.target.value
                  .split(/\n{2,}/).map((one) => one.trim()).filter(Boolean);
              })}
              helperText="PEM certificates, one after another. For an internal certificate authority whose dashboards would otherwise fail to load."
            />

            <Typography variant="body2" color="text.secondary" sx={{ mt: 2 }}>
              Changing any of these restarts the browser.
            </Typography>
          </Section>
        );
      }}
    </SettingsPage>
  );
}
