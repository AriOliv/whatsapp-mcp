/**
 * Generic HTTP dispatcher: turns any McpToolDefinition + args into an Evolution
 * API call and returns an MCP CallToolResult. Ported from the evo-go MCP /
 * avenia-internal-mcp executeTool, adapted for Evolution API: a single `apikey`
 * header and an auto-filled `{instance}` path segment.
 */
import axios, { type AxiosError } from "axios";
import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";

import type { CredentialProvider, McpToolDefinition } from "./types.js";

function buildPath(template: string, args: Record<string, unknown>): string {
  return template.replace(/\{([^}]+)\}/g, (full, key) => {
    const val = args[key];
    return val !== undefined && val !== null ? encodeURIComponent(String(val)) : full;
  });
}

function buildQuery(def: McpToolDefinition, args: Record<string, unknown>): string {
  const params = new URLSearchParams();
  for (const key of def.queryParams || []) {
    const val = args[key];
    if (val === undefined || val === null || val === "") continue;
    const backendKey = def.queryParamMapping?.[key] ?? key;
    if (Array.isArray(val)) {
      for (const item of val) params.append(backendKey, String(item));
    } else {
      params.append(backendKey, String(val));
    }
  }
  return params.toString();
}

function buildHeaders(def: McpToolDefinition, args: Record<string, unknown>): Record<string, string> {
  const headers: Record<string, string> = {};
  for (const key of def.headerParams || []) {
    const val = args[key];
    if (val !== undefined && val !== null && val !== "") {
      headers[def.headerParamMapping?.[key] ?? key] = String(val);
    }
  }
  return headers;
}

function buildBody(def: McpToolDefinition, args: Record<string, unknown>): Record<string, unknown> | undefined {
  if (!def.bodyParams || def.bodyParams.length === 0) return undefined;
  const body: Record<string, unknown> = {};
  for (const key of def.bodyParams) {
    if (args[key] !== undefined) body[key] = args[key];
  }
  return Object.keys(body).length > 0 ? body : undefined;
}

function textResult(text: string, isError = false): CallToolResult {
  return { content: [{ type: "text", text }], isError };
}

export async function executeTool(
  def: McpToolDefinition,
  args: Record<string, unknown>,
  provider: CredentialProvider,
): Promise<CallToolResult> {
  // Auto-fill the {instance} segment from the active instance name when the tool
  // is instance-scoped and the caller didn't pass an explicit override.
  if (def.usesInstance && (args.instance === undefined || args.instance === null || args.instance === "")) {
    const name = provider.getInstanceName();
    if (!name) {
      return textResult(
        "No active instance set. Create one (evo_instance_create) or select one with evo_use_instance, " +
          "or set EVO_INSTANCE in the MCP env. You can also pass an explicit `instance` argument.",
        true,
      );
    }
    args = { ...args, instance: name };
  }

  const baseUrl = provider.getBaseUrl();
  const query = buildQuery(def, args);
  const url = `${baseUrl}${buildPath(def.pathTemplate, args)}${query ? `?${query}` : ""}`;
  const method = def.method.toLowerCase();

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...buildHeaders(def, args),
  };

  if (def.requiresAuth) {
    const { key, error } = await provider.getApiKey();
    if (!key) return textResult(error!, true);
    headers["apikey"] = key;
  }

  const config: {
    method: string;
    url: string;
    headers: Record<string, string>;
    data?: unknown;
    timeout?: number;
  } = { method, url, headers };

  if (def.timeout) config.timeout = def.timeout;
  if (["post", "put", "patch", "delete"].includes(method)) {
    if (def.bodyUnwrap && args[def.bodyUnwrap] !== undefined) {
      config.data = args[def.bodyUnwrap];
    } else {
      const body = buildBody(def, args);
      if (body) config.data = body;
    }
  }

  try {
    const res = await axios(config);

    // Capture the name of a freshly created instance so it's immediately active
    // (mirrors avenia persisting tokens after a successful login).
    if (def.name === "evo_instance_create") {
      const name = res.data?.instance?.instanceName || res.data?.instance?.name;
      if (typeof name === "string" && name) await provider.setInstanceName(name);
    }

    return textResult(JSON.stringify(res.data, null, 2));
  } catch (err) {
    const axErr = err as AxiosError<{ message?: unknown; error?: unknown; response?: unknown }>;
    const status = axErr.response?.status;
    const data = axErr.response?.data as { message?: unknown; error?: unknown } | undefined;
    const raw = data?.message ?? data?.error ?? axErr.message ?? "Unknown error";
    const msg = typeof raw === "string" ? raw : JSON.stringify(raw);
    if (status === 401) {
      return textResult(
        `API Error (401 not authorized): ${msg}. Check EVO_API_KEY matches the server's AUTHENTICATION_API_KEY.`,
        true,
      );
    }
    if (status === 404) {
      return textResult(
        `API Error (404): ${msg}. The instance may not exist or isn't connected — check evo_instance_list / the active instance name.`,
        true,
      );
    }
    return textResult(`API Error (${status ?? "network"}): ${msg}`, true);
  }
}
