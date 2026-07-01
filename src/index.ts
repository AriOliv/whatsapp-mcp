#!/usr/bin/env node
/**
 * evolution-api-mcp — MCP server wrapping the Evolution API (Node/Baileys).
 *
 * Connection design inspired by avenia-internal-mcp: a declarative tool array
 * (tools.ts) + a generic HTTP dispatcher (dispatch.ts) + a transport-agnostic
 * credential provider (credentials.ts). This entrypoint wires those into an
 * McpServer over stdio.
 */
import dotenv from "dotenv";
dotenv.config();

import { pathToFileURL } from "url";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  ListToolsRequestSchema,
  CallToolRequestSchema,
  type Tool,
  type CallToolRequest,
  type CallToolResult,
} from "@modelcontextprotocol/sdk/types.js";

import { toolDefinitions, toolMap } from "./tools.js";
import { executeTool } from "./dispatch.js";
import { EnvFileCredentialProvider } from "./credentials.js";

const provider = new EnvFileCredentialProvider();

/**
 * Meta tool (no HTTP call): sets the active instance NAME used to fill the
 * `{instance}` path segment of every instance-scoped tool, and persists it.
 */
const USE_INSTANCE_TOOL: Tool = {
  name: "evo_use_instance",
  description:
    "Set the active instance NAME used by all instance-scoped tools (send, find, group, …). Persisted to ~/.evoapi-mcp.json. List names with evo_instance_list.",
  inputSchema: {
    type: "object",
    properties: { instance: { type: "string", description: "The instance name" } },
    required: ["instance"],
  },
};

const mcpServer = new McpServer(
  { name: "@aol/whatsapp-mcp", version: "1.0.0" },
  { capabilities: { tools: {} } },
);
const server = mcpServer.server;

server.setRequestHandler(ListToolsRequestSchema, async () => {
  const tools: Tool[] = [
    USE_INSTANCE_TOOL,
    ...toolDefinitions.map((def) => ({
      name: def.name,
      description: def.description,
      inputSchema: { type: "object" as const, ...def.inputSchema },
    })),
  ];
  return { tools };
});

server.setRequestHandler(CallToolRequestSchema, async (request: CallToolRequest): Promise<CallToolResult> => {
  const { name, arguments: rawArgs } = request.params;
  const args = (rawArgs as Record<string, unknown>) || {};

  if (name === USE_INSTANCE_TOOL.name) {
    const instance = args.instance;
    if (typeof instance !== "string" || !instance) {
      return { content: [{ type: "text", text: "Provide a non-empty 'instance' name." }], isError: true };
    }
    await provider.setInstanceName(instance);
    return { content: [{ type: "text", text: `Active instance set to "${instance}".` }] };
  }

  const def = toolMap.get(name);
  if (!def) {
    return { content: [{ type: "text", text: `Unknown tool: ${name}` }], isError: true };
  }
  return executeTool(def, args, provider);
});

export async function main(): Promise<void> {
  const transport = new StdioServerTransport();
  await mcpServer.connect(transport);
}

const isEntrypoint = (() => {
  try {
    const entry = process.argv[1];
    return !!entry && import.meta.url === pathToFileURL(entry).href;
  } catch {
    return false;
  }
})();

if (isEntrypoint) {
  main().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
