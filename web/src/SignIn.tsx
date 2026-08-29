import { useState, type FormEvent } from "react";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import Card from "@mui/material/Card";
import CardContent from "@mui/material/CardContent";
import Stack from "@mui/material/Stack";
import TextField from "@mui/material/TextField";
import Typography from "@mui/material/Typography";
import { api } from "./api";

export function SignIn({ name, onSignedIn }: { name?: string; onSignedIn: () => void }) {
  const [password, setPassword] = useState("");
  const [problem, setProblem] = useState("");
  const [trying, setTrying] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setProblem("");
    setTrying(true);
    try {
      await api.signIn(password);
      onSignedIn();
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
      setPassword("");
    } finally {
      setTrying(false);
    }
  };

  return (
    <Box sx={{ display: "grid", placeItems: "center", minHeight: "100dvh", p: 2 }}>
      <Card sx={{ width: "100%", maxWidth: 380 }}>
        <CardContent component="form" onSubmit={submit}>
          <Stack spacing={2}>
            <Box>
              <Typography variant="h6">{name || "Cue"}</Typography>
              <Typography variant="body2" color="text.secondary">
                Sign in to see and change what this screen shows.
              </Typography>
            </Box>
            {problem && <Alert severity="error">{problem}</Alert>}
            <TextField
              label="Password"
              type="password"
              autoComplete="current-password"
              autoFocus
              fullWidth
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
            <Button type="submit" variant="contained" disabled={trying || !password}>
              {trying ? "Signing in…" : "Sign in"}
            </Button>
          </Stack>
        </CardContent>
      </Card>
    </Box>
  );
}
