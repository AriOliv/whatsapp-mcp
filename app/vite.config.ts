import { defineConfig } from "vite";
import preact from "@preact/preset-vite";
import { viteSingleFile } from "vite-plugin-singlefile";

// The MCP App must be ONE self-contained HTML file (the host reads it as a
// ui:// resource and renders it in a sandboxed iframe with a strict CSP —
// no external scripts, styles or fonts). The Go server embeds the output.
export default defineConfig({
  plugins: [preact(), viteSingleFile()],
  build: {
    outDir: "../internal/mcpserver/ui",
    emptyOutDir: false,
    rollupOptions: { input: "index.html", output: { entryFileNames: "app.js" } },
    // singlefile inlines everything; the emitted index.html IS the app.

    assetsInlineLimit: 100_000_000,
    cssCodeSplit: false,
    target: "es2022",
    minify: true,
  },
});
