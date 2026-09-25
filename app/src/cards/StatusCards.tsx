/**
 * The small, high-frequency cards: account state, send receipts, number checks,
 * media, poll, location, privacy, business profile, invite link, devices.
 *
 * Each stays inside the inline budget — at most a handful of data points and
 * two actions — and every one of them is a real confirmation the user can read
 * at a glance instead of raw JSON.
 */
import { useState } from "preact/hooks";
import { Icon, type IconName } from "../ui/Icon";
import { Avatar, Badge, CardHeader, Empty, IconButton } from "../ui/atoms";
import { bytes, count, fullTime, phone } from "../lib/format";
import type {
  AccountPayload, BusinessPayload, DevicesPayload, InvitePayload, LocationPayload,
  MediaPayload, NumbersPayload, PollPayload, PrivacyPayload, ReceiptPayload, SentPayload,
} from "../lib/types";
import { say } from "../lib/host";

/* ------------------------------------------------------------- account -- */

export function AccountCard({ data }: { data: AccountPayload }) {
  const a = data.account;
  const online = a.connected && a.logged_in;
  return (
    <div class="card">
      <div class="card-pad row" style={{ gap: "12px" }}>
        <Avatar name={a.push_name} size="lg" icon="phone" />
        <div class="grow stack" style={{ gap: "4px" }}>
          <div class="row" style={{ gap: "8px" }}>
            <h1 class="h truncate">{a.push_name || "Conta WhatsApp"}</h1>
            <Badge tone={online ? "ok" : "danger"} icon={online ? "check" : "alert"}>
              {online ? "Conectado" : a.logged_in ? "Reconectando" : "Desconectado"}
            </Badge>
          </div>
          <span class="cap num">{a.number ? phone(a.number) : a.jid}</span>
        </div>
      </div>
      {data.chats != null || data.contacts != null || data.groups != null ? (
        <div class="card-pad stat-row" style={{ paddingTop: 0 }}>
          {data.chats != null ? <div class="stat"><div class="stat-v">{count(data.chats)}</div><div class="stat-k">conversas</div></div> : null}
          {data.contacts != null ? <div class="stat"><div class="stat-v">{count(data.contacts)}</div><div class="stat-k">contatos</div></div> : null}
          {data.groups != null ? <div class="stat"><div class="stat-v">{count(data.groups)}</div><div class="stat-k">grupos</div></div> : null}
        </div>
      ) : null}
      <hr class="divider" />
      <div class="card-pad actions">
        <button class="btn-secondary" onClick={() => say("Liste minhas conversas recentes do WhatsApp.")}>Conversas</button>
        <button class="btn-primary" onClick={() => say("Liste meus grupos do WhatsApp.")}>Grupos</button>
      </div>
    </div>
  );
}

/* ---------------------------------------------------------------- sent -- */

const SENT_LABEL: Record<SentPayload["what"], { title: string; icon: IconName }> = {
  text: { title: "Mensagem enviada", icon: "send" },
  media: { title: "Mídia enviada", icon: "image" },
  audio: { title: "Áudio enviado", icon: "mic" },
  sticker: { title: "Figurinha enviada", icon: "sticker" },
  location: { title: "Localização enviada", icon: "pin" },
  contact: { title: "Contato enviado", icon: "user" },
  poll: { title: "Enquete enviada", icon: "poll" },
  reaction: { title: "Reação enviada", icon: "check" },
  edit: { title: "Mensagem editada", icon: "edit" },
  delete: { title: "Mensagem apagada", icon: "trash" },
};

export function SentCard({ data }: { data: SentPayload }) {
  const meta = SENT_LABEL[data.what] ?? SENT_LABEL.text;
  const to = data.to_name || (data.to?.includes("@") ? data.to.split("@")[0] : data.to) || "";
  return (
    <div class="card">
      <div class="card-pad row" style={{ gap: "12px" }}>
        <div class="avatar" style={{ background: "var(--ok-bg)", color: "var(--ok-tx)", borderColor: "transparent" }} aria-hidden="true">
          <Icon name={meta.icon} size={18} />
        </div>
        <div class="grow stack" style={{ gap: "2px" }}>
          <span class="strong">{meta.title}</span>
          <span class="cap truncate">para {to}</span>
          {data.preview ? <span class="body clamp-2" style={{ marginTop: "4px" }}>{data.preview}</span> : null}
        </div>
      </div>
      {data.ts_millis ? <div class="card-pad cap" style={{ paddingTop: 0 }}>{fullTime(data.ts_millis)}</div> : null}
    </div>
  );
}

