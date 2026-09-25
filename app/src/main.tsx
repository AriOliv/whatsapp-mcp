/**
 * Entry point. Registers handlers BEFORE connecting — `ontoolresult` never
 * fires until the handshake completes, and a result pushed during boot would
 * otherwise be missed.
 */
import { render } from "preact";
import "./ui/tokens.css";
import "./ui/bubbles.css";
import { App } from "./App";
import { app, applyHostContext } from "./lib/host";
import { setLocale } from "./lib/format";

render(<App />, document.getElementById("root")!);

app
  .connect()
  .then(() => {
    const ctx = app.getHostContext();
    applyHostContext(ctx);
    setLocale(ctx?.locale, ctx?.timeZone);
  })
  .catch(() => {
    /* Outside a host (or handshake refused) the card still renders its shell. */
  });
