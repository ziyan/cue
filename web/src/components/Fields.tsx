import type { ReactNode } from "react";
import Box from "@mui/material/Box";
import FormControlLabel from "@mui/material/FormControlLabel";
import FormHelperText from "@mui/material/FormHelperText";
import MenuItem from "@mui/material/MenuItem";
import Switch from "@mui/material/Switch";
import TextField from "@mui/material/TextField";

// Two to a row on anything wider than a phone, one on a phone.
export function Row({ children }: { children: ReactNode }) {
  return (
    <Box sx={{
      display: "grid", gap: 2, mb: 1,
      gridTemplateColumns: { xs: "1fr", sm: "repeat(2, minmax(0, 1fr))" },
      alignItems: "start",
    }}>
      {children}
    </Box>
  );
}

export function Text({ label, value, onChange, hint, type = "text", ...rest }: {
  label: string;
  value: string | number;
  onChange: (value: string) => void;
  hint?: string;
  type?: string;
  placeholder?: string;
}) {
  return (
    <TextField
      label={label}
      type={type}
      value={value ?? ""}
      onChange={(event) => onChange(event.target.value)}
      helperText={hint}
      size="small"
      fullWidth
      {...rest}
    />
  );
}

export function Choice({ label, value, onChange, options, hint }: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  hint?: string;
}) {
  // A value the device already has but this list does not -- a mode a monitor
  // stopped offering, a timezone from a newer database -- is added rather than
  // silently replaced by whatever happens to be first.
  const known = options.some((one) => one.value === value);
  const all = known || !value
    ? options
    : [{ value, label: `${value} (not offered now)` }, ...options];

  return (
    <TextField
      select
      label={label}
      value={value ?? ""}
      onChange={(event) => onChange(event.target.value)}
      helperText={hint}
      size="small"
      fullWidth
    >
      {all.map((one) => <MenuItem key={one.value} value={one.value}>{one.label}</MenuItem>)}
    </TextField>
  );
}

export function Toggle({ label, checked, onChange, hint }: {
  label: string;
  checked: boolean;
  onChange: (value: boolean) => void;
  hint?: string;
}) {
  return (
    <Box>
      <FormControlLabel
        control={<Switch checked={!!checked} onChange={(event) => onChange(event.target.checked)} />}
        label={label}
      />
      {hint && <FormHelperText sx={{ mt: 0 }}>{hint}</FormHelperText>}
    </Box>
  );
}