/* ------------------------------------------------------------- receipt -- */

export function ReceiptCard({ data }: { data: ReceiptPayload }) {
  return (
    <div class="card">
      <div class="card-pad row" style={{ gap: "12px" }}>
        <div
          class="avatar"
          style={{
            background: data.ok ? "var(--ok-bg)" : "var(--danger-bg)",
            color: data.ok ? "var(--ok-tx)" : "var(--danger-tx)",
            borderColor: "transparent",
          }}
          aria-hidden="true"
        >
          <Icon name={data.ok ? "check" : "alert"} size={18} />
        </div>
        <div class="grow stack" style={{ gap: "2px" }}>
          <span class="strong">{data.action}</span>
          {data.detail ? <span class="cap">{data.detail}</span> : null}
          {data.target ? <span class="cap num truncate">{data.target}</span> : null}
        </div>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------- numbers -- */

export function NumbersCard({ data }: { data: NumbersPayload }) {
  const list = data.numbers ?? [];
  const on = list.filter((n) => n.registered).length;
  return (
    <div class="card">
      <CardHeader icon="phone" title="Números no WhatsApp" subtitle={`${count(on)} de ${count(list.length)} registrados`} />
      <hr class="divider" />
      {list.length === 0 ? (
        <Empty icon="phone" title="Nenhum número verificado" />
      ) : (
        <div>
          {list.map((n) => (
            <div key={n.query} class="item" style={{ minHeight: "48px" }}>
              <span class="dot" style={{ background: n.registered ? "var(--ok-tx)" : "var(--tx-3)" }} />
              <span class="grow num truncate">{phone(n.query)}</span>
              {n.business_name ? <Badge tone="info" icon="store">{n.business_name}</Badge> : null}
              <Badge tone={n.registered ? "ok" : "neutral"}>{n.registered ? "Tem WhatsApp" : "Não tem"}</Badge>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/* --------------------------------------------------------------- media -- */

export function MediaCard({ data }: { data: MediaPayload }) {
  const url = `data:${data.mimetype};base64,${data.data_base64}`;
  const isImage = data.mimetype?.startsWith("image/");
  const isAudio = data.mimetype?.startsWith("audio/");
  const isVideo = data.mimetype?.startsWith("video/");
  return (
    <div class="card">
      <CardHeader icon={isImage ? "image" : isAudio ? "mic" : isVideo ? "video" : "doc"} title={data.filename || "Mídia"} subtitle={[data.mimetype, bytes(data.bytes)].filter(Boolean).join(" · ")} />
      <div class="card-pad" style={{ paddingTop: 0 }}>
        {isImage ? <img class="msg-img" src={url} alt={data.filename} style={{ width: "100%" }} /> : null}
        {isAudio ? <audio controls src={url} style={{ width: "100%" }} /> : null}
        {isVideo ? <video controls src={url} style={{ width: "100%", maxHeight: "320px", borderRadius: "var(--r-md)" }} /> : null}
      </div>
      <hr class="divider" />
      <div class="card-pad">
        <a class="btn-primary" href={url} download={data.filename} style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: "8px", minHeight: "44px", borderRadius: "var(--r-md)", textDecoration: "none" }}>
          <Icon name="download" size={15} /> Baixar
        </a>
      </div>
    </div>
  );
}

/* ---------------------------------------------------------------- poll -- */

export function PollCard({ data }: { data: PollPayload }) {
  return (
    <div class="card">
      <CardHeader icon="poll" title={data.name} subtitle={`Enquete enviada para ${data.to_name || data.to} · escolha ${data.selectable === 1 ? "1 opção" : `até ${data.selectable}`}`} />
      <hr class="divider" />
      <div class="card-pad stack" style={{ gap: "8px" }}>
        {(data.options ?? []).map((o, i) => (
          <div key={i} class="row" style={{ gap: "10px", padding: "8px 12px", background: "var(--surface-2)", border: "var(--bw) solid var(--line-3)", borderRadius: "var(--r-md)" }}>
            <span class="cap num" style={{ flex: "none", width: "18px" }}>{i + 1}</span>
            <span class="grow">{o}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

/* ------------------------------------------------------------ location -- */

export function LocationCard({ data }: { data: LocationPayload }) {
  const coords = `${data.latitude?.toFixed(5)}, ${data.longitude?.toFixed(5)}`;
  return (
    <div class="card">
      <CardHeader icon="pin" title={data.name || "Localização enviada"} subtitle={`para ${data.to_name || data.to}`} />
      <div class="card-pad stack" style={{ paddingTop: 0, gap: "8px" }}>
        {data.address ? <p class="body" style={{ margin: 0 }}>{data.address}</p> : null}
        <div class="row" style={{ gap: "8px" }}>
          <Icon name="globe" size={14} />
          <span class="cap num">{coords}</span>
        </div>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------- privacy -- */

const PRIVACY_LABEL: Record<string, string> = {
  group_add: "Adicionar a grupos", last_seen: "Visto por último", status: "Status",
  profile: "Foto do perfil", read_receipts: "Confirmações de leitura", call_add: "Chamadas",
  online: "Online", messages: "Mensagens", defense: "Proteção", stickers: "Figurinhas",
};
const PRIVACY_VALUE: Record<string, string> = {
  all: "Todos", contacts: "Meus contatos", contact_blacklist: "Contatos exceto…",
  none: "Ninguém", match_last_seen: "Igual ao visto por último", known: "Conhecidos",
  contact_allowlist: "Contatos selecionados", on: "Ativada", off: "Desativada",
};

export function PrivacyCard({ data }: { data: PrivacyPayload }) {
  const entries = Object.entries(data.settings ?? {});
  return (
    <div class="card">
      <CardHeader icon="lock" title="Privacidade" subtitle={`${entries.length} configurações`} />
      <hr class="divider" />
      <div class="card-pad">
        <dl class="kv">
          {entries.map(([k, v]) => (
            <>
              <dt key={`k-${k}`}>{PRIVACY_LABEL[k] ?? k}</dt>
              <dd key={`v-${k}`}>{PRIVACY_VALUE[v] ?? v}</dd>
            </>
          ))}
        </dl>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------ business -- */

export function BusinessCard({ data }: { data: BusinessPayload }) {
  return (
    <div class="card">
      <CardHeader icon="store" title={data.name || "Perfil Business"} subtitle={data.categories?.join(" · ")} />
      <div class="card-pad stack" style={{ paddingTop: 0, gap: "10px" }}>
        <dl class="kv">
          {data.address ? (<><dt>Endereço</dt><dd>{data.address}</dd></>) : null}
          {data.email ? (<><dt>E-mail</dt><dd>{data.email}</dd></>) : null}
          {data.timezone ? (<><dt>Fuso</dt><dd>{data.timezone}</dd></>) : null}
        </dl>
        {data.hours?.length ? (
          <div class="stack" style={{ gap: "4px" }}>
            <span class="cap strong">Horários</span>
            {data.hours.map((h, i) => (
              <div key={i} class="row-between cap">
                <span>{h.day}</span>
                <span class="num">{h.mode === "open" && h.open ? `${h.open}–${h.close}` : h.mode}</span>
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}

/* -------------------------------------------------------------- invite -- */

export function InviteCard({ data }: { data: InvitePayload }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(data.invite_link);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      /* clipboard can be blocked in the sandbox; the link stays selectable */
    }
  }
  return (
    <div class="card">
      <CardHeader icon="link" title="Link de convite" subtitle={data.group_name} />
      <div class="card-pad stack" style={{ paddingTop: 0, gap: "10px" }}>
        <code
          class="num"
          style={{ display: "block", padding: "10px 12px", background: "var(--surface-2)", border: "var(--bw) solid var(--line-3)", borderRadius: "var(--r-md)", fontSize: "var(--t-cap)", overflowWrap: "anywhere", userSelect: "all" }}
        >
          {data.invite_link}
        </code>
        <button class="btn-primary" onClick={copy}>
          {copied ? "Copiado ✓" : "Copiar link"}
        </button>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------- devices -- */

export function DevicesCard({ data }: { data: DevicesPayload }) {
  const list = data.devices ?? [];
  return (
    <div class="card">
      <CardHeader icon="phone" title="Dispositivos vinculados" subtitle={`${count(list.length)} ${list.length === 1 ? "conta" : "contas"}`} />
      <hr class="divider" />
      {list.length === 0 ? (
        <Empty icon="phone" title="Nenhum dispositivo" hint="Nenhuma conta vinculada a este servidor." />
      ) : (
        <div>
          {list.map((d) => (
            <div key={d} class="item" style={{ minHeight: "48px" }}>
              <Icon name="phone" size={15} />
              <span class="grow num truncate">{d}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
