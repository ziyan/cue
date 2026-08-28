import type { ReactNode } from "react";
import Box from "@mui/material/Box";
import Typography from "@mui/material/Typography";

// A label on the left and a value on the right, with a rule between rows.
//
// The value wraps rather than being cut off: this is read on a phone, and a
// truncated address or processor name says less than a wrapped one.
export function Readout({ label, children, mono }: {
  label: ReactNode;
  children: ReactNode;
  mono?: boolean;
}) {
  return (
    <Box
      sx={{
        display: "flex", alignItems: "baseline", gap: 2, py: 0.75,
        borderBottom: 1, borderColor: "divider",
        "&:last-of-type": { borderBottom: 0, pb: 0 },
      }}
    >
      <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>
        {label}
      </Typography>
      <Box
        sx={{
          ml: "auto", minWidth: 0, textAlign: "right",
          fontFamily: mono ? "ui-monospace, Menlo, Consolas, monospace" : undefined,
          fontSize: mono ? "0.85em" : undefined,
          overflowWrap: "anywhere",
        }}
      >
        {children}
      </Box>
    </Box>
  );
}
