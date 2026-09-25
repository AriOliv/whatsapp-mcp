/**
 * Card router.
 *
 * The server hands every UI-backed tool the SAME `ui://whatsapp/app.html`
 * resource and puts a `kind` discriminator in `structuredContent`; this
 * component picks the card. One resource means one "Always allow" prompt for
 * the user and one bundle to ship, instead of one per tool.
 *
 * Inline is the default. `onExpand` asks the host for fullscreen — the only
 * place a vertically scrollable viewport is legitimate, since vertical pans in
 * an inline app belong to the conversation.
 */
import { useCallback, useEffect, useState } from "preact/hooks";
import { app, applyHostContext, callTool, type McpUiHostContext } from "./lib/host";
import { setLocale } from "./lib/format";
import { SkeletonRows, ErrorNote, CardHeader } from "./ui/atoms";
import type {
  ChatRow, ChatsPayload, ContactRow, ContactsPayload, ContactPayload, GroupRow,
  GroupsPayload, GroupPayload, MessagesPayload, ParticipantsPayload, Payload,
} from "./lib/types";
import { ChatsCard } from "./cards/ChatsCard";
import { ThreadCard } from "./cards/ThreadCard";
import { ContactsCard, ContactCard } from "./cards/ContactsCard";
import { GroupsCard, GroupCard, ParticipantsCard } from "./cards/GroupsCard";
import {
  AccountCard, BusinessCard, DevicesCard, InviteCard, LocationCard, MediaCard,
  NumbersCard, PollCard, PrivacyCard, ReceiptCard, SentCard,
} from "./cards/StatusCards";

type View = { payload: Payload; back?: View };

export function App() {
  const [view, setView] = useState<View | null>(null);
  const [ctx, setCtx] = useState<McpUiHostContext | undefined>(app.getHostContext());
  const [error, setError] = useState<string | null>(null);
  const fullscreen = ctx?.displayMode === "fullscreen";

  useEffect(() => {
    const onResult = (params: unknown) => {
      const data = (params as { structuredContent?: Record<string, unknown> })?.structuredContent;
      if (data && typeof data === "object" && typeof (data as { kind?: unknown }).kind === "string") {
        setView({ payload: data as unknown as Payload });
      }
    };
    const onCtx = (next: McpUiHostContext) => {
      applyHostContext(next);
      setLocale(next.locale, next.timeZone);
      setCtx((prev) => ({ ...prev, ...next }));
    };
    app.addEventListener("toolresult", onResult);
    app.addEventListener("hostcontextchanged", onCtx);
    return () => {
      app.removeEventListener("toolresult", onResult);
      app.removeEventListener("hostcontextchanged", onCtx);
    };
  }, []);

  const expand = useCallback(() => {
    void app.requestDisplayMode({ mode: "fullscreen" }).catch(() => {});
  }, []);

  /** Drill-in inside fullscreen only; inline stays a single view (no drill-ins). */
  const push = useCallback(
    async (loader: () => Promise<unknown>) => {
      if (!fullscreen) {
        expand();
      }
      setError(null);
      try {
        const data = (await loader()) as Payload;
        if (data && typeof (data as { kind?: unknown }).kind === "string") {
          setView((prev) => ({ payload: data, back: prev ?? undefined }));
        }
      } catch (e) {
        setError(e instanceof Error ? e.message : "não foi possível carregar");
      }
    },
    [fullscreen, expand],
  );

  const openChat = useCallback(
    (chat: ChatRow) => push(() => callTool("whatsapp_find_messages", { remoteJid: chat.jid, limit: 60 })),
    [push],
  );
  const openContact = useCallback(
    (c: ContactRow) => push(() => callTool("whatsapp_fetch_profile", { number: c.Number || c.JID })),
    [push],
  );
  const openGroup = useCallback(
    (g: GroupRow) => push(() => callTool("whatsapp_group_info", { groupJid: g.jid })),
    [push],
  );
  const back = view?.back ? () => setView(view.back ?? null) : undefined;

  if (!view) {
    return (
      <div class="app">
        <div class="card">
          <CardHeader icon="chat" title="WhatsApp" subtitle="Carregando…" />
          <hr class="divider" />
          <SkeletonRows rows={3} />
        </div>
      </div>
    );
  }

  const p = view.payload;
  const common = { fullscreen, onExpand: expand };

  return (
    <div class="app">
      {error ? <div style={{ marginBottom: "8px" }}><ErrorNote message={error} /></div> : null}
      {p.kind === "chats" ? <ChatsCard data={p as ChatsPayload} {...common} onOpenChat={openChat} /> : null}
      {p.kind === "messages" ? <ThreadCard data={p as MessagesPayload} {...common} onBack={back} /> : null}
      {p.kind === "contacts" ? <ContactsCard data={p as ContactsPayload} {...common} onPick={openContact} /> : null}
      {p.kind === "contact" ? <ContactCard data={p as ContactPayload} /> : null}
      {p.kind === "groups" ? <GroupsCard data={p as GroupsPayload} {...common} onOpen={openGroup} /> : null}
      {p.kind === "group" ? <GroupCard data={p as GroupPayload} {...common} /> : null}
      {p.kind === "participants" ? <ParticipantsCard data={p as ParticipantsPayload} {...common} /> : null}
      {p.kind === "account" ? <AccountCard data={p} /> : null}
      {p.kind === "sent" ? <SentCard data={p} /> : null}
      {p.kind === "receipt" ? <ReceiptCard data={p} /> : null}
      {p.kind === "numbers" ? <NumbersCard data={p} /> : null}
      {p.kind === "media" ? <MediaCard data={p} /> : null}
      {p.kind === "poll" ? <PollCard data={p} /> : null}
      {p.kind === "location" ? <LocationCard data={p} /> : null}
      {p.kind === "privacy" ? <PrivacyCard data={p} /> : null}
      {p.kind === "business" ? <BusinessCard data={p} /> : null}
      {p.kind === "invite" ? <InviteCard data={p} /> : null}
      {p.kind === "devices" ? <DevicesCard data={p} /> : null}
    </div>
  );
}
