import { useCallback, useEffect, useState } from "react";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import Chip from "@mui/material/Chip";
import CircularProgress from "@mui/material/CircularProgress";
import Divider from "@mui/material/Divider";
import List from "@mui/material/List";
import ListItemButton from "@mui/material/ListItemButton";
import ListItemText from "@mui/material/ListItemText";
import LockOutlinedIcon from "@mui/icons-material/LockOutlined";
import SignalWifi4BarIcon from "@mui/icons-material/SignalWifi4Bar";
import SignalWifi3BarIcon from "@mui/icons-material/NetworkWifi3Bar";
import SignalWifi2BarIcon from "@mui/icons-material/NetworkWifi2Bar";
import SignalWifi1BarIcon from "@mui/icons-material/NetworkWifi1Bar";
import Typography from "@mui/material/Typography";

import { api, type Interface, type NetworkState, type WirelessNetwork } from "../api";
import { Section } from "../components/Section";
import { Readout } from "../components/Readout";
import { Row, Choice, Text, Toggle } from "../components/Fields";
import { SettingsPage, useSettings } from "../settings";
import { asSeconds, secondsOf } from "../seconds";
import { bytes } from "../format";

interface InterfaceSettings {
  name: string;
  method?: string;
  address?: string;
  gateway?: string;
  nameservers?: string[];
  searchDomain?: string;
  wireless?: { ssid: string; passphrase: string };
}

interface NetworkSettings {
  manage?: boolean;
  onboarding?: string;
  lostAfter?: string;
  interfaces?: InterfaceSettings[];
}

function signalIcon(dBm: number) {
  if (dBm >= -55) return <SignalWifi4BarIcon fontSize="small" />;
  if (dBm >= -67) return <SignalWifi3BarIcon fontSize="small" />;
  if (dBm >= -78) return <SignalWifi2BarIcon fontSize="small" />;
  return <SignalWifi1BarIcon fontSize="small" />;
}

function signalWord(strength: number) {
  if (strength >= -55) return "(strong)";
  if (strength >= -70) return "(usable)";
  return "(weak — the screen will drop out)";
}

export function Network() {
  const settings = useSettings();
  const [state, setState] = useState<NetworkState | null>(null);

  useEffect(() => {
    void api.network().then(setState).catch(() => setState({ interfaces: [] }));
  }, []);

  return (
    <SettingsPage settings={settings}>
      {(configuration) => {
        const network = configuration.network as unknown as NetworkSettings;
        const set = (change: (draft: NetworkSettings) => void) =>
          settings.change((draft) => change(draft.network as unknown as NetworkSettings));

        // Only what somebody would plug a cable into or join a network with.
        //
        // The filter used to exclude "loopback", which is not one of the kinds
        // the daemon reports -- so it matched nothing, and the page listed
        // docker0, the loopback and every veth a container brought with it.
        // The daemon calls all of those "virtual", and none of them is
        // something an operator configures; offering them invites somebody to
        // try.
        const interfaces = (state?.interfaces ?? []).filter((one) => one.kind !== "virtual");

        return (
          <>
            {state?.problem && <Alert severity="warning" sx={{ mb: 2 }}>{state.problem}</Alert>}

            <Section title="Managing the network">
              <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
                Off, and the machine keeps whatever network setup it already has — which for a
                screen plugged into a wired network is an address it was given without being
                asked, and nothing to do here. On, and this daemon sets the interfaces you name
                below: what a screen on a wireless network needs, and what a screen that has to
                sit at a fixed address needs.
              </Typography>
              <Toggle label="Set the network from here" checked={!!network.manage}
                onChange={(value) => set((draft) => { draft.manage = value; })} />
            </Section>

            <Section title="Setting up from a phone">
              <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
                A device with no network can run a temporary wireless network of its own and show
                a code on its screen. Scanning that code with a phone joins it and opens a page
                for choosing the real network, so a screen in a room with no ethernet can still be
                set up. The password for that temporary network appears only on the screen.
              </Typography>
              <Row>
                <Choice label="When to offer it" value={network.onboarding ?? "auto"}
                  options={[
                    { value: "auto", label: "Only when this device has no network" },
                    { value: "always", label: "Whenever the hardware allows" },
                    { value: "off", label: "Never" },
                  ]}
                  onChange={(value) => set((draft) => { draft.onboarding = value; })} />
                <Text label="Give up on the network after, in minutes" type="number"
                  value={String(Math.round((parseInt(secondsOf(network.lostAfter) || "600", 10)) / 60))}
                  hint="How long with no network before the setup code appears"
                  onChange={(value) => set((draft) => {
                    draft.lostAfter = asSeconds(String(Math.max(1, parseInt(value, 10) || 10) * 60), 60);
                  })} />
              </Row>
            </Section>

            {interfaces.length === 0 && (
              <Section title="No network hardware">
                <Typography color="text.secondary">
                  This process can see no network interfaces. In a container that usually means it
                  was not given the host's network.
                </Typography>
              </Section>
            )}

            {interfaces.map((one) => (
              <InterfaceCard
                key={one.name}
                one={one}
                error={state?.errors?.[one.name]}
                network={network}
                set={set}
              />
            ))}
          </>
        );
      }}
    </SettingsPage>
  );
}

