#!/usr/bin/env node
/**
 * whatsapp-mcp — remote (HTTP) transport.
 *
 * Streamable-HTTP MCP endpoint guarded by an OAuth 2.1 flow (mcpAuthRouter +
 * WhatsAppOAuthProvider). The "login" step is a WhatsApp device-pairing page
 * (login-router.ts): the user scans a QR / types a pairing code, and the paired
 * phone number becomes their identity. Each bearer token then resolves to that
 * user's own Evolution instance (SessionCredentialProvider), so the server is
 * multi-tenant — every user drives their own WhatsApp.
 *
 * Same declarative tool catalog + generic dispatcher as the stdio entrypoint
 * (index.ts); only the credential source and transport differ.
 */
import dotenv from "dotenv";
dotenv.config();

import http from "http";
import { fileURLToPath } from "url";
import express from "express";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
  type CallToolRequest,
  type CallToolResult,
  type Tool,
} from "@modelcontextprotocol/sdk/types.js";
import { mcpAuthRouter } from "@modelcontextprotocol/sdk/server/auth/router.js";
import { requireBearerAuth } from "@modelcontextprotocol/sdk/server/auth/middleware/bearerAuth.js";

import { toolDefinitions, toolMap, HIDDEN_IN_HTTP } from "./tools.js";
import { executeTool } from "./dispatch.js";
import { WhatsAppOAuthProvider } from "./http/provider.js";
import { createLoginRouter } from "./http/login-router.js";
import { SessionCredentialProvider, startSessionCleanupLoop } from "./http/session-provider.js";
import type { EvolutionConfig } from "./http/evolution.js";

const PORT = Number(process.env.PORT ?? 3000);
const PUBLIC_URL = (process.env.PUBLIC_URL ?? `http://localhost:${PORT}`).replace(/\/$/, "");
const JWT_SECRET_STR = process.env.MCP_JWT_SECRET;
const EVO_BASE_URL = (process.env.EVO_BASE_URL ?? "http://127.0.0.1:8080").replace(/\/$/, "");
const EVO_GLOBAL_API_KEY =
  process.env.EVO_GLOBAL_API_KEY ?? process.env.EVO_API_KEY ?? process.env.AUTHENTICATION_API_KEY ?? "";
const INSTANCE_PREFIX = process.env.EVO_INSTANCE_PREFIX ?? "mcp-";

if (!JWT_SECRET_STR || JWT_SECRET_STR.length < 32) {
  console.error("MCP_JWT_SECRET is required and must be at least 32 characters. Aborting.");
  process.exit(1);
}
if (!EVO_GLOBAL_API_KEY) {
  console.error("EVO_GLOBAL_API_KEY (the Evolution AUTHENTICATION_API_KEY) is required. Aborting.");
  process.exit(1);
}
const JWT_SECRET = new TextEncoder().encode(JWT_SECRET_STR);

const issuerUrl = new URL(PUBLIC_URL);
const resourceUrl = new URL("/mcp", PUBLIC_URL);

const evoConfig: EvolutionConfig = { baseUrl: EVO_BASE_URL, globalApiKey: EVO_GLOBAL_API_KEY };

const provider = new WhatsAppOAuthProvider({
  issuerUrl,
  resourceUrl,
  loginPath: "/login",
  jwtSecret: JWT_SECRET,
});

// Remote tool catalog = full catalog minus the instance-admin tools.
const remoteTools = toolDefinitions.filter((d) => !HIDDEN_IN_HTTP.has(d.name));

const app = express();
app.use(express.json({ limit: "8mb" }));
app.use(express.urlencoded({ extended: false }));

app.use((req, _res, next) => {
  const bodyMethod = (req.body as { method?: string } | undefined)?.method;
  const hasAuth = !!req.header("authorization");
  console.log(
    `[http] ${req.method} ${req.path}${bodyMethod ? ` rpc=${bodyMethod}` : ""}${hasAuth ? " auth=yes" : " auth=no"}`,
  );
  next();
});

