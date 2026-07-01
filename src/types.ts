/**
 * Declarative tool + credential abstractions for the Evolution API MCP.
 *
 * Mirrors the avenia-internal-mcp design: each tool is a plain data object that
 * describes its MCP schema *and* how to turn arguments into an HTTP request. A
 * single generic dispatcher (see dispatch.ts) executes any tool from its
 * definition, so adding endpoints means appending to an array — no per-tool code.
 *
 * Evolution API specifics (vs the older evolution-go):
 *  - The instance is identified by its **name in the URL path** (`/.../{instance}`),
 *    not by which key authenticates. So `{instance}` segments are auto-filled from
 *    the active instance name (CredentialProvider.getInstanceName) when not passed.
 *  - A single `apikey` header authenticates everything — the server's global
 *    AUTHENTICATION_API_KEY (or an instance's own key) both work.
 */

export interface McpToolDefinition {
  name: string;
  description: string;
  /** JSON Schema object body (without the outer `type: "object"`, added at list time). */
  inputSchema: Record<string, unknown>;
  method: "get" | "post" | "put" | "patch" | "delete";
  /** e.g. "/message/sendText/{instance}". `{x}` segments are filled from args. */
  pathTemplate: string;
  /**
   * If true, a `{instance}` segment is auto-filled from the active instance name
   * (an explicit `instance` arg still overrides it).
   */
  usesInstance?: boolean;
  /** Args appended as query string. */
  queryParams?: string[];
  /** Maps MCP arg name -> backend query key when they differ. */
  queryParamMapping?: Record<string, string>;
  /** Args sent as JSON body fields (nested objects/arrays are passed through as-is). */
  bodyParams?: string[];
  /** If set, send args[bodyUnwrap] as the raw body (overrides bodyParams). */
  bodyUnwrap?: string;
  /** Args forwarded as HTTP headers. */
  headerParams?: string[];
  headerParamMapping?: Record<string, string>;
  /** Whether the request needs an `apikey` header. */
  requiresAuth: boolean;
  /** Custom timeout in ms. */
  timeout?: number;
}

/**
 * Sources/persists Evolution API credentials, decoupled from the transport — the
 * stdio provider reads env + a file; an HTTP provider (future) could read
 * per-request headers. Analogous to avenia's TokenProvider.
 */
export interface CredentialProvider {
  getBaseUrl(): string;
  /** Returns the apikey, or an instructive error if missing. */
  getApiKey(): Promise<{ key: string | null; error?: string }>;
  /** The active instance name used to fill `{instance}` path segments. */
  getInstanceName(): string | null;
  /** Persists the active instance name (e.g. after creating an instance). */
  setInstanceName(name: string): Promise<void>;
}
