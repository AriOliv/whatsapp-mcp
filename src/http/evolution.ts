/**
 * Thin Evolution API client used by the pairing flow (login-router.ts).
 *
 * These calls use the server's GLOBAL apikey to provision and inspect the
 * per-user instance while the browser is on the login page. Once the instance
 * is `open`, its own apikey (`hash`) is what the MCP session uses thereafter.
 */
import axios from "axios";

export type EvolutionConfig = {
  baseUrl: string;
  globalApiKey: string;
};

export type QrPayload = {
  /** data:image/png;base64,… of the QR (may be empty once connected). */
  base64?: string;
  /** Raw WhatsApp linking code encoded in the QR. */
  code?: string;
  /** 8-char pairing code (present only when a `number` was supplied). */
  pairingCode?: string;
  count?: number;
};

export type CreateResult = {
  instanceName: string;
  /** The instance's own apikey. */
  apiKey: string;
  qr: QrPayload;
};

export type ConnState = "open" | "connecting" | "close" | "unknown";

function client(cfg: EvolutionConfig) {
  return axios.create({
    baseURL: cfg.baseUrl,
    headers: { apikey: cfg.globalApiKey, "Content-Type": "application/json" },
    timeout: 15_000,
  });
}

/** Create a fresh Baileys instance and return its apikey + first QR. */
export async function createInstance(
  cfg: EvolutionConfig,
  instanceName: string,
  number?: string,
): Promise<CreateResult> {
  const body: Record<string, unknown> = {
    instanceName,
    integration: "WHATSAPP-BAILEYS",
    qrcode: true,
  };
  if (number) body.number = number;

  const { data } = await client(cfg).post("/instance/create", body);
  const qr = (data?.qrcode ?? {}) as QrPayload;
  const apiKey = String(data?.hash ?? "");
  return { instanceName, apiKey, qr };
}

/** Re-fetch a rotating QR (and pairing code if `number` is given). */
export async function refreshQr(
  cfg: EvolutionConfig,
  instanceName: string,
  number?: string,
): Promise<QrPayload> {
  const url = `/instance/connect/${encodeURIComponent(instanceName)}`;
  const { data } = await client(cfg).get(url, {
    params: number ? { number } : undefined,
  });
  return (data ?? {}) as QrPayload;
}

/** Current connection state of the instance. */
export async function getState(
  cfg: EvolutionConfig,
  instanceName: string,
): Promise<ConnState> {
  try {
    const { data } = await client(cfg).get(
      `/instance/connectionState/${encodeURIComponent(instanceName)}`,
    );
    const s = data?.instance?.state as string | undefined;
    if (s === "open" || s === "connecting" || s === "close") return s;
    return "unknown";
  } catch {
    return "unknown";
  }
}

/** After `open`, resolve the paired number / owner JID. */
export async function fetchOwner(
  cfg: EvolutionConfig,
  instanceName: string,
): Promise<{ number: string; ownerJid?: string } | null> {
  const { data } = await client(cfg).get("/instance/fetchInstances", {
    params: { instanceName },
  });
  const list = Array.isArray(data) ? data : data?.instances ?? [];
  const inst = list[0];
  if (!inst) return null;
  const rawJid: string | undefined = inst.ownerJid ?? inst.owner ?? inst.number;
  const rawNumber: string | undefined = inst.number ?? rawJid;
  if (!rawNumber) return null;
  const number = String(rawNumber).replace(/@.*/, "").replace(/\D/g, "");
  const ownerJid = rawJid && String(rawJid).includes("@") ? String(rawJid) : undefined;
  return { number, ownerJid };
}

/** Best-effort teardown of a transient/duplicate instance. */
export async function deleteInstance(
  cfg: EvolutionConfig,
  instanceName: string,
): Promise<void> {
  const c = client(cfg);
  try {
    await c.delete(`/instance/logout/${encodeURIComponent(instanceName)}`);
  } catch {
    /* ignore */
  }
  try {
    await c.delete(`/instance/delete/${encodeURIComponent(instanceName)}`);
  } catch {
    /* ignore */
  }
}
