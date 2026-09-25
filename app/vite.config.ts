import { existsSync, renameSync } from "node:fs";
import { resolve } from "node:path";
import { defineConfig } from "vite";
import preact from "@preact/preset-vite";
import { viteSingleFile } from "vite-plugin-singlefile";

// The MCP App must be ONE self-contained HTML file (the host reads it as a
// ui:// resource and renders it in a sandboxed iframe with a strict CSP —
// no external scripts, styles or fonts). The Go server embeds the output.
/**
 * Rename the emitted document to app.html — the name go:embed reads.
 * Runs in closeBundle (after vite-plugin-singlefile has inlined everything and
 * the file is on disk), so the rename can never race the inlining.
 */
function emitAppHtml(outDir: string) {
  return {
    name: "emit-app-html",
    closeBundle() {
      const from = resolve(outDir, "index.html");
      const to = resolve(outDir, "app.html");
      if (existsSync(from)) renameSync(from, to);
    },
  };
}

const OUT_DIR = "../internal/mcpserver/ui";

export default defineConfig({
  plugins: [preact(), viteSingleFile(), emitAppHtml(OUT_DIR)],
  build: {
    outDir: OUT_DIR,
    emptyOutDir: false,
    rollupOptions: {
      input: "index.html",
      output: { entryFileNames: "app.js" },
    },

    assetsInlineLimit: 100_000_000,
    cssCodeSplit: false,
    target: "es2022",
    minify: true,
  },
});
