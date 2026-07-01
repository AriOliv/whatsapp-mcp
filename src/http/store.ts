/**
 * In-memory stores backing the HTTP (remote) transport's OAuth 2.1 flow and the
 * per-user WhatsApp credentials captured during pairing.
 *
 * Ported from the uber-mcp / ifood-mcp servers, trimmed to what the WhatsApp
 * pairing flow needs (no server-side browser sessions). The OAuth pieces
 * (clients / pending flows / auth codes / refresh tokens) are generic; the
 * WhatsApp-specific part is `WhatsAppCredentials` + `UserTokenStore`.
 */
import { randomBytes } from "crypto";
import type { OAuthClientInformationFull } from "@modelcontextprotocol/sdk/shared/auth.js";
import type { OAuthRegisteredClientsStore } from "@modelcontextprotocol/sdk/server/auth/clients.js";

function randomId(bytes = 16): string {
  return randomBytes(bytes).toString("hex");
}

/* ------------------------------- OAuth clients ---------------------------- */

export class InMemoryClientsStore implements OAuthRegisteredClientsStore {
  private clients = new Map<string, OAuthClientInformationFull>();

  getClient(clientId: string): OAuthClientInformationFull | undefined {
    return this.clients.get(clientId);
  }

  async registerClient(
    client: Omit<OAuthClientInformationFull, "client_id" | "client_id_issued_at">,
  ): Promise<OAuthClientInformationFull> {
    const full: OAuthClientInformationFull = {
      ...client,
      client_id: `wa_${randomId(12)}`,
      client_id_issued_at: Math.floor(Date.now() / 1000),
    };
    this.clients.set(full.client_id, full);
    return full;
  }
}

/* ----------------------------- Pending auth flow -------------------------- */

/**
 * A login/pairing flow in progress. Beyond the standard OAuth fields, it carries
 * the transient Evolution instance created for this flow (`instanceName` +
 * `instanceApiKey`) and, once known, the paired `number`.
 */
export type PendingAuthFlow = {
  flowId: string;
  clientId: string;
  redirectUri: string;
  state?: string;
  scopes: string[];
  codeChallenge: string;
  resource?: string;
  createdAt: number;
  expiresAt: number;
  /** Evolution instance provisioned for this pairing (name in the URL path). */
  instanceName?: string;
  /** That instance's own apikey (the `hash` returned by /instance/create). */
  instanceApiKey?: string;
  /** Phone the user asked to pair via pairing-code (E.164 digits, no +). */
  number?: string;
};

export class PendingAuthStore {
  private flows = new Map<string, PendingAuthFlow>();

  create(
    partial: Omit<PendingAuthFlow, "flowId" | "createdAt" | "expiresAt">,
    ttlSec = 600,
  ): PendingAuthFlow {
    const now = Math.floor(Date.now() / 1000);
    const flow: PendingAuthFlow = {
      ...partial,
      flowId: randomId(16),
      createdAt: now,
      expiresAt: now + ttlSec,
    };
    this.flows.set(flow.flowId, flow);
    return flow;
  }

  get(flowId: string): PendingAuthFlow | undefined {
    const f = this.flows.get(flowId);
    if (!f) return undefined;
    if (f.expiresAt < Math.floor(Date.now() / 1000)) {
      this.flows.delete(flowId);
      return undefined;
    }
    return f;
  }

  update(flowId: string, patch: Partial<PendingAuthFlow>): void {
    const f = this.flows.get(flowId);
    if (!f) return;
    this.flows.set(flowId, { ...f, ...patch });
  }

  delete(flowId: string): void {
    this.flows.delete(flowId);
  }

  allIds(): string[] {
    return Array.from(this.flows.keys());
  }
}

/* -------------------------------- Auth codes ------------------------------ */

export type AuthCode = {
  code: string;
  clientId: string;
  redirectUri: string;
  codeChallenge: string;
  scopes: string[];
  resource?: string;
  userSub: string;
  expiresAt: number;
};

export class AuthCodeStore {
  private codes = new Map<string, AuthCode>();

  create(partial: Omit<AuthCode, "code" | "expiresAt">, ttlSec = 60): AuthCode {
    const ac: AuthCode = {
      ...partial,
      code: randomId(24),
      expiresAt: Math.floor(Date.now() / 1000) + ttlSec,
    };
    this.codes.set(ac.code, ac);
    return ac;
  }

  peek(code: string): AuthCode | undefined {
    const ac = this.codes.get(code);
    if (!ac) return undefined;
    if (ac.expiresAt < Math.floor(Date.now() / 1000)) {
      this.codes.delete(code);
      return undefined;
    }
    return ac;
  }

  take(code: string): AuthCode | undefined {
    const ac = this.peek(code);
    if (!ac) return undefined;
    this.codes.delete(code);
    return ac;
  }
}

/* ------------------------------ Refresh tokens ---------------------------- */

export type RefreshRecord = {
  refreshToken: string;
  clientId: string;
  userSub: string;
  resource?: string;
  scopes: string[];
  expiresAt: number;
};

export class RefreshTokenStore {
  private tokens = new Map<string, RefreshRecord>();

  create(
    partial: Omit<RefreshRecord, "refreshToken" | "expiresAt">,
    ttlSec = 60 * 60 * 24 * 30,
  ): RefreshRecord {
    const r: RefreshRecord = {
      ...partial,
      refreshToken: randomId(32),
      expiresAt: Math.floor(Date.now() / 1000) + ttlSec,
    };
    this.tokens.set(r.refreshToken, r);
    return r;
  }

  take(token: string): RefreshRecord | undefined {
    const r = this.tokens.get(token);
    if (!r) return undefined;
    this.tokens.delete(token);
    if (r.expiresAt < Math.floor(Date.now() / 1000)) return undefined;
    return r;
  }
}

/* -------------------- WhatsApp per-user credential store ------------------ */

/**
 * The downstream credential captured when a user finishes pairing: the Evolution
 * instance bound to their number plus that instance's apikey. Every MCP call for
 * this user targets `instanceName` with `apiKey` (see SessionCredentialProvider).
 */
export type WhatsAppCredentials = {
  /** Stable identity for the user = their paired WhatsApp number (digits only). */
  userSub: string;
  /** Evolution instance name (fills the `{instance}` path segment). */
  instanceName: string;
  /** The instance's own apikey (`hash` from /instance/create). */
  apiKey: string;
  /** Full owner JID, e.g. 5521999999999@s.whatsapp.net. */
  ownerJid?: string;
  capturedAt: number;
};

export class UserTokenStore {
  private byId = new Map<string, WhatsAppCredentials>();

  set(userSub: string, creds: WhatsAppCredentials): void {
    this.byId.set(userSub, creds);
  }

  get(userSub: string): WhatsAppCredentials | undefined {
    return this.byId.get(userSub);
  }

  has(userSub: string): boolean {
    return this.byId.has(userSub);
  }

  delete(userSub: string): void {
    this.byId.delete(userSub);
  }

  allIds(): string[] {
    return Array.from(this.byId.keys());
  }
}
