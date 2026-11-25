# WhatsApp MCP Server <img src="assets/logo.svg" align="right" width="96"/>

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Node.js](https://img.shields.io/badge/node-%E2%89%A520-brightgreen.svg)](https://nodejs.org/)
[![TypeScript](https://img.shields.io/badge/TypeScript-5-blue.svg)](https://www.typescriptlang.org/)
[![MCP](https://img.shields.io/badge/MCP-stdio%20%7C%20HTTP%2BOAuth-purple.svg)](https://modelcontextprotocol.io/)

Unofficial [Model Context Protocol](https://modelcontextprotocol.io/) server for **WhatsApp**, built on top of the [Evolution API](https://github.com/EvolutionAPI/evolution-api) (Baileys). Read your chats, catch up on who's waiting for a reply, send messages, manage groups and labels — from any AI assistant that speaks MCP.

> Not affiliated with WhatsApp / Meta. Wraps a self‑hosted Evolution API for personal use. Automating a personal WhatsApp account is against WhatsApp's Terms — use at your own risk.

## What This MCP Server Does

Exposes the Evolution API as MCP tools so an AI (Claude Code, Claude Desktop, Cursor, …) can:

- 💬 **Read** conversations, chats and contacts (`find_chats`, `find_messages`, `find_contacts`)
- ✅ Triage who is **pending your reply**, mark read/unread, archive
- ✉️ **Send** text, media, audio, stickers, locations, contacts, reactions, **polls**, lists
- 👥 Manage **groups** (create, participants, subject, invite link, leave)
- 🏷️ Manage **labels**, presence, profile and privacy settings
- 📇 Check numbers, fetch profiles / business profiles and profile pictures

The same declarative tool catalog backs both transports below.

## Interactive UI (MCP App)

In Claude desktop/web the tools render as **cards inside the conversation**
instead of raw JSON — chats with previews, a conversation thread with
play/download for voice notes and photos, contacts, a swipeable groups carousel,
group detail with participants, plus confirmations for every send, and cards for
number checks, media, polls, locations, privacy, business profiles, invite links
and account state.

It follows the [Claude MCP Apps design guidelines][mcp-apps-guidelines] and is
built on the official [`@modelcontextprotocol/ext-apps`][ext-apps] SDK: all
structural styling comes from the host's own style tokens (so light/dark follow
Claude automatically), cards stay within the inline budget and expand to
fullscreen for search and drill-in.

Hosts without MCP Apps support are unaffected — they ignore `_meta.ui` and still
receive the text plus structured content.

See [`app/README.md`](app/README.md) for the architecture and how to add a card.

[mcp-apps-guidelines]: https://claude.com/docs/connectors/building/mcp-apps/design-guidelines
[ext-apps]: https://github.com/modelcontextprotocol/ext-apps

## Two Modes

| Mode | Transport | "Login" | Best for |
| --- | --- | --- | --- |
| **HTTP + OAuth 2.1** | Streamable HTTP | **WhatsApp device pairing** — scan a QR (or type an 8‑digit code) on a hosted login page | Multi‑user / remote deploys, sharing with others |
| **stdio** | stdin / stdout | Point it at an already‑connected instance via `.env` | Local single‑user setup, fastest to wire up |

### The pairing flow (the HTTP "login")

Where other MCP servers log you into a web account, here the OAuth `authorize` step drops you on a **pairing page**. The server provisions a fresh Evolution instance for your flow, shows a **QR code** (auto‑rotating) with an **8‑digit pairing‑code** fallback, and polls the connection state. The moment WhatsApp links the device, the server captures the paired **phone number as your identity**, mints the OAuth token, and hands it back to your MCP client. Each user drives **their own** WhatsApp number — the server is multi‑tenant.

```
MCP client → /authorize → /login (QR + code) → scan on phone
            → instance "open" → number becomes your sub → OAuth token → /mcp
```

## Prerequisites

- [Node.js](https://nodejs.org/en/download) **≥ 20**
- A running **[Evolution API](https://github.com/EvolutionAPI/evolution-api)** (v2.x) reachable from this server, and its global `AUTHENTICATION_API_KEY`. A ready `docker-compose.yml` lives in `../evolution-api` of this workspace.

## Installation

```bash
git clone https://github.com/AriOliv/whatsapp-mcp.git
cd whatsapp-mcp
npm install
npm run build
cp .env.example .env   # then edit
```

## Running

### stdio (local, single user)

Set `EVO_BASE_URL`, `EVO_API_KEY` (the global key) and `EVO_INSTANCE` (an instance you already connected via the Evolution Manager). Then register it with your MCP client:

```jsonc
// claude_desktop_config.json  (or: claude mcp add)
{
  "mcpServers": {
    "whatsapp": {
      "command": "node",
      "args": ["/absolute/path/to/whatsapp-mcp/build/index.js"],
      "env": {
        "EVO_BASE_URL": "http://127.0.0.1:8080",
        "EVO_API_KEY": "<your AUTHENTICATION_API_KEY>",
        "EVO_INSTANCE": "my-instance"
      }
    }
  }
}
```

In stdio mode the full catalog is available, including instance lifecycle tools
(`evo_instance_create`, `evo_instance_connect`, …) and the `evo_use_instance`
meta‑tool to switch the active instance at runtime.

### HTTP + OAuth (remote, multi‑tenant)

```bash
# .env: PORT, PUBLIC_URL, MCP_JWT_SECRET (≥32 chars), EVO_BASE_URL, EVO_GLOBAL_API_KEY
npm run start:http
```

Point your MCP client at `${PUBLIC_URL}/mcp`. It discovers the OAuth endpoints,
opens the pairing page, and once you link your phone you're connected.
Instance‑admin tools are **hidden** in this mode — pairing owns the instance
lifecycle, and `evo_instance_list` is withheld so tenants can't see each other's
instances.

Health check: `GET ${PUBLIC_URL}/healthz`.

## Gateway REST API

Besides the per-user OAuth `/mcp`, HTTP mode can expose `/api/*` for one trusted backend that uses a paired number as its WhatsApp front door: send text, media, buttons, lists and forms, presence/read receipts, media download and account status. Set `GATEWAY_API_TOKEN` (static bearer, ≥24 chars) and `GATEWAY_ACCOUNTS` (the numbers it may drive). Inbound messages, button taps and submitted forms are pushed to `INBOUND_WEBHOOK_URL`, HMAC-signed with `INBOUND_WEBHOOK_SECRET`.

The full contract is in [`openapi.json`](openapi.json) (OpenAPI 3.1): open it in [Swagger Editor](https://editor.swagger.io) or import it in Postman (*Import → File*), then set the bearer token on the collection. A test fails if a route is added without being documented there.

## OAuth mode (Go server)

With `MCP_MODE=http` and the default `MCP_AUTH_MODE=oauth`, the server is its own OAuth 2.1 authorization server and WhatsApp pairing is the login, so the token's `sub` is the paired number. Clients such as Claude Code register themselves (DCR) and use a loopback redirect.

| Variable | Default | Meaning |
| --- | --- | --- |
| `PUBLIC_URL` | `http://localhost:3000` | Issuer. Access tokens carry `aud` = `PUBLIC_URL/mcp` and `/mcp` rejects any other audience |
| `MCP_JWT_SECRET` / `MCP_JWT_SECRET_FILE` | none | Signing secret (32+ chars), or a file holding it |
| `OAUTH_ALLOWED_REDIRECT_HOSTS` | `claude.ai,claude.com` | https hosts a registered client may redirect to. http redirects are only accepted on `localhost`, `127.0.0.1` or `[::1]` with an explicit port |
| `FORMS_ENABLED` | `false` | Mounts `/f/*` for web forms sent over WhatsApp |
| `GATEWAY_API_TOKEN` | none | Mounts `/api/*` only when set |
| `MEDIA_URL_ALLOWED_HOSTS`, `SEND_RATE_PER_MINUTE`, `SEND_RATE_BURST`, `WA_HISTORY_SYNC` | see gateway mode | Same meaning as in gateway mode. Media by URL goes through the SSRF-safe fetcher and history sync is off by default in every HTTP mode |

`/register`, `/authorize` and `/login*` are rate limited per client address, registered clients are capped, and pairing flows are capped globally and per address and torn down after five minutes. `/healthz` reports counts only. Logging an account out, from the tool or from the phone, deletes what the server stored for it and revokes its refresh tokens

## Gateway auth mode (Go server)

When the server runs behind an MCP gateway that already signs users in, set `MCP_MODE=http` and `MCP_AUTH_MODE=gateway`. The gateway must send `Authorization: Bearer <JWT>` on every MCP request, signed RS256 with a key published at its JWKS URL, carrying `kid` in the header and `iss`, `aud`, `exp` and `sub` (the user's email) in the claims. Every `tools/call` needs that user token. The gateway's own catalog and health traffic (`initialize`, `ping`, `notifications/*`, `tools/list`, alone or batched together, and session GET and DELETE) is served without one, and if it carries a token that token must still be a valid gateway token, whatever its `sub`. A batch that mixes `tools/call` with those methods needs the user token. The built-in OAuth server, the login and pairing pages, `/api/*` and `/f/*` are not mounted in this mode. Only `/mcp` and `/healthz` exist, and `/healthz` returns `{"ok":true}` and nothing else.

| Variable | Required | Meaning |
| --- | --- | --- |
| `MCP_AUTH_MODE` | yes | `gateway` (default `oauth`) |
| `GATEWAY_JWKS_URL` | yes | The gateway's JWKS document, e.g. `https://gateway.example.com/.well-known/jwks.json`. Keys are cached and refetched (rate limited) when an unknown `kid` shows up |
| `GATEWAY_JWT_ISSUER` | yes | Exact `iss` value |
| `GATEWAY_JWT_AUDIENCE` | yes | Exact `aud` value |
| `ALLOWED_EMAIL_DOMAINS` | yes | Comma-separated domains. `sub` must be an email whose domain matches one exactly |
| `MEDIA_URL_ALLOWED_HOSTS` | no | Comma-separated hosts that media URLs may point to. `.example.com` also allows subdomains |
| `SEND_RATE_PER_MINUTE` / `SEND_RATE_BURST` | no | Per-number send budget, default 20 per minute with bursts of 5 |
| `WA_HISTORY_SYNC` | no | History sync is off by default in this mode. Set `on` to enable it |
| `DATABASE_URL_FILE` | no | Path to a file holding the database URL, used when `DATABASE_URL` is unset (works in every mode) |

Each user links their own number with the `whatsapp_connect` tool, which returns an 8-digit code to type under WhatsApp > Linked devices > Link with phone number. The number is bound to that user only after WhatsApp confirms the link, a number can belong to one user at most, and every other tool acts on the caller's bound number only. `whatsapp_connection_status` shows the state, and `whatsapp_disconnect` unlinks the device and deletes everything stored for that number. Media given by URL must be `https` and is refused if it resolves to a private, loopback, link-local or otherwise internal address

## Architecture

```
src/
├── types.ts              # McpToolDefinition + CredentialProvider interfaces
├── tools.ts              # declarative catalog (~55 tools) + HIDDEN_IN_HTTP set
├── dispatch.ts           # generic executeTool: definition + args → HTTP → CallToolResult
├── credentials.ts        # stdio CredentialProvider (env + ~/.evoapi-mcp.json)
├── index.ts              # stdio entrypoint (McpServer + StdioServerTransport)
├── http-server.ts        # HTTP entrypoint (Streamable HTTP + OAuth)
└── http/
    ├── store.ts            # in‑memory OAuth stores + per‑user WhatsApp credentials
    ├── provider.ts         # WhatsAppOAuthProvider (OAuth 2.1, JWT via jose)
    ├── session-provider.ts # resolves a user's instance+apikey per request
    ├── evolution.ts        # pairing‑time Evolution client (create/connect/state)
    └── login-router.ts     # the QR / pairing‑code login page + status polling
```

The connection design mirrors the sibling `@aol/uber-mcp` and `@aol/ifood-mcp`
servers: a declarative tool array, a single generic dispatcher, and a
transport‑agnostic credential provider — swap the provider and the same tools run
over stdio or over multi‑tenant HTTP.

### Go server (whatsmeow) + MCP App

The deployed server is the Go one: it talks to WhatsApp directly through
[whatsmeow](https://github.com/tulir/whatsmeow) (no Evolution API in the loop)
and embeds the MCP App UI.

```
cmd/whatsapp-mcp/       # entrypoint (MCP_MODE=stdio|http)
internal/
├── wa/                 # whatsmeow client registry, pairing, send/read helpers
├── store/              # our own chat/message history (whatsmeow keeps none)
├── oauth/              # OAuth 2.1 + per-user bearer (multi-tenant HTTP)
├── gateway/            # gateway auth mode: JWT check, owner bindings, pairing
├── safehttp/           # SSRF-safe fetcher for media given by URL
└── mcpserver/
    ├── server.go       # the whatsapp_* tool catalog
    ├── view.go         # card payloads (wire contract for the app)
    ├── app.go          # the ui:// resource + per-tool _meta.ui
    └── ui/app.html     # built app bundle (committed; go:embed reads it)
app/                    # MCP App source (Preact + Vite) → ui/app.html
```

```bash
# build the UI bundle, then the server
npm --prefix app ci && npm --prefix app run build
go build ./cmd/whatsapp-mcp

# or the whole thing, reproducibly
docker build -f Dockerfile.mcp -t whatsapp-mcp .
```

The bundle is committed so `go build` needs no Node toolchain; CI rebuilds it and
fails if it drifts from its source.

## Scripts

| Script | Description |
| --- | --- |
| `npm run build` | Compile TypeScript to `build/` |
| `npm run typecheck` | Type‑check without emitting |
| `npm run start` | Run stdio server |
| `npm run start:http` | Run HTTP server |
| `npm run dev` / `dev:stdio` | Watch mode (tsx) |
| `npm run smoke` | End‑to‑end stdio smoke test against a live Evolution API |

## License

MIT © Ari Oliveira
