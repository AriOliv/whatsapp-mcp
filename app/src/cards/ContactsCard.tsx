/**
 * Contacts card. The account can hold tens of thousands of contacts, so the
 * server returns a capped page and the search box re-queries server-side rather
 * than filtering a list we never received.
 */
import { useState } from "preact/hooks";
import { Icon } from "../ui/Icon";
import { Avatar, Badge, CardHeader, Empty, ErrorNote, IconButton } from "../ui/atoms";
import { count, phone } from "../lib/format";
import { contactName, type ContactRow, type ContactsPayload } from "../lib/types";
import { callTool, say } from "../lib/host";

const INLINE_ROWS = 5;

function ContactItem({ c, onPick }: { c: ContactRow; onPick: (c: ContactRow) => void }) {
  const name = contactName(c);
  const number = c.Number ? phone(c.Number) : c.RedactedPhone || "número privado";
  return (
    <button class="item" onClick={() => onPick(c)} aria-label={`Ver ${name}`}>
      <Avatar name={name} icon="user" />
      <div class="grow stack" style={{ gap: "2px" }}>
        <div class="row" style={{ gap: "6px" }}>
          <span class="strong truncate">{name}</span>
          {c.BusinessName ? <Badge tone="info" icon="store">Business</Badge> : null}
        </div>
        <span class="cap num truncate">{number}</span>
      </div>
      <span style={{ color: "var(--tx-3)", display: "flex", flex: "none" }}>
        <Icon name="chevron-right" size={15} />
      </span>
    </button>
  );
}

export function ContactsCard({
  data,
  fullscreen,
  onExpand,
  onPick,
}: {
  data: ContactsPayload;
  fullscreen: boolean;
  onExpand: () => void;
  onPick: (c: ContactRow) => void;
}) {
  const [contacts, setContacts] = useState(data.contacts ?? []);
  const [total, setTotal] = useState(data.total ?? contacts.length);
  const [query, setQuery] = useState(data.query ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function search(q: string) {
    setBusy(true);
    setError(null);
    try {
      const d = (await callTool("whatsapp_find_contacts", {
        query: q,
        limit: fullscreen ? 200 : 50,
      })) as Partial<ContactsPayload>;
      if (Array.isArray(d.contacts)) setContacts(d.contacts);
      if (typeof d.total === "number") setTotal(d.total);
    } catch (e) {
      setError(e instanceof Error ? e.message : "busca falhou");
    } finally {
      setBusy(false);
    }
  }

  const visible = fullscreen ? contacts : contacts.slice(0, INLINE_ROWS);
  const hidden = contacts.length - visible.length;

  return (
    <div class="card">
      <CardHeader
        icon="users"
        title="Contatos"
        subtitle={`${count(contacts.length)} de ${count(total)}${query ? ` · "${query}"` : ""}`}
        actions={!fullscreen ? <IconButton icon="expand" label="Abrir em tela cheia" onClick={onExpand} /> : undefined}
      />

      {fullscreen ? (
        <div class="card-pad" style={{ paddingTop: 0 }}>
          <form
            class="row"
            style={{ background: "var(--surface-2)", border: "var(--bw) solid var(--line-3)", borderRadius: "var(--r-md)", padding: "0 12px", minHeight: "44px" }}
            onSubmit={(e) => {
              e.preventDefault();
              void search(query);
            }}
          >
            <Icon name="search" size={15} />
            <input
              class="grow"
              type="search"
              value={query}
              placeholder="Buscar por nome ou número"
              aria-label="Buscar contato"
              onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
              style={{ border: 0, background: "transparent", color: "var(--tx)", font: "inherit", outline: "none", minHeight: "42px" }}
            />
            {busy ? <span class="cap">…</span> : null}
          </form>
        </div>
      ) : null}

      <hr class="divider" />

      {error ? <div class="card-pad"><ErrorNote message={error} /></div> : null}

      {visible.length === 0 ? (
        <Empty icon="users" title="Nenhum contato" hint={query ? "Tente outro nome ou número." : undefined} />
      ) : (
        <div>{visible.map((c) => <ContactItem key={c.JID} c={c} onPick={onPick} />)}</div>
      )}

      {!fullscreen && (hidden > 0 || total > contacts.length) ? (
        <>
          <hr class="divider" />
          <div class="card-pad">
            <button class="btn-secondary" style={{ width: "100%" }} onClick={onExpand}>
              Ver e buscar nos {count(total)} contatos
            </button>
          </div>
        </>
      ) : null}
    </div>
  );
}

/** Single-contact detail card. */
export function ContactCard({ data }: { data: { contact: ContactRow; status?: string; picture_url?: string; is_business?: boolean } }) {
  const c = data.contact;
  const name = contactName(c);
  const number = c.Number ? phone(c.Number) : c.RedactedPhone || "número privado";
  return (
    <div class="card">
      <div class="card-pad stack" style={{ alignItems: "center", textAlign: "center", gap: "8px", paddingTop: "20px" }}>
        <Avatar name={name} url={data.picture_url} size="lg" icon="user" />
        <div>
          <h1 class="h-lg">{name}</h1>
          <div class="cap num">{number}</div>
        </div>
        <div class="row" style={{ gap: "6px", flexWrap: "wrap", justifyContent: "center" }}>
          {data.is_business || c.BusinessName ? <Badge tone="info" icon="store">Business</Badge> : null}
          {c.Found ? <Badge tone="ok" icon="check">No WhatsApp</Badge> : null}
          {c.PushName && c.PushName !== name ? <Badge tone="neutral">“{c.PushName}”</Badge> : null}
        </div>
        {data.status ? <p class="body" style={{ margin: 0 }}>{data.status}</p> : null}
      </div>
      <hr class="divider" />
      <div class="card-pad actions">
        <button class="btn-secondary" onClick={() => say(`Mostre as últimas mensagens da conversa com ${name} (${c.JID}).`)}>
          Ver conversa
        </button>
        <button class="btn-primary" onClick={() => say(`Quero enviar uma mensagem para ${name} (${c.Number || c.JID}). Me ajude a escrever.`)}>
          Enviar mensagem
        </button>
      </div>
    </div>
  );
}
