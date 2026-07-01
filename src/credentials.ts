/**
 * File+env credential provider for the stdio transport (single user/dev).
 *
 * Inspired by avenia-internal-mcp's FileTokenProvider: env vars seed the
 * credentials, and the active instance name is persisted to ~/.evoapi-mcp.json so
 * it survives restarts.
 */
import { readFileSync, writeFileSync } from "fs";
import { homedir } from "os";
import { join } from "path";

import type { CredentialProvider } from "./types.js";

const STORE_FILE = join(homedir(), ".evoapi-mcp.json");

interface Store {
  instanceName?: string;
}

function loadStore(): Store {
  try {
    return JSON.parse(readFileSync(STORE_FILE, "utf-8")) as Store;
  } catch {
    return {};
  }
}

function saveStore(store: Store): void {
  try {
    writeFileSync(STORE_FILE, JSON.stringify(store, null, 2), "utf-8");
  } catch {
    // best-effort persistence
  }
}

export class EnvFileCredentialProvider implements CredentialProvider {
  private baseUrl: string;
  private apiKey: string;
  /** In-memory override; falls back to env then the persisted file. */
  private instanceName: string;

  constructor() {
    // Default to 127.0.0.1 (not "localhost") — Node may resolve localhost to IPv6
    // ::1 while the API listens on IPv4, which surfaces as a confusing network error.
    this.baseUrl = (process.env.EVO_BASE_URL || "http://127.0.0.1:8080").replace(/\/+$/, "");
    this.apiKey = process.env.EVO_API_KEY || process.env.AUTHENTICATION_API_KEY || "";
    this.instanceName = process.env.EVO_INSTANCE || loadStore().instanceName || "";
  }

  getBaseUrl(): string {
    return this.baseUrl;
  }

  async getApiKey(): Promise<{ key: string | null; error?: string }> {
    if (!this.apiKey) {
      return {
        key: null,
        error:
          "No API key configured. Set EVO_API_KEY (the server's AUTHENTICATION_API_KEY) in the MCP env.",
      };
    }
    return { key: this.apiKey };
  }

  getInstanceName(): string | null {
    return this.instanceName || null;
  }

  async setInstanceName(name: string): Promise<void> {
    this.instanceName = name;
    saveStore({ instanceName: name });
  }
}
