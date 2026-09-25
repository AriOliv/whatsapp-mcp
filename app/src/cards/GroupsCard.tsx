/**
 * Groups cards.
 *
 * Inline uses a carousel — groups are comparable items, horizontal swipe is the
 * one gesture an inline app owns, and each slide carries exactly one CTA.
 * Fullscreen switches to a searchable list.
 */
import { useState } from "preact/hooks";
import { Icon } from "../ui/Icon";
import { Avatar, Badge, CardHeader, Empty, ErrorNote, IconButton } from "../ui/atoms";
import { count } from "../lib/format";
import { displayName, type GroupRow, type GroupsPayload, type ParticipantRow, type GroupPayload } from "../lib/types";
import { callTool, say } from "../lib/host";

function GroupSlide({ g, onOpen }: { g: GroupRow; onOpen: (g: GroupRow) => void }) {
  const name = displayName(g);
  return (
    <div class="slide">
      <div class="row">
        <Avatar name={name} icon="group" />
        <div class="grow" style={{ minWidth: 0 }}>
          <div class="strong truncate">{name}</div>
          <div class="cap">{count(g.participant_count)} participantes</div>
        </div>
      </div>
      {g.topic ? <p class="cap clamp-2" style={{ margin: 0 }}>{g.topic}</p> : null}
      <div class="row" style={{ gap: "4px", flexWrap: "wrap" }}>
        {g.am_admin ? <Badge tone="accent" icon="shield">Admin</Badge> : null}
        {g.is_announce ? <Badge tone="warn" icon="megaphone">Só admins</Badge> : null}
        {g.is_community ? <Badge tone="info" icon="users">Comunidade</Badge> : null}
        {g.is_ephemeral ? <Badge tone="neutral" icon="clock">Temporárias</Badge> : null}
      </div>
      <button class="btn-secondary" style={{ marginTop: "auto" }} onClick={() => onOpen(g)}>
        Ver grupo
      </button>
    </div>
  );
}

