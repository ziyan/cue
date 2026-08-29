import { useCallback, useEffect, useState } from "react";
import type { Appearance } from "./theme";

const remembered = "cue.theme";

// Which appearance is in force, and how to change it.
//
// Three states, not two. A toggle can only say light or dark, and a screen set
// up in a room that darkens in the evening should be able to say "whatever the
// machine says" -- which is what it did before there was a control at all.
//
// Remembered in the browser it was set in rather than on the device: this is
// about the person looking, and two people looking after the same screen from
// different laptops need not agree.
export function useAppearance(): [Appearance, "light" | "dark", (next: Appearance) => void] {
  const [chosen, setChosen] = useState<Appearance>(() => {
    try {
      const value = localStorage.getItem(remembered);
      if (value === "light" || value === "dark") return value;
    } catch {
      // A private window refuses storage. Follow the system, as before.
    }
    return "system";
  });

  const [system, setSystem] = useState<"light" | "dark">(() =>
    window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light");

  useEffect(() => {
    const media = window.matchMedia?.("(prefers-color-scheme: dark)");
    if (!media) return;
    const listen = (event: MediaQueryListEvent) => setSystem(event.matches ? "dark" : "light");
    media.addEventListener("change", listen);
    return () => media.removeEventListener("change", listen);
  }, []);

  const choose = useCallback((next: Appearance) => {
    setChosen(next);
    try {
      if (next === "system") localStorage.removeItem(remembered);
      else localStorage.setItem(remembered, next);
    } catch {
      // Nothing to remember it in.
    }
  }, []);

  return [chosen, chosen === "system" ? system : chosen, choose];
}
