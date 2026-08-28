import Box from "@mui/material/Box";
import LinearProgress from "@mui/material/LinearProgress";
import Typography from "@mui/material/Typography";

// A number with a bar under it. Amber from three quarters, red from nine
// tenths -- the thresholds the interface has always used.
export function Meter({ label, value, percent }: {
  label: string;
  value: string;
  percent: number;
}) {
  const colour = percent >= 90 ? "error" : percent >= 75 ? "warning" : "primary";
  return (
    <Box sx={{ py: 0.75 }}>
      <Box sx={{ display: "flex", gap: 2, mb: 0.5 }}>
        <Typography variant="body2" color="text.secondary">{label}</Typography>
        <Typography variant="body2" sx={{ ml: "auto" }}>{value}</Typography>
      </Box>
      <LinearProgress variant="determinate" value={percent} color={colour}
        sx={{ height: 6, borderRadius: 3 }} />
    </Box>
  );
}
