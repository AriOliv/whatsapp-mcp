/**
 * The wire contract between the Go server and this app.
 *
 * Every tool that has a UI returns `structuredContent` carrying a `kind`
 * discriminator; the app switches on it to pick a card. Keep these in sync with
 * internal/mcpserver/view.go — that file is the source of truth.
 */

export type Kind =
  | "chats" | "messages" | "contacts" | "contact" | "groups" | "group"
  | "participants" | "numbers" | "media" | "sent" | "receipt" | "account"
  | "privacy" | "business" | "poll" | "location" | "invite" | "devices";

export interface ChatRow {
  jid: string;
  name: string;
  last_ts_millis: number;
  kind?: "dm" | "group" | "newsletter" | "status";
  preview?: string;
  media_type?: string;
  from_me?: boolean;
}

export interface MessageRow {
  id: string;
  chat_jid: string;
  sender_jid: string;
  sender_name?: string;
  from_me: boolean;
  ts_millis: number;
  body: string;
  media_type?: string;
  has_media?: boolean;
}

export interface ContactRow {
  JID: string;
  Number: string;
  Found?: boolean;
  FirstName?: string;
  FullName?: string;
  PushName?: string;
  BusinessName?: string;
  RedactedPhone?: string;
}

export interface GroupRow {
  jid: string;
  name: string;
  topic?: string;
  participant_count: number;
  is_announce?: boolean;
  is_locked?: boolean;
  is_ephemeral?: boolean;
  is_community?: boolean;
  created?: string;
  am_admin?: boolean;
}

export interface ParticipantRow {
  jid: string;
  name?: string;
  number?: string;
  is_admin?: boolean;
  is_super_admin?: boolean;
}

export interface NumberRow {
  query: string;
  jid?: string;
  registered: boolean;
  business_name?: string;
}

/* ------------------------------------------------------------- payloads -- */

export interface ChatsPayload {
  kind: "chats";
  chats: ChatRow[];
  total?: number;
  account?: AccountSummary;
}

export interface MessagesPayload {
  kind: "messages";
  chat: { jid: string; name: string; kind?: ChatRow["kind"]; participant_count?: number };
  messages: MessageRow[];
  has_more?: boolean;
}

export interface ContactsPayload {
  kind: "contacts";
  contacts: ContactRow[];
  total: number;
  shown: number;
  query?: string;
}

export interface ContactPayload {
  kind: "contact";
  contact: ContactRow;
  status?: string;
  picture_url?: string;
  is_business?: boolean;
}

export interface GroupsPayload {
  kind: "groups";
  groups: GroupRow[];
  total: number;
  shown: number;
  query?: string;
}

export interface GroupPayload {
  kind: "group";
  group: GroupRow;
  participants?: ParticipantRow[];
  invite_link?: string;
}

export interface ParticipantsPayload {
  kind: "participants";
  group: { jid: string; name?: string };
  participants: ParticipantRow[];
}

export interface NumbersPayload {
  kind: "numbers";
  numbers: NumberRow[];
}

export interface MediaPayload {
  kind: "media";
  mimetype: string;
  filename: string;
  bytes: number;
  data_base64: string;
  message_id?: string;
}

export interface SentPayload {
  kind: "sent";
  id: string;
  to: string;
  to_name?: string;
  what: "text" | "media" | "audio" | "sticker" | "location" | "contact" | "poll" | "reaction" | "edit" | "delete";
  preview?: string;
  ts_millis?: number;
}

export interface ReceiptPayload {
  kind: "receipt";
  ok: boolean;
  action: string;
  detail?: string;
  target?: string;
}

export interface AccountSummary {
  jid: string;
  number?: string;
  connected: boolean;
  logged_in: boolean;
  push_name?: string;
}

export interface AccountPayload {
  kind: "account";
  account: AccountSummary;
  chats?: number;
  contacts?: number;
  groups?: number;
}

export interface PrivacyPayload {
  kind: "privacy";
  settings: Record<string, string>;
}

export interface BusinessPayload {
  kind: "business";
  jid: string;
  name?: string;
  address?: string;
  email?: string;
  categories?: string[];
  hours?: Array<{ day: string; mode: string; open?: string; close?: string }>;
  timezone?: string;
}

export interface PollPayload {
  kind: "poll";
  id: string;
  to: string;
  to_name?: string;
  name: string;
  options: string[];
  selectable: number;
}

export interface LocationPayload {
  kind: "location";
  id: string;
  to: string;
  to_name?: string;
  latitude: number;
  longitude: number;
  name?: string;
  address?: string;
}

export interface InvitePayload {
  kind: "invite";
  group_jid: string;
  group_name?: string;
  invite_link: string;
}

export interface DevicesPayload {
  kind: "devices";
  devices: string[];
}

export type Payload =
  | ChatsPayload | MessagesPayload | ContactsPayload | ContactPayload
  | GroupsPayload | GroupPayload | ParticipantsPayload | NumbersPayload
  | MediaPayload | SentPayload | ReceiptPayload | AccountPayload
  | PrivacyPayload | BusinessPayload | PollPayload | LocationPayload
  | InvitePayload | DevicesPayload;

/** Narrow an unknown structuredContent to a Payload (unknown kinds render raw). */
export function asPayload(data: Record<string, unknown>): Payload | undefined {
  const kind = data?.["kind"];
  return typeof kind === "string" ? (data as unknown as Payload) : undefined;
}

/** Display name for a chat/contact, never falling back to a bare JID. */
export function displayName(row: { name?: string; jid?: string }): string {
  if (row.name) return row.name;
  const user = String(row.jid ?? "").split("@")[0] ?? "";
  return user ? `+${user}` : "Sem nome";
}

export function contactName(c: ContactRow): string {
  return c.FullName || c.PushName || c.BusinessName || c.RedactedPhone || (c.Number ? `+${c.Number}` : c.JID);
}
