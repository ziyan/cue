import Card from "@mui/material/Card";
import CardContent from "@mui/material/CardContent";
import Typography from "@mui/material/Typography";

// Every page has one of these until it is ported. Named rather than blank so
// that a half-finished port is obvious from the interface rather than from the
// commit log.
export function Placeholder({ title }: { title: string }) {
  return (
    <Card>
      <CardContent>
        <Typography variant="h2" gutterBottom>{title}</Typography>
        <Typography color="text.secondary">
          This page has not been moved to the new interface yet.
        </Typography>
      </CardContent>
    </Card>
  );
}