export function GroupsCard({
  data,
  fullscreen,
  onExpand,
  onOpen,
}: {
  data: GroupsPayload;
  fullscreen: boolean;
  onExpand: () => void;
  onOpen: (g: GroupRow) => void;
}) {
  const [groups, setGroups] = useState(data.groups ?? []);
  const [query, setQuery] = useState(data.query ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const total = data.total ?? groups.length;

  async function search(q: string) {
    setBusy(true);
    setError(null);
    try {
      const d = (await callTool("whatsapp_group_fetch_all", { query: q, limit: 200 })) as Partial<GroupsPayload>;
      if (Array.isArray(d.groups)) setGroups(d.groups);
    } catch (e) {
      setError(e instanceof Error ? e.message : "busca falhou");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div class="card">
      <CardHeader
        icon="group"
        title="Grupos"
        subtitle={`${count(groups.length)} de ${count(total)}${query ? ` · "${query}"` : ""}`}
        actions={!fullscreen ? <IconButton icon="expand" label="Abrir em tela cheia" onClick={onExpand} /> : undefined}
      />

      {fullscreen ? (
        <div class="card-pad" style={{ paddingTop: 0 }}>
          <form
            class="row"
            style={{ background: "var(--surface-2)", border: "var(--bw) solid var(--line-3)", borderRadius: "var(--r-md)", padding: "0 12px", minHeight: "44px" }}
            onSubmit={(e) => { e.preventDefault(); void search(query); }}
          >
            <Icon name="search" size={15} />
            <input
              class="grow" type="search" value={query} placeholder="Buscar grupo" aria-label="Buscar grupo"
              onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
              style={{ border: 0, background: "transparent", color: "var(--tx)", font: "inherit", outline: "none", minHeight: "42px" }}
            />
            {busy ? <span class="cap">…</span> : null}
          </form>
        </div>
      ) : null}

      {error ? <div class="card-pad" style={{ paddingTop: 0 }}><ErrorNote message={error} /></div> : null}

      {groups.length === 0 ? (
        <>
          <hr class="divider" />
          <Empty icon="group" title="Nenhum grupo" hint={query ? "Tente outro termo." : "Você ainda não participa de grupos."} />
        </>
      ) : fullscreen ? (
        <>
          <hr class="divider" />
          <div>
            {groups.map((g) => (
              <button key={g.jid} class="item" onClick={() => onOpen(g)}>
                <Avatar name={displayName(g)} icon="group" />
                <div class="grow stack" style={{ gap: "2px" }}>
                  <span class="strong truncate">{displayName(g)}</span>
                  <span class="cap truncate">{count(g.participant_count)} participantes{g.topic ? ` · ${g.topic}` : ""}</span>
                </div>
                {g.am_admin ? <Badge tone="accent" icon="shield">Admin</Badge> : null}
                <Icon name="chevron-right" size={15} />
              </button>
            ))}
          </div>
        </>
      ) : (
        <div class="carousel">
          {groups.slice(0, 8).map((g) => <GroupSlide key={g.jid} g={g} onOpen={onOpen} />)}
        </div>
      )}

      {!fullscreen && groups.length > 0 ? (
        <div class="card-pad" style={{ paddingTop: 0 }}>
          <button class="btn-secondary" style={{ width: "100%" }} onClick={onExpand}>
            Ver todos os {count(total)} grupos
          </button>
        </div>
      ) : null}
    </div>
  );
}

function ParticipantItem({ p }: { p: ParticipantRow }) {
  const name = p.name || (p.number ? `+${p.number}` : p.jid.split("@")[0] ?? "");
  return (
    <div class="item" style={{ minHeight: "48px" }}>
      <Avatar name={p.name} size="sm" icon="user" />
      <span class="grow truncate">{name}</span>
      {p.is_super_admin ? <Badge tone="accent" icon="shield">Dono</Badge> : p.is_admin ? <Badge tone="info" icon="shield">Admin</Badge> : null}
    </div>
  );
}

/** Single-group detail: metadata, stats and participants. */
export function GroupCard({
  data,
  fullscreen,
  onExpand,
}: {
  data: GroupPayload;
  fullscreen: boolean;
  onExpand: () => void;
}) {
  const g = data.group;
  const name = displayName(g);
  const [participants, setParticipants] = useState(data.participants ?? []);
  const [busy, setBusy] = useState(false);
  const admins = participants.filter((p) => p.is_admin || p.is_super_admin).length;
  const shown = fullscreen ? participants : participants.slice(0, 4);

  async function loadParticipants() {
    setBusy(true);
    try {
      const d = (await callTool("whatsapp_group_participants", { groupJid: g.jid })) as { participants?: ParticipantRow[] };
      if (Array.isArray(d.participants)) setParticipants(d.participants);
    } catch {
      /* leave what we have */
    } finally {
      setBusy(false);
    }
  }

  return (
    <div class="card">
      <CardHeader
        icon="group"
        title={name}
        subtitle={g.created ? `Criado em ${g.created}` : undefined}
        actions={!fullscreen ? <IconButton icon="expand" label="Abrir em tela cheia" onClick={onExpand} /> : undefined}
      />
      <div class="card-pad stack" style={{ paddingTop: 0 }}>
        {g.topic ? <p class="body" style={{ margin: 0 }}>{g.topic}</p> : null}
        <div class="stat-row">
          <div class="stat">
            <div class="stat-v">{count(g.participant_count)}</div>
            <div class="stat-k">participantes</div>
          </div>
          <div class="stat">
            <div class="stat-v">{count(admins || 0)}</div>
            <div class="stat-k">admins</div>
          </div>
        </div>
        <div class="row" style={{ gap: "4px", flexWrap: "wrap" }}>
          {g.am_admin ? <Badge tone="accent" icon="shield">Você é admin</Badge> : null}
          {g.is_announce ? <Badge tone="warn" icon="megaphone">Só admins enviam</Badge> : null}
          {g.is_locked ? <Badge tone="neutral" icon="lock">Info travada</Badge> : null}
          {g.is_ephemeral ? <Badge tone="neutral" icon="clock">Mensagens temporárias</Badge> : null}
        </div>
      </div>

      {participants.length > 0 ? (
        <>
          <hr class="divider" />
          <div>{shown.map((p) => <ParticipantItem key={p.jid} p={p} />)}</div>
          {!fullscreen && participants.length > shown.length ? (
            <div class="card-pad" style={{ paddingTop: "8px" }}>
              <button class="btn-secondary" style={{ width: "100%" }} onClick={onExpand}>
                Ver os {count(participants.length)} participantes
              </button>
            </div>
          ) : null}
        </>
      ) : (
        <>
          <hr class="divider" />
          <div class="card-pad">
            <button class="btn-secondary" style={{ width: "100%" }} onClick={loadParticipants} disabled={busy}>
              {busy ? "Carregando…" : "Carregar participantes"}
            </button>
          </div>
        </>
      )}

      <hr class="divider" />
      <div class="card-pad actions">
        <button class="btn-secondary" onClick={() => say(`Resuma as últimas mensagens do grupo ${name} (${g.jid}).`)}>
          Resumir
        </button>
        <button class="btn-primary" onClick={() => say(`Quero enviar uma mensagem no grupo ${name} (${g.jid}). Me ajude a escrever.`)}>
          Enviar
        </button>
      </div>
    </div>
  );
}

/** Participants-only card (whatsapp_group_participants called directly). */
export function ParticipantsCard({
  data,
  fullscreen,
  onExpand,
}: {
  data: { group: { jid: string; name?: string }; participants: ParticipantRow[] };
  fullscreen: boolean;
  onExpand: () => void;
}) {
  const list = data.participants ?? [];
  const shown = fullscreen ? list : list.slice(0, 6);
  const admins = list.filter((p) => p.is_admin || p.is_super_admin).length;
  return (
    <div class="card">
      <CardHeader
        icon="users"
        title={data.group?.name || "Participantes"}
        subtitle={`${count(list.length)} participantes · ${count(admins)} admins`}
        actions={!fullscreen ? <IconButton icon="expand" label="Abrir em tela cheia" onClick={onExpand} /> : undefined}
      />
      <hr class="divider" />
      {list.length === 0 ? (
        <Empty icon="users" title="Sem participantes" />
      ) : (
        <div>{shown.map((p) => <ParticipantItem key={p.jid} p={p} />)}</div>
      )}
      {!fullscreen && list.length > shown.length ? (
        <div class="card-pad">
          <button class="btn-secondary" style={{ width: "100%" }} onClick={onExpand}>
            Ver todos os {count(list.length)}
          </button>
        </div>
      ) : null}
    </div>
  );
}
