/**
 * /login — WhatsApp pairing flow, the analog of uber-mcp's browser login and
 * ifood-mcp's OTP login. Instead of a password, the user links a device:
 *
 *   1. GET  /login            — provisions a per-flow Evolution instance and
 *                               renders the QR code (+ "use a code" option).
 *   2. GET  /login/qr         — JSON, returns a fresh (rotating) QR image.
 *   3. POST /login/pairing    — JSON, swaps to an 8-digit pairing code for a
 *                               number the user types (QR alternative).
 *   4. GET  /login/status     — JSON poller. Once the instance is `open`, it
 *                               captures the paired number as the user identity,
 *                               mints an OAuth auth code, and returns the
 *                               redirect back to the waiting MCP client.
 */
import { Router } from "express";
import type { WhatsAppOAuthProvider } from "./provider.js";
import type { EvolutionConfig, QrPayload } from "./evolution.js";
import {
  createInstance,
  deleteInstance,
  fetchOwner,
  getState,
  refreshQr,
} from "./evolution.js";

export function createLoginRouter(
  provider: WhatsAppOAuthProvider,
  evo: EvolutionConfig,
  instancePrefix: string,
): Router {
  const router = Router();

  /** Ensure the flow has a provisioned instance; returns the first QR payload. */
  async function ensureInstance(flowId: string) {
    const flow = provider.pending.get(flowId);
    if (!flow) return null;
    if (flow.instanceName && flow.instanceApiKey) {
      // Already provisioned — just fetch a current QR.
      const qr = await refreshQr(evo, flow.instanceName).catch(() => ({}) as QrPayload);
      return { flow, qr };
    }
    const instanceName = `${instancePrefix}${flowId.slice(0, 12)}`;
    const created = await createInstance(evo, instanceName);
    provider.pending.update(flowId, {
      instanceName,
      instanceApiKey: created.apiKey,
    });
    return { flow: provider.pending.get(flowId)!, qr: created.qr };
  }

  /**
   * Finalize pairing: capture the number as identity, store credentials, mint an
   * OAuth auth code, and return the client redirect URL. Returns null if the
   * instance is not actually connected yet.
   */
  async function complete(flowId: string): Promise<string | null> {
    const flow = provider.pending.get(flowId);
    if (!flow || !flow.instanceName || !flow.instanceApiKey) return null;

    const owner = await fetchOwner(evo, flow.instanceName);
    if (!owner) return null;

    const sub = owner.number;

    // If this number was already paired to a different (older) instance, tear
    // the stale one down so we don't leave a duplicate linked device behind.
    const prev = provider.userTokens.get(sub);
    if (prev && prev.instanceName !== flow.instanceName) {
      await deleteInstance(evo, prev.instanceName);
    }

    provider.userTokens.set(sub, {
      userSub: sub,
      instanceName: flow.instanceName,
      apiKey: flow.instanceApiKey,
      ownerJid: owner.ownerJid,
      capturedAt: Math.floor(Date.now() / 1000),
    });

    const ac = provider.codes.create({
      clientId: flow.clientId,
      redirectUri: flow.redirectUri,
      codeChallenge: flow.codeChallenge,
      scopes: flow.scopes,
      resource: flow.resource,
      userSub: sub,
    });
    provider.pending.delete(flowId);

    const redirect = new URL(flow.redirectUri);
    redirect.searchParams.set("code", ac.code);
    if (flow.state) redirect.searchParams.set("state", flow.state);
    return redirect.toString();
  }

  /* ------------------------------- step 1: page ------------------------------ */

  router.get("/login", async (req, res) => {
    const flowId = typeof req.query.authFlow === "string" ? req.query.authFlow : "";
    const flow = flowId ? provider.pending.get(flowId) : undefined;
    if (!flow) {
      res
        .status(400)
        .type("html")
        .send(page(`<h1>Link expirado</h1><p class="sub">Reinicie o login pelo seu cliente MCP.</p>`));
      return;
    }

    let qrDataUrl = "";
    try {
      const r = await ensureInstance(flowId);
      qrDataUrl = r?.qr.base64 ?? "";
    } catch (err) {
      const msg = err instanceof Error ? err.message : "erro ao provisionar instância";
      res
        .status(502)
        .type("html")
        .send(page(`<h1>Falha ao iniciar</h1><p class="sub">${escapeHtml(msg)}</p>`));
      return;
    }

    res.type("html").send(
      page(
        `
        <div class="brand"><img src="/assets/logo.svg" width="44" height="44" alt=""/></div>
        <h1>Conectar o WhatsApp</h1>
        <p class="sub">Abra o WhatsApp no celular → <strong>Aparelhos conectados</strong> →
        <strong>Conectar um aparelho</strong> e aponte para o QR abaixo.</p>

        <div class="qr" id="qrBox">
          ${qrDataUrl ? `<img id="qrImg" src="${escapeHtml(qrDataUrl)}" width="264" height="264" alt="QR code"/>` : `<div class="qr-empty">Gerando QR…</div>`}
        </div>

        <div class="status" id="status"><span class="dot"></span><span id="statusText">Aguardando leitura…</span></div>

        <details class="alt">
          <summary>Prefere um código de 8 dígitos?</summary>
          <p class="sub">Informe seu número com DDI/DDD (só dígitos, ex.: <code>5521999999999</code>).</p>
          <div class="row">
            <input id="num" class="field" inputmode="numeric" placeholder="5521999999999"/>
            <button id="pairBtn" class="btn-sm" type="button">Gerar código</button>
          </div>
          <div id="pairOut" class="pair"></div>
          <p class="hint">No WhatsApp: Aparelhos conectados → Conectar um aparelho → <strong>Conectar com número de telefone</strong>.</p>
        </details>

        <p class="footer">Seu QR/código vai direto ao WhatsApp. O servidor só guarda a instância pareada ao seu número.</p>

        <script>
          const flow = ${JSON.stringify(flowId)};
          const statusText = document.getElementById('statusText');
          const dot = document.querySelector('.dot');
          let done = false;

          async function poll() {
            if (done) return;
            try {
              const r = await fetch('/login/status?authFlow=' + encodeURIComponent(flow), { cache: 'no-store' });
              const j = await r.json();
              if (j.state === 'open' && j.redirect) {
                done = true;
                statusText.textContent = 'Conectado! Redirecionando…';
                dot.classList.add('ok');
                window.location = j.redirect;
                return;
              }
              statusText.textContent = j.state === 'connecting' ? 'Aguardando leitura…' : 'Reconectando…';
            } catch (e) { /* transient */ }
            setTimeout(poll, 2500);
          }

          async function refreshQr() {
            if (done) return;
            try {
              const r = await fetch('/login/qr?authFlow=' + encodeURIComponent(flow), { cache: 'no-store' });
              const j = await r.json();
              const box = document.getElementById('qrBox');
              if (j.base64) box.innerHTML = '<img id="qrImg" src="' + j.base64 + '" width="264" height="264" alt="QR code"/>';
            } catch (e) { /* transient */ }
            setTimeout(refreshQr, 20000);
          }

          document.getElementById('pairBtn').addEventListener('click', async () => {
            const num = (document.getElementById('num').value || '').replace(/\\D/g, '');
            const out = document.getElementById('pairOut');
            if (num.length < 10) { out.textContent = 'Número inválido.'; return; }
            out.textContent = 'Gerando…';
            try {
              const r = await fetch('/login/pairing?authFlow=' + encodeURIComponent(flow), {
                method: 'POST', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ number: num }),
              });
              const j = await r.json();
              out.innerHTML = j.pairingCode
                ? 'Código: <span class="code">' + j.pairingCode + '</span>'
                : (j.error || 'Sem código — use o QR.');
            } catch (e) { out.textContent = 'Falha ao gerar código.'; }
          });

          setTimeout(poll, 1500);
          setTimeout(refreshQr, 20000);
        </script>
      `,
      ),
    );
  });

  /* ------------------------------ step 2: fresh QR --------------------------- */

  router.get("/login/qr", async (req, res) => {
    const flowId = typeof req.query.authFlow === "string" ? req.query.authFlow : "";
    const flow = flowId ? provider.pending.get(flowId) : undefined;
    if (!flow || !flow.instanceName) {
      res.json({ error: "flow expired" });
      return;
    }
    try {
      const qr = await refreshQr(evo, flow.instanceName);
      res.json({ base64: qr.base64 ?? "", pairingCode: qr.pairingCode ?? null });
    } catch {
      res.json({ error: "qr refresh failed" });
    }
  });

  /* --------------------------- step 3: pairing code ------------------------- */

  router.post("/login/pairing", async (req, res) => {
    const flowId = typeof req.query.authFlow === "string" ? req.query.authFlow : "";
    const flow = flowId ? provider.pending.get(flowId) : undefined;
    if (!flow) {
      res.json({ error: "flow expired" });
      return;
    }
    const number = String((req.body as { number?: unknown })?.number ?? "").replace(/\D/g, "");
    if (number.length < 10) {
      res.json({ error: "número inválido" });
      return;
    }
    // Evolution only emits a pairing code when the instance is CREATED with the
    // number — it can't be retrofitted onto the QR-only instance. So swap to a
    // dedicated "-p" instance provisioned with the number.
    const oldName = flow.instanceName;
    const newName = `${instancePrefix}${flowId.slice(0, 12)}p`;
    try {
      await deleteInstance(evo, newName); // clear any leftover from a prior click
      const created = await createInstance(evo, newName, number);
      provider.pending.update(flowId, {
        instanceName: newName,
        instanceApiKey: created.apiKey,
        number,
      });
      if (oldName && oldName !== newName) await deleteInstance(evo, oldName);
      res.json({ pairingCode: created.qr.pairingCode ?? null });
    } catch {
      res.json({ error: "falha ao gerar código" });
    }
  });

  /* ------------------------------ step 4: status ---------------------------- */

  router.get("/login/status", async (req, res) => {
    const flowId = typeof req.query.authFlow === "string" ? req.query.authFlow : "";
    const flow = flowId ? provider.pending.get(flowId) : undefined;
    if (!flow || !flow.instanceName) {
      res.json({ state: "expired" });
      return;
    }
    const state = await getState(evo, flow.instanceName);
    if (state === "open") {
      const redirect = await complete(flowId);
      if (redirect) {
        res.json({ state: "open", redirect });
        return;
      }
      // connected but owner not resolvable yet — report as connecting
      res.json({ state: "connecting" });
      return;
    }
    res.json({ state });
  });

  return router;
}

