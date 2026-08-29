import type { ReactNode } from "react";
import Card from "@mui/material/Card";
import CardContent from "@mui/material/CardContent";
import Typography from "@mui/material/Typography";

// A card with a small heading. Every page is made of these.
export function Section({ title, children, action }: {
  title: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <Card sx={{ mb: 2 }}>
      <CardContent>
        <Typography variant="h2" color="text.secondary" sx={{ mb: 1.5,
          display: "flex", alignItems: "center", gap: 1 }}>
          <span>{title}</span>
          {action && <span style={{ marginLeft: "auto" }}>{action}</span>}
        </Typography>
        {children}
      </CardContent>
    </Card>
  );
}
