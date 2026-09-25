/**
 * Chats card — the entry point: recent conversations.
 *
 * Inline: a compact list (the host caps inline height, and vertical pan inside
 * an inline app scrolls the conversation, not us — so we show a slice and offer
 * fullscreen). Fullscreen: the whole list with a search field.
 */
import { useMemo, useState } from "preact/hooks";
import { Icon } from "../ui/Icon";
import { Avatar, Badge, CardHeader, Empty, IconButton } from "../ui/atoms";
import { chatTime } from "../lib/format";
import { displayName, type ChatRow, type ChatsPayload } from "../lib/types";
import { callTool, say } from "../lib/host";

const INLINE_ROWS = 5;

function chatIcon(c: ChatRow) {
  if (c.kind === "group") return "group" as const;
  if (c.kind === "newsletter") return "megaphone" as const;
  if (c.kind === "status") return "eye" as const;
  return "user" as const;
}

function mediaLabel(t?: string): string | undefined {
  switch (t) {
    case "image": return "Foto";
    case "video": return "Vídeo";
    case "audio": return "Áudio";
    case "document": return "Documento";
    case "sticker": return "Figurinha";
    default: return undefined;
  }
}

function ChatItem({ chat, onOpen }: { chat: ChatRow; onOpen: (c: ChatRow) => void }) {
  const name = displayName(chat);
  const media = mediaLabel(chat.media_type);
  const preview = chat.preview?.trim();
  return (
    <button class="item" onClick={() => onOpen(chat)} aria-label={`Abrir conversa com ${name}`}>
      <Avatar name={chat.name} icon={chatIcon(chat)} />
      <div class="grow stack" style={{ gap: "2px" }}>
        <div class="row-between">
          <span class="strong truncate">{name}</span>
          <span class="cap num" style={{ flex: "none" }}>{chatTime(chat.last_ts_millis)}</span>
        </div>
        <div class="row" style={{ gap: "5px" }}>
          {chat.from_me ? (
            <span style={{ color: "var(--tx-3)", display: "flex" }} aria-label="Enviada por você">
              <Icon name="check-double" size={13} />
            </span>
          ) : null}
          {media ? (
            <span class="cap row" style={{ gap: "3px" }}>
              <Icon name={chat.media_type === "audio" ? "mic" : chat.media_type === "video" ? "video" : chat.media_type === "document" ? "doc" : chat.media_type === "sticker" ? "sticker" : "image"} size={12} />
              {media}
            </span>
          ) : null}
          <span class="cap truncate">{preview || (media ? "" : "Sem mensagens")}</span>
        </div>
      </div>
      <span style={{ color: "var(--tx-3)", display: "flex", flex: "none" }}>
        <Icon name="chevron-right" size={15} />
      </span>
    </button>
  );
}

export function ChatsCard({
  data,
  fullscreen,
  onExpand,
  onOpenChat,
}: {
  data: ChatsPayload;
  fullscreen: boolean;
  onExpand: () => void;
  onOpenChat: (chat: ChatRow) => void;
}) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<"all" | "dm" | "group">("all");
  const [busy, setBusy] = useState(false);
  const [chats, setChats] = useState(data.chats ?? []);

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return chats.filter((c) => {
      if (filter === "dm" && c.kind !== "dm") return false;
      if (filter === "group" && c.kind !== "group") return false;
      if (!q) return true;
      return displayName(c).toLowerCase().includes(q) || (c.preview ?? "").toLowerCase().includes(q);
    });
  }, [chats, query, filter]);

  const visible = fullscreen ? shown : shown.slice(0, INLINE_ROWS);
  const hidden = shown.length - visible.length;

  async function refresh() {
    setBusy(true);
    try {
      const d = (await callTool("whatsapp_find_chats", { limit: fullscreen ? 200 : 50 })) as Partial<ChatsPayload>;
      if (Array.isArray(d.chats)) setChats(d.chats);
    } catch {
      /* keep what we have; the list stays usable */
    } finally {
      setBusy(false);
    }
  }

  return (
    <div class="card">
      <CardHeader
        icon="chat"
        title="Conversas"
        subtitle={`${shown.length} ${shown.length === 1 ? "conversa" : "conversas"}${data.account?.push_name ? ` · ${data.account.push_name}` : ""}`}
        actions={
          <>
            <IconButton icon="refresh" label="Atualizar conversas" onClick={refresh} disabled={busy} />
            {!fullscreen ? <IconButton icon="expand" label="Abrir em tela cheia" onClick={onExpand} /> : null}
          </>
        }
      />

      {fullscreen ? (
        <div class="card-pad stack" style={{ paddingTop: 0, gap: "10px" }}>
          <label class="row" style={{ background: "var(--surface-2)", border: "var(--bw) solid var(--line-3)", borderRadius: "var(--r-md)", padding: "0 12px", minHeight: "44px" }}>
            <Icon name="search" size={15} />
            <input
              class="grow"
              type="search"
              value={query}
              placeholder="Buscar conversa"
              aria-label="Buscar conversa"
              onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
              style={{ border: 0, background: "transparent", color: "var(--tx)", font: "inherit", outline: "none", minHeight: "42px" }}
            />
          </label>
          <div class="segmented" role="group" aria-label="Filtrar por tipo">
            {([["all", "Todas"], ["dm", "Pessoas"], ["group", "Grupos"]] as const).map(([k, label]) => (
              <button key={k} aria-pressed={filter === k} onClick={() => setFilter(k)}>{label}</button>
            ))}
          </div>
        </div>
      ) : null}

      <hr class="divider" />

      {visible.length === 0 ? (
        <Empty icon="chat" title="Nenhuma conversa" hint={query ? "Tente outro termo de busca." : "As conversas aparecem aqui conforme chegam mensagens."} />
      ) : (
        <div>
          {visible.map((c) => (
            <ChatItem key={c.jid} chat={c} onOpen={onOpenChat} />
          ))}
        </div>
      )}

      {!fullscreen && hidden > 0 ? (
        <>
          <hr class="divider" />
          <div class="card-pad">
            <button class="btn-secondary" style={{ width: "100%" }} onClick={onExpand}>
              Ver todas as {shown.length} conversas
            </button>
          </div>
        </>
      ) : null}
    </div>
  );
}

/** Quick actions that belong in chat (they need Claude's interpretation). */
export function chatSuggestion(chat: ChatRow): void {
  say(`Mostre as últimas mensagens da conversa com ${displayName(chat)} (${chat.jid}).`);
}

export type { ChatRow };
export { Badge };
