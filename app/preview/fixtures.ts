/** Realistic payloads for the preview harness — same shapes the Go server emits. */
import type { Payload } from "../src/lib/types";

const now = Date.now();
const min = 60_000;

export const FIXTURES: Record<string, Payload> = {
  chats: {
    kind: "chats",
    total: 6,
    account: { jid: "5521968865678:12@s.whatsapp.net", number: "5521968865678", connected: true, logged_in: true, push_name: "Ari" },
    chats: [
      { jid: "120363041@g.us", name: "Avenia Team", last_ts_millis: now - 3 * min, kind: "group", preview: "fechei o deploy do temporal, tudo verde", from_me: false },
      { jid: "5521968865678@s.whatsapp.net", name: "A Princesa", last_ts_millis: now - 42 * min, kind: "dm", media_type: "image", preview: "", from_me: true },
      { jid: "120363999@g.us", name: "Woovi <> Avenia/BRLA", last_ts_millis: now - 5 * 60 * min, kind: "group", preview: "podemos marcar quinta 10h?", from_me: false },
      { jid: "558596269540@lid", name: "Joel Jucá", last_ts_millis: now - 26 * 60 * min, kind: "dm", preview: "valeu! já testei aqui e funcionou", from_me: false },
      { jid: "120363777@newsletter", name: "InfoMoney | Notícias sobre economia", last_ts_millis: now - 50 * 60 * min, kind: "newsletter", preview: "Dólar fecha em queda após dados de emprego", from_me: false },
      { jid: "status@broadcast", name: "Status", last_ts_millis: now - 70 * 60 * min, kind: "status", preview: "", from_me: false },
    ],
  },

  messages: {
    kind: "messages",
    chat: { jid: "120363041@g.us", name: "Avenia Team", kind: "group", participant_count: 14 },
    has_more: true,
    messages: [
      { id: "m5", chat_jid: "120363041@g.us", sender_jid: "", from_me: true, ts_millis: now - 2 * min, body: "boa! vou subir o card novo então" },
      { id: "m4", chat_jid: "120363041@g.us", sender_jid: "x@s.whatsapp.net", sender_name: "Bruno Betenson", from_me: false, ts_millis: now - 4 * min, body: "fechei o deploy do temporal, tudo verde" },
      { id: "m3", chat_jid: "120363041@g.us", sender_jid: "y@s.whatsapp.net", sender_name: "Caio", from_me: false, ts_millis: now - 9 * min, body: "", media_type: "audio", has_media: true },
      { id: "m2", chat_jid: "120363041@g.us", sender_jid: "y@s.whatsapp.net", sender_name: "Caio", from_me: false, ts_millis: now - 12 * min, body: "olha o gráfico de ontem", media_type: "image", has_media: true },
      { id: "m1", chat_jid: "120363041@g.us", sender_jid: "", from_me: true, ts_millis: now - 30 * min, body: "alguém revisou o PR do whatsapp-mcp?" },
    ],
  },

  contacts: {
    kind: "contacts", total: 18258, shown: 5, query: "",
    contacts: [
      { JID: "5521968865678@s.whatsapp.net", Number: "5521968865678", Found: true, FullName: "A Princesa", PushName: "Bia" },
      { JID: "558596269540@lid", Number: "558596269540", Found: true, FullName: "Joel Jucá" },
      { JID: "5511999998888@s.whatsapp.net", Number: "5511999998888", Found: true, FullName: "Woovi Suporte", BusinessName: "Woovi" },
      { JID: "5521988887777@s.whatsapp.net", Number: "5521988887777", Found: true, PushName: "Bruno Betenson" },
      { JID: "999123@lid", Number: "", Found: true, FullName: "Contato Privado", RedactedPhone: "+55 21 ••••-5678" },
    ],
  },

  contact: {
    kind: "contact", is_business: false, status: "Disponível · trabalhando remoto",
    contact: { JID: "5521968865678@s.whatsapp.net", Number: "5521968865678", Found: true, FullName: "A Princesa", PushName: "Bia" },
  },

  groups: {
    kind: "groups", total: 359, shown: 4, query: "",
    groups: [
      { jid: "120363041@g.us", name: "Avenia Team", topic: "Coordenação geral do time de produto e engenharia", participant_count: 14, am_admin: true },
      { jid: "120363999@g.us", name: "Woovi <> Avenia/BRLA", topic: "Integração PIX e settlement", participant_count: 8 },
      { jid: "120363555@g.us", name: "Avisos Avenia", topic: "Somente admins publicam", participant_count: 132, is_announce: true },
      { jid: "120363222@g.us", name: "Comunidade Dev", participant_count: 47, is_community: true, is_ephemeral: true },
    ],
  },

  group: {
    kind: "group",
    group: { jid: "120363041@g.us", name: "Avenia Team", topic: "Coordenação geral do time de produto e engenharia", participant_count: 14, am_admin: true, created: "2024-03-11" },
    participants: [
      { jid: "5521968865678@s.whatsapp.net", name: "Ari Oliveira", number: "5521968865678", is_super_admin: true },
      { jid: "5521988887777@s.whatsapp.net", name: "Bruno Betenson", number: "5521988887777", is_admin: true },
      { jid: "5511999996666@s.whatsapp.net", name: "Caio", number: "5511999996666" },
      { jid: "5511999995555@s.whatsapp.net", name: "Marina", number: "5511999995555" },
      { jid: "5511999994444@s.whatsapp.net", name: "Rafael", number: "5511999994444" },
    ],
  },

  participants: {
    kind: "participants",
    group: { jid: "120363041@g.us", name: "Avenia Team" },
    participants: [
      { jid: "5521968865678@s.whatsapp.net", name: "Ari Oliveira", number: "5521968865678", is_super_admin: true },
      { jid: "5521988887777@s.whatsapp.net", name: "Bruno Betenson", number: "5521988887777", is_admin: true },
      { jid: "5511999996666@s.whatsapp.net", name: "Caio", number: "5511999996666" },
    ],
  },

  account: {
    kind: "account",
    account: { jid: "5521968865678:12@s.whatsapp.net", number: "5521968865678", connected: true, logged_in: true, push_name: "Ari Oliveira" },
    chats: 412, contacts: 18258, groups: 359,
  },

  sent: { kind: "sent", id: "3EB0", to: "5521968865678@s.whatsapp.net", to_name: "A Princesa", what: "text", preview: "cheguei! já subo o card novo", ts_millis: now },
  receipt: { kind: "receipt", ok: true, action: "Mensagens marcadas como lidas", detail: "12 mensagens", target: "Avenia Team" },
  numbers: {
    kind: "numbers",
    numbers: [
      { query: "5521968865678", jid: "5521968865678@s.whatsapp.net", registered: true },
      { query: "5511999998888", jid: "5511999998888@s.whatsapp.net", registered: true, business_name: "Woovi" },
      { query: "5511000000000", registered: false },
    ],
  },
  poll: { kind: "poll", id: "3EB1", to: "120363041@g.us", to_name: "Avenia Team", name: "Onde fazemos o offsite?", options: ["Rio de Janeiro", "São Paulo", "Florianópolis"], selectable: 1 },
  location: { kind: "location", id: "3EB2", to: "5521968865678@s.whatsapp.net", to_name: "A Princesa", latitude: -22.9711, longitude: -43.1822, name: "Praia de Ipanema", address: "Ipanema, Rio de Janeiro - RJ" },
  privacy: { kind: "privacy", settings: { group_add: "contacts", last_seen: "contacts", status: "contacts", profile: "all", read_receipts: "all", online: "match_last_seen", call_add: "known", messages: "all" } },
  business: { kind: "business", jid: "5511999998888@s.whatsapp.net", name: "Woovi", address: "Av. Paulista, 1000 — São Paulo", email: "suporte@woovi.com", categories: ["Serviços financeiros"], timezone: "America/Sao_Paulo", hours: [{ day: "seg", mode: "open", open: "09:00", close: "18:00" }, { day: "sáb", mode: "closed" }] },
  invite: { kind: "invite", group_jid: "120363041@g.us", group_name: "Avenia Team", invite_link: "https://chat.whatsapp.com/EXEMPLOexemploEXEMPLO" },
  devices: { kind: "devices", devices: ["5521968865678:12@s.whatsapp.net", "5521968865678:31@s.whatsapp.net"] },
  media: { kind: "media", mimetype: "image/png", filename: "grafico.png", bytes: 184320, message_id: "m2", data_base64: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==" },
};
