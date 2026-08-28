import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The bundle is written into internal/web/dist because Go's embed cannot
// reach outside the directory of the package that declares it: the daemon
// embeds what is beside it, so the build has to put it there.
export default defineConfig({
  plugins: [react()],
  // Served under /next while the pages are moved across, so the interface
  // people are actually using goes on working at / until this one replaces it.
  base: "/next/",
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
    // A device is fetched from over a local network by one person at a time.
    // Splitting into many small chunks buys nothing here and makes the embed
    // a longer list of files.
    chunkSizeWarningLimit: 1500,
  },
  server: {
    // `npm run dev` against a real device: everything the interface asks for
    // goes to the daemon, and only the page itself is served locally.
    proxy: {
      "/api": { target: process.env.CUE ?? "http://127.0.0.1:8080", changeOrigin: true },
      "/healthz": { target: process.env.CUE ?? "http://127.0.0.1:8080", changeOrigin: true },
    },
  },
});