function InterfaceCard({ one, error, network, set }: {
  one: Interface;
  error?: string;
  network: NetworkSettings;
  set: (change: (draft: NetworkSettings) => void) => void;
}) {
  const entry = (network.interfaces ?? []).find((each) => each.name === one.name);
  const managed = !!entry;

  return (
    <Section title={`${one.name} · ${one.kind}`}>
      <Readout label="State">
        <Chip size="small" variant="outlined" color={one.carrier ? "success" : "warning"}
          label={one.carrier ? "carrier" : one.up ? "up, no carrier" : "down"} />
      </Readout>
      <Readout label="Address" mono>{(one.addresses ?? []).join(", ") || "none"}</Readout>
      {one.gateway && <Readout label="Gateway" mono>{one.gateway}</Readout>}
      {(one.nameservers ?? []).length > 0 && (
        <Readout label="Name servers" mono>{(one.nameservers ?? []).join(", ")}</Readout>
      )}
      <Readout label="Carried">
        {bytes(one.receivedBytes)} in, {bytes(one.transmittedBytes)} out
      </Readout>
      {one.wireless && (
        <>
          <Readout label="Wireless">
            {one.wireless.ssid
              ? `${one.wireless.ssid} · ${one.wireless.state.toLowerCase()}`
              : one.wireless.state.toLowerCase() || "not joined"}
          </Readout>
          {one.wireless.signalStrength && (
            <Readout label="Signal">
              {one.wireless.signalStrength} dBm {signalWord(one.wireless.signalStrength)}
            </Readout>
          )}
        </>
      )}
      {error && <Alert severity="error" sx={{ mt: 2 }}>{error}</Alert>}

      <Divider sx={{ my: 2 }} />
      <Toggle
        label="Set this interface from here"
        checked={managed}
        onChange={(value) => set((draft) => {
          const list = draft.interfaces ?? (draft.interfaces = []);
          if (value) list.push({ name: one.name, method: "dhcp" });
          else draft.interfaces = list.filter((each) => each.name !== one.name);
        })}
      />

      {managed && entry && (
        <Box sx={{ mt: 2 }}>
          <AddressForm entry={entry} name={one.name} set={set} />
          {one.kind === "wireless" && <WirelessForm entry={entry} name={one.name} set={set} />}
        </Box>
      )}
    </Section>
  );
}

