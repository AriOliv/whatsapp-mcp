/** Dev-only config for the card preview harness (never shipped). */
import { defineConfig } from "vite";
import preact from "@preact/preset-vite";

export default defineConfig({
  root: "preview",
  plugins: [preact()],
  server: { port: 5199, strictPort: true },
});