// Static assets (the login page's logo).
const assetsDir = fileURLToPath(new URL("../assets", import.meta.url));
app.use("/assets", express.static(assetsDir));

app.use(
  mcpAuthRouter({
    provider,
    issuerUrl,
    baseUrl: new URL(PUBLIC_URL),
    resourceServerUrl: resourceUrl,
    scopesSupported: ["mcp:tools"],
    resourceName: "WhatsApp MCP",
  }),
);

app.use(createLoginRouter(provider, evoConfig, INSTANCE_PREFIX));

app.get("/healthz", (_req, res) => {
  res.json({
    ok: true,
    tools: remoteTools.length,
    publicUrl: PUBLIC_URL,
    evolution: EVO_BASE_URL,
    pairedUsers: provider.userTokens.allIds().length,
  });
});

/* ------------------------------ MCP endpoint ------------------------------ */

const resourceMetadataUrl = `${PUBLIC_URL}/.well-known/oauth-protected-resource${resourceUrl.pathname}`;
const bearerGuard = requireBearerAuth({
  verifier: provider,
  requiredScopes: [],
  resourceMetadataUrl,
});

function makeMcpServer(userSub: string): McpServer {
  const credProvider = new SessionCredentialProvider(userSub, provider.userTokens, EVO_BASE_URL);

  const mcp = new McpServer(
    { name: "@aol/whatsapp-mcp", version: "1.0.0" },
    { capabilities: { tools: {} } },
  );
  const low = mcp.server;

  low.setRequestHandler(ListToolsRequestSchema, async () => {
    const tools: Tool[] = remoteTools.map((def) => ({
      name: def.name,
      description: def.description,
      inputSchema: { type: "object" as const, ...def.inputSchema },
    }));
    return { tools };
  });

  low.setRequestHandler(CallToolRequestSchema, async (req: CallToolRequest): Promise<CallToolResult> => {
    const def = toolMap.get(req.params.name);
    if (!def || HIDDEN_IN_HTTP.has(req.params.name)) {
      return { content: [{ type: "text", text: `Unknown tool: ${req.params.name}` }], isError: true };
    }
    const args = (req.params.arguments as Record<string, unknown>) ?? {};
    console.log(`[mcp] tools/call name=${req.params.name} sub=${userSub}`);
    return executeTool(def, args, credProvider);
  });

  return mcp;
}

app.all("/mcp", bearerGuard, async (req, res) => {
  const userSub = req.auth?.extra?.userSub as string | undefined;
  if (!userSub) {
    res.status(401).json({ error: "invalid_token", error_description: "missing sub" });
    return;
  }
  const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
  res.on("close", () => {
    transport.close().catch(() => {});
  });
  const server = makeMcpServer(userSub);
  await server.connect(transport);
  await transport.handleRequest(req, res, req.body);
});

startSessionCleanupLoop(provider.userTokens, 60_000);

const httpServer = http.createServer(app);
httpServer.listen(PORT, () => {
  console.log(`WhatsApp MCP remote listening on :${PORT}`);
  console.log(`  Public URL:      ${PUBLIC_URL}`);
  console.log(`  Resource (MCP):  ${resourceUrl.href}`);
  console.log(`  Authorization:   ${issuerUrl.href}`);
  console.log(`  Login (pairing): ${PUBLIC_URL}/login`);
  console.log(`  Evolution API:   ${EVO_BASE_URL}`);
  console.log(`  Tools (remote):  ${remoteTools.length}`);
});

function shutdown(signal: string) {
  console.log(`[whatsapp-mcp] ${signal} — shutting down…`);
  httpServer.close(() => process.exit(0));
  setTimeout(() => process.exit(0), 3000).unref();
}
process.on("SIGTERM", () => shutdown("SIGTERM"));
process.on("SIGINT", () => shutdown("SIGINT"));