function AddressForm({ entry, name, set }: {
  entry: InterfaceSettings;
  name: string;
  set: (change: (draft: NetworkSettings) => void) => void;
}) {
  const change = (modify: (draft: InterfaceSettings) => void) =>
    set((draft) => {
      const found = (draft.interfaces ?? []).find((each) => each.name === name);
      if (found) modify(found);
    });

  return (
    <>
      <Row>
        <Choice label="Address" value={entry.method ?? "dhcp"}
          options={[{ value: "dhcp", label: "dhcp" }, { value: "static", label: "static" }]}
          onChange={(value) => change((draft) => {
            draft.method = value;
            if (value !== "static") draft.address = "";
          })} />
      </Row>
      {entry.method === "static" && (
        <Row>
          <Text label="Address and prefix" value={entry.address ?? ""} hint="for example 192.0.2.10/24"
            onChange={(value) => change((draft) => { draft.address = value; })} />
          <Text label="Gateway" value={entry.gateway ?? ""} hint="the router that reaches everything else"
            onChange={(value) => change((draft) => { draft.gateway = value; })} />
        </Row>
      )}
      <Row>
        <Text label="Name servers" value={(entry.nameservers ?? []).join(", ")}
          hint="Empty uses whatever the network offers"
          onChange={(value) => change((draft) => {
            draft.nameservers = value.split(",").map((each) => each.trim()).filter(Boolean);
          })} />
        <Text label="Search domain" value={entry.searchDomain ?? ""}
          onChange={(value) => change((draft) => { draft.searchDomain = value; })} />
      </Row>
    </>
  );
}

// Choosing from what is in the room is the ordinary way to join a wireless
// network; typing the name is the exception, for one that does not broadcast.
function WirelessForm({ entry, name, set }: {
  entry: InterfaceSettings;
  name: string;
  set: (change: (draft: NetworkSettings) => void) => void;
}) {
  const [found, setFound] = useState<WirelessNetwork[] | null>(null);
  const [looking, setLooking] = useState(false);

  const look = useCallback(async () => {
    setLooking(true);
    try {
      const answer = await api.scanWireless(name);
      setFound(answer.networks ?? []);
    } catch {
      setFound([]);
    } finally {
      setLooking(false);
    }
  }, [name]);

  // Look as soon as the form appears, rather than waiting to be asked.
  useEffect(() => { void look(); }, [look]);

  const change = (modify: (draft: InterfaceSettings) => void) =>
    set((draft) => {
      const one = (draft.interfaces ?? []).find((each) => each.name === name);
      if (one) {
        one.wireless = one.wireless ?? { ssid: "", passphrase: "" };
        modify(one);
      }
    });

  const chosen = entry.wireless?.ssid ?? "";

  return (
    <>
      <Typography variant="h2" color="text.secondary" sx={{ mt: 2, mb: 1 }}>Network</Typography>
      {looking && found === null && (
        <Box sx={{ display: "flex", alignItems: "center", gap: 1.5, py: 1 }}>
          <CircularProgress size={18} />
          <Typography color="text.secondary">Looking for networks…</Typography>
        </Box>
      )}
      {found !== null && found.length === 0 && (
        <Typography color="text.secondary" sx={{ py: 1 }}>
          Nothing was found. The radio may be off, or there may be nothing in range.
        </Typography>
      )}
      {found !== null && found.length > 0 && (
        <List dense sx={{ border: 1, borderColor: "divider", borderRadius: 1, p: 0, maxHeight: 260, overflow: "auto" }}>
          {found.map((candidate) => (
            <ListItemButton
              key={candidate.ssid}
              selected={candidate.ssid === chosen}
              onClick={() => change((draft) => { draft.wireless!.ssid = candidate.ssid; })}
              sx={{ borderRadius: 0 }}
            >
              <ListItemText primary={candidate.ssid} />
              {candidate.security && candidate.security !== "open" && (
                <LockOutlinedIcon fontSize="small" sx={{ color: "text.disabled", mr: 1 }} />
              )}
              {signalIcon(candidate.signalStrength)}
            </ListItemButton>
          ))}
        </List>
      )}
      <Button size="small" onClick={() => void look()} disabled={looking} sx={{ my: 1.5 }}>
        {looking ? "Looking…" : "Look again"}
      </Button>
      <Row>
        <Text label="Password" type="password" value={entry.wireless?.passphrase ?? ""}
          hint={chosen ? `For ${chosen}. Empty for an open network.` : "Empty for an open network"}
          onChange={(value) => change((draft) => { draft.wireless!.passphrase = value; })} />
        <Text label="Or type a name" value={chosen}
          hint="For a network that does not announce itself"
          onChange={(value) => change((draft) => { draft.wireless!.ssid = value; })} />
      </Row>
    </>
  );
}
