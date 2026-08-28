import { StrictMode, Suspense, lazy, useCallback, useEffect, useState } from "react";
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
import { Overview } from "./pages/Overview";
import { Device } from "./pages/Device";
import { Display } from "./pages/Display";
import { Browser } from "./pages/Browser";
import { Health } from "./pages/Health";
import { Access } from "./pages/Access";
import { Time } from "./pages/Time";
import { Logs } from "./pages/Logs";
import { Content } from "./pages/Content";
import { Network } from "./pages/Network";
// The viewer pulls noVNC in with it, which is the largest single thing in
// this interface and is wanted only by somebody who has asked to drive the
// screen. Loaded when that happens.
const Screen = lazy(() => import("./pages/Screen").then((module) => ({ default: module.Screen })));
import { Upgrade } from "./pages/Upgrade";
import { allPages } from "./pages";

// The pages that have been moved across. Anything not in here still shows a
// placeholder saying so, which keeps a half-finished port visible in the
// interface rather than only in the commit log.
const ported: Record<string, React.ReactElement> = {
  "/": <Overview />,
  "/device": <Device />,
  "/display": <Display />,
  "/browser": <Browser />,
  "/health": <Health />,
  "/access": <Access />,
  "/time": <Time />,
  "/logs": <Logs />,
  "/content": <Content />,
  "/network": <Network />,
  "/screen": <Suspense fallback={<Waiting />}><Screen /></Suspense>,
  "/upgrade": <Upgrade />,
};

function Waiting() {
  return (
    <Box sx={{ display: "grid", placeItems: "center", minHeight: "40vh" }}>
      <CircularProgress />
    </Box>
  );
}

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
              <Route
                key={page.path}
                path={page.path}
                element={ported[page.path] ?? <Placeholder title={page.title} />}
              />
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
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
