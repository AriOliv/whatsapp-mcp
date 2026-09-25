/**
 * Thread card — one conversation rendered as bubbles.
 *
 * Media is fetched on demand (`whatsapp_download_media`) rather than shipped in
 * the tool result: a fat initial payload would blow past the host's inline-result
 * budget and the app would never hydrate. Images render inline from a data URI;
 * audio gets a real <audio> player; everything else downloads as a file.
 */
import { useMemo, useState } from "preact/hooks";
import { Icon, type IconName } from "../ui/Icon";
import { Avatar, CardHeader, Empty, ErrorNote, IconButton } from "../ui/atoms";
import { bytes, fullTime, timeOnly } from "../lib/format";
import { displayName, type MessageRow, type MessagesPayload, type MediaPayload } from "../lib/types";
import { callTool, say } from "../lib/host";

const INLINE_MSGS = 6;

const MEDIA_ICON: Record<string, IconName> = {
  image: "image", video: "video", audio: "mic", document: "doc", sticker: "sticker",
};
const MEDIA_LABEL: Record<string, string> = {
  image: "Foto", video: "Vídeo", audio: "Mensagem de voz", document: "Documento", sticker: "Figurinha",
};

type Loaded = { url: string; mimetype: string; filename: string; bytes: number };

function Media({ msg }: { msg: MessageRow }) {
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const type = msg.media_type ?? "document";

  async function load() {
    setBusy(true);
    setError(null);
    try {
      const d = (await callTool("whatsapp_download_media", { id: msg.id })) as Partial<MediaPayload>;
      if (!d.data_base64) throw new Error("mídia indisponível");
      setLoaded({
        url: `data:${d.mimetype || "application/octet-stream"};base64,${d.data_base64}`,
        mimetype: d.mimetype ?? "",
        filename: d.filename ?? "arquivo",
        bytes: d.bytes ?? 0,
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : "não foi possível baixar");
    } finally {
      setBusy(false);
    }
  }

  if (loaded && (type === "image" || type === "sticker")) {
    return <img class="msg-img" src={loaded.url} alt={MEDIA_LABEL[type] ?? "Mídia"} />;
  }
  if (loaded && type === "audio") {
    return <audio class="msg-img" controls src={loaded.url} style={{ width: "min(260px, 100%)" }} />;
  }
  if (loaded && type === "video") {
    return <video class="msg-img" controls src={loaded.url} style={{ maxHeight: "260px" }} />;
  }
  if (loaded) {
    return (
      <a class="msg-media" href={loaded.url} download={loaded.filename} style={{ textDecoration: "none" }}>
        <Icon name="download" size={15} />
        <span class="grow truncate">{loaded.filename}</span>
        <span class="cap">{bytes(loaded.bytes)}</span>
      </a>
    );
  }
  return (
    <>
      <button class="msg-media" onClick={load} disabled={busy} aria-label={`Carregar ${MEDIA_LABEL[type] ?? "mídia"}`}>
        <Icon name={MEDIA_ICON[type] ?? "doc"} size={15} />
        <span class="grow">{busy ? "Carregando…" : MEDIA_LABEL[type] ?? "Anexo"}</span>
        {!busy ? <Icon name="download" size={14} /> : null}
      </button>
      {error ? <div class="cap" style={{ color: "var(--danger-tx)" }}>{error}</div> : null}
    </>
  );
}

function Bubble({ msg, isGroup }: { msg: MessageRow; isGroup: boolean }) {
  return (
    <div class={`msg${msg.from_me ? " mine" : ""}`}>
      {isGroup && !msg.from_me && msg.sender_name ? <div class="msg-sender">{msg.sender_name}</div> : null}
      {msg.has_media ? <Media msg={msg} /> : null}
      {msg.body ? <div>{msg.body}</div> : null}
      <div class="msg-meta">
        <time dateTime={new Date(msg.ts_millis).toISOString()} title={fullTime(msg.ts_millis)}>
          {timeOnly(msg.ts_millis)}
        </time>
        {msg.from_me ? <Icon name="check-double" size={12} /> : null}
      </div>
    </div>
  );
}

export function ThreadCard({
  data,
  fullscreen,
  onExpand,
  onBack,
}: {
  data: MessagesPayload;
  fullscreen: boolean;
  onExpand: () => void;
  onBack?: () => void;
}) {
  const [messages, setMessages] = useState(data.messages ?? []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isGroup = data.chat?.kind === "group";
  const name = displayName({ name: data.chat?.name, jid: data.chat?.jid });

  // The tool returns newest-first; render oldest→newest like a real thread.
  const ordered = useMemo(() => [...messages].reverse(), [messages]);
  const visible = fullscreen ? ordered : ordered.slice(-INLINE_MSGS);
  const hidden = ordered.length - visible.length;

  async function loadMore() {
    setBusy(true);
    setError(null);
    try {
      const d = (await callTool("whatsapp_find_messages", {
        remoteJid: data.chat.jid,
        limit: Math.min(messages.length + 50, 200),
      })) as Partial<MessagesPayload>;
      if (Array.isArray(d.messages)) setMessages(d.messages);
    } catch (e) {
      setError(e instanceof Error ? e.message : "não foi possível carregar");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div class="card">
      <CardHeader
        icon={isGroup ? "group" : "user"}
        title={name}
        subtitle={
          isGroup && data.chat.participant_count
            ? `${data.chat.participant_count} participantes`
            : `${ordered.length} ${ordered.length === 1 ? "mensagem" : "mensagens"}`
        }
        actions={
          <>
            {onBack && fullscreen ? <IconButton icon="chevron-left" label="Voltar" onClick={onBack} /> : null}
            <IconButton icon="refresh" label="Atualizar mensagens" onClick={loadMore} disabled={busy} />
            {!fullscreen ? <IconButton icon="expand" label="Abrir em tela cheia" onClick={onExpand} /> : null}
          </>
        }
      />
      <hr class="divider" />

      {visible.length === 0 ? (
        <Empty icon="chat" title="Sem mensagens" hint="Nada foi guardado para esta conversa ainda." />
      ) : (
        <div class="thread">
          {!fullscreen && hidden > 0 ? (
            <button class="day-sep" onClick={onExpand} style={{ cursor: "pointer", minHeight: "unset" }}>
              +{hidden} anteriores
            </button>
          ) : null}
          {visible.map((m) => (
            <Bubble key={m.id} msg={m} isGroup={isGroup} />
          ))}
        </div>
      )}

      {error ? <div class="card-pad" style={{ paddingTop: 0 }}><ErrorNote message={error} /></div> : null}

      <hr class="divider" />
      <div class="card-pad actions">
        <button class="btn-secondary" onClick={() => say(`Resuma a conversa com ${name} (${data.chat.jid}).`)}>
          Resumir
        </button>
        <button class="btn-primary" onClick={() => say(`Quero responder ${name} (${data.chat.jid}). Me ajude a escrever a mensagem.`)}>
          Responder
        </button>
      </div>
    </div>
  );
}
