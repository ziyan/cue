import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// internal/web embeds the output directory, and an embed of a directory with
// nothing in it does not compile -- so a checkout that has never built the
// interface would not build at all. A committed placeholder guards that, and
// this puts it back after every build, because emptying the output directory
// is the first thing a build does and it takes the placeholder with it.
//
// It belongs here rather than in the Makefile: `npm run build` is run directly
// often enough, and when it was only in the Makefile the placeholder was
// deleted by one of those and committed away.
function keepTheDirectory(): Plugin {
  return {
    name: "cue-keep-the-directory",
    closeBundle() {
      writeFileSync(resolve(__dirname, "../internal/web/dist/.gitkeep"), "");
    },
  };
}

// The bundle is written into internal/web/dist because Go's embed cannot
// reach outside the directory of the package that declares it: the daemon
// embeds what is beside it, so the build has to put it there.
export default defineConfig({
  plugins: [react(), keepTheDirectory()],
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // Three pieces rather than one. The screen viewer is the largest
        // thing here and the page nobody opens by accident, so it is loaded
        // when somebody asks for it rather than on the way to the overview;
        // MUI is separated because it changes when a dependency is bumped and
        // not when this interface is edited, so a browser that has it keeps
        // it across releases.
        manualChunks(id: string) {
          if (id.includes("node_modules/@mui") || id.includes("node_modules/@emotion")) {
            return "mui";
          }
          return undefined;
        },
      },
    },
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
