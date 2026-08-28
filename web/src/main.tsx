import { StrictMode, useCallback, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router";
import Alert from "@mui/material/Alert";
import Box from "@mui/material/Box";
import CircularProgress from "@mui/material/CircularProgress";
import CssBaseline from "@mui/material/CssBaseline";
import { ThemeProvider } from "@mui/material/styles";

import { api, whenSignedOut, type SetupState } from "./api";
import { useAppearance } from "./appearance";
import { themeFor } from "./theme";
import { Shell } from "./Shell";
import { SignIn } from "./SignIn";
import { Placeholder } from "./pages/Placeholder";
import { allPages } from "./pages";

function App() {
  const [appearance, mode, chooseAppearance] = useAppearance();
  const [state, setState] = useState<SetupState | null>(null);
  const [problem, setProblem] = useState("");

  const look = useCallback(async () => {
    try {
      setState(await api.setupState());
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    }
  }, []);

  useEffect(() => {
    whenSignedOut(() => setState((was) => (was ? { ...was, signedIn: false } : was)));
    void look();
  }, [look]);

  const theme = themeFor(mode);

  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      {problem ? (
        <Box sx={{ p: 3 }}><Alert severity="error">{problem}</Alert></Box>
      ) : !state ? (
        <Box sx={{ display: "grid", placeItems: "center", minHeight: "100dvh" }}>
          <CircularProgress />
        </Box>
      ) : !state.signedIn ? (
        <SignIn name={state.device.name} onSignedIn={look} />
      ) : (
        <Routes>
          <Route
            element={
              <Shell
                state={state}
                appearance={appearance}
                onAppearance={chooseAppearance}
                onSignedOut={() => setState({ ...state, signedIn: false })}
              />
            }
          >
            {allPages.map((page) => (
              <Route key={page.path} path={page.path} element={<Placeholder title={page.title} />} />
            ))}
            <Route path="*" element={<Placeholder title="Not a page" />} />
          </Route>
        </Routes>
      )}
    </ThemeProvider>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter basename="/next">
      <App />
    </BrowserRouter>
  </StrictMode>,
);
