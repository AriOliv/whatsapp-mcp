/**
 * Host bridge: the one place that talks to Claude (the MCP Apps host).
 *
 * Everything fragile about the protocol — the `ui/initialize` handshake,
 * autoResize (`ui/notifications/size-changed` via ResizeObserver), theming and
 * tool-call proxying — is owned by the official SDK's `App` class. A hand-rolled
 * bridge is what broke a previous iteration of this work (a missing
 * `protocolVersion` made the host drop every app→host message: buttons dead AND
 * the card stuck small, from one root cause).
 */
import {
  App,
  applyDocumentTheme,
  applyHostFonts,
  applyHostStyleVariables,
  type McpUiHostContext,
} from "@modelcontextprotocol/ext-apps";

export type { McpUiHostContext };

/** Apply theme, host style variables, fonts and safe-area insets. Best-effort. */
export function applyHostContext(ctx: Partial<McpUiHostContext> | undefined | null): void {
  if (!ctx) return;
  try {
    if (ctx.theme) applyDocumentTheme(ctx.theme);
    if (ctx.styles?.variables) applyHostStyleVariables(ctx.styles.variables);
    if (ctx.styles?.css?.fonts) applyHostFonts(ctx.styles.css.fonts);
  } catch {
    /* theming must never break rendering */
  }
  applySafeArea(ctx);
}

/**
 * Push the host's safe-area insets into CSS vars the layout pads with. Required
 * for borderless inline apps: with no host card padding to absorb them, controls
 * would otherwise sit under the mobile nav bar or the composer (which can
 * overlay the bottom edge on web too).
 */
function applySafeArea(ctx: Partial<McpUiHostContext>): void {
  const si = ctx.safeAreaInsets;
  if (!si || typeof si !== "object") return;
  const root = document.documentElement.style;
  const px = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? `${v}px` : "0px");
  root.setProperty("--safe-top", px(si.top));
  root.setProperty("--safe-right", px(si.right));
  root.setProperty("--safe-bottom", px(si.bottom));
  root.setProperty("--safe-left", px(si.left));
}

/** The single App instance. autoResize is on by default — never turn it off. */
export const app = new App({ name: "whatsapp-mcp", version: "1.0.0" });

/**
 * Tool payload: prefer `structuredContent`, fall back to parsing the JSON text
 * mirror (hosts/tools that predate structured output still send only text).
 */
export function readPayload(result: unknown): Record<string, unknown> {
  const res = result as { structuredContent?: unknown; content?: Array<{ text?: string }> } | null;
  if (!res) return {};
  if (res.structuredContent && typeof res.structuredContent === "object") {
    return res.structuredContent as Record<string, unknown>;
  }
  const text = res.content?.find((c) => typeof c?.text === "string")?.text;
  if (text) {
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed === "object") return parsed as Record<string, unknown>;
    } catch {
      /* not JSON: no structured payload to show */
    }
  }
  return {};
}

/**
 * Call a server tool and return its parsed payload (throws on tool error).
 * Returns `unknown`: the payload crosses the wire, so callers narrow it.
 */
export async function callTool(name: string, args: Record<string, unknown> = {}): Promise<unknown> {
  const res = await app.callServerTool({ name, arguments: args });
  if ((res as { isError?: boolean }).isError) {
    const msg = (res as { content?: Array<{ text?: string }> }).content?.find((c) => c?.text)?.text;
    throw new Error(msg || "tool call failed");
  }
  return readPayload(res);
}

/** Nudge the conversation: anything needing Claude's interpretation goes to chat. */
export function say(text: string): void {
  void app.sendMessage({ role: "user", content: [{ type: "text", text }] }).catch(() => {});
}
