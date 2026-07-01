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
