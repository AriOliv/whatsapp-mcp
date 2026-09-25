# WhatsApp MCP App

The interactive UI Claude renders for this server's tools — chats, conversations,
contacts, groups, media and confirmations as cards inside the conversation,
instead of raw JSON.

Built to the [Claude MCP Apps design guidelines][guidelines] on the official
[`@modelcontextprotocol/ext-apps`][sdk] SDK.

[guidelines]: https://claude.com/docs/connectors/building/mcp-apps/design-guidelines
[sdk]: https://github.com/modelcontextprotocol/ext-apps

## How it fits together

```
tool call ──▶ Go handler ──▶ structuredContent { kind: "chats", … }   (view.go)
                  │
                  └─ _meta.ui.resourceUri = ui://whatsapp/app.html    (app.go)
                                    │
host reads the resource ────────────┘
                  │
                  ▼
        app.html (one self-contained file)
                  │
        App.ontoolresult ──▶ router (App.tsx) ──▶ card by `kind`
```

**One resource backs every card.** Tools point at the same `ui://` resource and
the card is chosen by a `kind` discriminator in the payload. That means one
"Always allow" prompt for the user and one bundle to ship, rather than one per
tool. The wire contract lives in two files that must stay in sync:
`internal/mcpserver/view.go` (source of truth) and `src/lib/types.ts`.

**The SDK owns the protocol.** The `ui/initialize` handshake, autoResize
(`ui/notifications/size-changed` via ResizeObserver), theming and tool proxying
all come from the SDK's `App` class. This is deliberate: hand-rolling that
bridge previously shipped a version where a missing `protocolVersion` made the
host drop every app→host message — dead buttons *and* a card stuck at the wrong
size, from a single root cause.

## Layout

```
src/
  main.tsx          entry: registers handlers, then connects
  App.tsx           router — picks a card from `kind`, owns fullscreen/drill-in
  lib/host.ts       the only module that talks to the host
  lib/types.ts      wire contract (mirrors view.go)
  lib/format.ts     locale-aware dates, numbers, phones
  ui/               design system: tokens.css, bubbles.css, Icon, atoms
  cards/            one file per card family
preview/            local harness (dev only, never shipped)
```

## Developing

```bash
npm install
npm run build          # → ../internal/mcpserver/ui/app.html (what Go embeds)
npm run preview:cards  # http://localhost:5199 — every card, real fixtures
npm run typecheck
```

`preview:cards` renders all cards against fixtures with the host's documented
light/dark token values, plus toggles for theme, inline/fullscreen and a 375px
viewport. Use it before deploying: it is far faster than a deploy-and-look loop,
and it has already caught real defects (dark mode not applying, washed-out
bubbles, a carousel that hid its own scrollability).

To exercise the real protocol, point a host at the server and call a tool —
Claude Desktop caches `tools/list` and the resource, so **reconnect the
connector** after changing either.

## Design rules this app follows

- **Host tokens for everything structural.** Colors, type, radii, borders and
  shadows alias `--color-*` / `--font-*` / `--border-radius-*` with light
  fallbacks. Dark mode needs no rules of its own — the host swaps the values.
  The single brand value is the WhatsApp-green accent, used only for identity
  and the primary action.
- **Transparent by default.** `prefersBorder: false`, a transparent body and
  `<meta name="color-scheme" content="light dark">` so cards sit *on* the chat
  surface rather than in a nested box.
- **Inline stays small.** Short lists, at most two actions, no nested scrolling,
  no drill-ins. Vertical pans inside an inline app belong to the conversation,
  so anything that needs its own scroll asks for fullscreen instead.
- **Visible controls.** Segmented buttons and chips, never dropdowns or popovers
  (the host container clips them).
- **44px tap targets**, safe-area insets honored as padding, skeletons instead of
  spinners, and text alternatives on every icon-only control.

## Payload budget

A tool result over ~150k characters is spilled to the host's sandbox filesystem;
the app then receives a file pointer and never hydrates. So:

- list tools take `query` + `limit` and return a capped page,
- details are fetched on demand with `app.callServerTool`,
- media is never inlined in a list — the card downloads it when asked.

## Adding a card

1. Add the payload struct + `kind` constant in `internal/mcpserver/view.go`.
2. Mirror it in `src/lib/types.ts`.
3. Write the component in `src/cards/`.
4. Register it in the `App.tsx` switch and add a fixture in `preview/fixtures.ts`.
5. Wrap the tool with `withUI(...)` and return the new view.
6. `npm run build` and commit the regenerated `app.html` (CI fails if it drifts).
