/**
 * SessionCredentialProvider — the HTTP counterpart of the stdio
 * EnvFileCredentialProvider. It implements the same `CredentialProvider`
 * interface the dispatcher consumes, but resolves the Evolution instance +
 * apikey from the per-user `UserTokenStore` (keyed by the JWT `sub`) instead of
 * env/file. This is what makes the remote server multi-tenant: each bearer token
 * maps to that user's own paired WhatsApp instance.
 */
import type { CredentialProvider } from "../types.js";
import type { UserTokenStore } from "./store.js";

export class SessionCredentialProvider implements CredentialProvider {
  constructor(
    private readonly userSub: string,
    private readonly store: UserTokenStore,
    private readonly baseUrl: string,
  ) {}

  getBaseUrl(): string {
    return this.baseUrl;
  }

  async getApiKey(): Promise<{ key: string | null; error?: string }> {
    const creds = this.store.get(this.userSub);
    if (!creds) {
      return {
        key: null,
        error:
          "No paired WhatsApp session found for this token. Re-authenticate via the login page to pair your number.",
      };
    }
    return { key: creds.apiKey };
  }

  getInstanceName(): string | null {
    return this.store.get(this.userSub)?.instanceName ?? null;
  }

  async setInstanceName(_name: string): Promise<void> {
    // No-op for HTTP sessions: the instance is bound at pairing time and never
    // switched per request. (The interface requires the method.)
  }
}

/**
 * Periodically prunes nothing by default — WhatsApp instances don't carry a hard
 * expiry the way cookie sessions do — but the hook is kept for symmetry with the
 * other servers and to allow future TTL-based eviction.
 */
export function startSessionCleanupLoop(
  _store: UserTokenStore,
  intervalMs = 60_000,
): () => void {
  const timer = setInterval(() => {
    // Intentionally empty: pairing credentials persist until the user re-pairs
    // or the process restarts (in-memory store).
  }, intervalMs);
  timer.unref?.();
  return () => clearInterval(timer);
}