/* --------------------------------- HTML shell ----------------------------- */

const STYLE = `
  :root {
    --bg:#f4f6f5; --card:#fff; --ink:#0b141a; --sub:#5b6b73; --line:#e6eae9;
    --field:#f7faf9; --brand:#25D366; --brand-ink:#0b3d24; --ok:#16a34a;
    --shadow:0 2px 10px rgba(0,0,0,.06),0 18px 50px rgba(0,0,0,.06); --r:16px;
  }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#0b141a; --card:#141d24; --ink:#e9edef; --sub:#8696a0; --line:#2a353d;
            --field:#101a20; --shadow:0 2px 10px rgba(0,0,0,.5),0 18px 50px rgba(0,0,0,.4); }
  }
  * { box-sizing:border-box; }
  html,body { margin:0; height:100%; }
  body { background:var(--bg); color:var(--ink);
    font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",sans-serif;
    display:grid; place-items:center; padding:32px 18px; }
  .card { width:100%; max-width:420px; background:var(--card); border:1px solid var(--line);
    border-radius:var(--r); padding:32px 28px; box-shadow:var(--shadow); text-align:center; }
  .brand { display:flex; justify-content:center; margin-bottom:14px; }
  h1 { margin:0 0 8px; font-size:21px; font-weight:700; letter-spacing:-.01em; }
  .sub { margin:0 0 18px; color:var(--sub); font-size:13.5px; line-height:1.5; }
  .sub code, code { background:var(--field); border:1px solid var(--line); padding:1px 5px;
    border-radius:5px; font-family:ui-monospace,monospace; font-size:12px; }
  .qr { display:grid; place-items:center; padding:14px; background:#fff; border-radius:12px;
    border:1px solid var(--line); width:292px; height:292px; margin:0 auto 14px; }
  .qr img { border-radius:6px; display:block; }
  .qr-empty { color:#888; font-size:13px; }
  .status { display:inline-flex; align-items:center; gap:8px; font-size:13px; color:var(--sub);
    margin-bottom:12px; }
  .dot { width:9px; height:9px; border-radius:50%; background:var(--brand);
    box-shadow:0 0 0 0 rgba(37,211,102,.5); animation:pulse 1.6s infinite; }
  .dot.ok { background:var(--ok); animation:none; }
  @keyframes pulse { 0%{box-shadow:0 0 0 0 rgba(37,211,102,.45)} 70%{box-shadow:0 0 0 8px rgba(37,211,102,0)} 100%{box-shadow:0 0 0 0 rgba(37,211,102,0)} }
  .alt { text-align:left; margin:10px 0 4px; border-top:1px solid var(--line); padding-top:14px; }
  .alt summary { cursor:pointer; font-size:13.5px; font-weight:600; }
  .row { display:flex; gap:8px; margin:12px 0 6px; }
  .field { flex:1; padding:11px 12px; background:var(--field); border:1px solid var(--line);
    border-radius:9px; color:var(--ink); font-size:14px; }
  .btn-sm { padding:11px 14px; background:var(--brand); color:var(--brand-ink); border:0;
    border-radius:9px; font-weight:700; font-size:13px; cursor:pointer; }
  .pair { min-height:20px; font-size:14px; color:var(--ink); margin-top:4px; }
  .pair .code, .code { font-family:ui-monospace,monospace; font-weight:700; letter-spacing:2px;
    background:var(--field); border:1px solid var(--line); padding:3px 8px; border-radius:6px; }
  .hint { font-size:12px; color:var(--sub); margin:8px 0 0; }
  .footer { margin-top:18px; font-size:11.5px; color:var(--sub); }
`;

function page(body: string): string {
  return `<!doctype html><html lang="pt-BR"><head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<meta name="robots" content="noindex"/>
<title>Conectar o WhatsApp</title>
<style>${STYLE}</style>
</head><body><div class="card">${body}</div></body></html>`;
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}
