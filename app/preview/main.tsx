/**
 * Local preview harness — renders every card against fixtures, outside a host.
 *
 * Its whole point is to make the cards reviewable (light/dark, inline/fullscreen,
 * narrow/wide) WITHOUT a deploy cycle: the previous iteration of this work burned
 * days on "deploy → ask if it looks right". It is not shipped: the Go server only
 * embeds the app bundle.
 *
 * Host style variables are stubbed with Claude's documented light/dark values so
 * what you see here is what the host will paint.
 */
import { render } from "preact";
import { useState } from "preact/hooks";
import "../src/ui/tokens.css";
import "../src/ui/bubbles.css";
import { FIXTURES } from "./fixtures";
import type { Payload } from "../src/lib/types";
import { ChatsCard } from "../src/cards/ChatsCard";
import { ThreadCard } from "../src/cards/ThreadCard";
import { ContactsCard, ContactCard } from "../src/cards/ContactsCard";
import { GroupsCard, GroupCard, ParticipantsCard } from "../src/cards/GroupsCard";
import {
  AccountCard, BusinessCard, DevicesCard, InviteCard, LocationCard, MediaCard,
  NumbersCard, PollCard, PrivacyCard, ReceiptCard, SentCard,
} from "../src/cards/StatusCards";

/** Claude's documented token values, so the preview matches the real host. */
const LIGHT: Record<string, string> = {
  "--color-background-primary": "#FFFFFF", "--color-background-secondary": "#F5F4ED",
  "--color-background-tertiary": "#FAF9F5", "--color-background-inverse": "#141413",
  "--color-background-info": "#D6E4F6", "--color-background-danger": "#F7ECEC",
  "--color-background-success": "#E9F1DC", "--color-background-warning": "#F6EEDF",
  "--color-text-primary": "#141413", "--color-text-secondary": "#3D3D3A",
  "--color-text-tertiary": "#73726C", "--color-text-inverse": "#FFFFFF",
  "--color-text-info": "#3266AD", "--color-text-danger": "#7F2C28",
  "--color-text-success": "#265B19", "--color-text-warning": "#5A4815",
  "--color-border-primary": "rgba(31,30,29,0.40)", "--color-border-secondary": "rgba(31,30,29,0.30)",
  "--color-border-tertiary": "rgba(31,30,29,0.15)", "--color-ring-primary": "rgba(20,20,19,0.70)",
};
const DARK: Record<string, string> = {
  "--color-background-primary": "#30302E", "--color-background-secondary": "#262624",
  "--color-background-tertiary": "#141413", "--color-background-inverse": "#FAF9F5",
  "--color-background-info": "#253E5F", "--color-background-danger": "#602A28",
  "--color-background-success": "#1B4614", "--color-background-warning": "#483A0F",
  "--color-text-primary": "#FAF9F5", "--color-text-secondary": "#C2C0B6",
  "--color-text-tertiary": "#9C9A92", "--color-text-inverse": "#141413",
  "--color-text-info": "#80AADD", "--color-text-danger": "#EE8884",
  "--color-text-success": "#7AB948", "--color-text-warning": "#D1A041",
  "--color-border-primary": "rgba(222,220,209,0.40)", "--color-border-secondary": "rgba(222,220,209,0.30)",
  "--color-border-tertiary": "rgba(222,220,209,0.15)", "--color-ring-primary": "rgba(250,249,245,0.70)",
};

const ORDER = [
  "chats", "messages", "contacts", "contact", "groups", "group", "participants",
  "account", "sent", "receipt", "numbers", "media", "poll", "location",
  "privacy", "business", "invite", "devices",
] as const;

const TITLES: Record<string, string> = {
  chats: "Conversas (inline: 5 linhas + expandir)",
  messages: "Conversa (bolhas, mídia sob demanda)",
  contacts: "Contatos (busca server-side)",
  contact: "Contato (detalhe)",
  groups: "Grupos (carrossel inline)",
  group: "Grupo (detalhe + participantes)",
  participants: "Participantes",
  account: "Conta (estado + números)",
  sent: "Envio confirmado",
  receipt: "Recibo de ação",
  numbers: "Checagem de números",
  media: "Mídia baixada",
  poll: "Enquete enviada",
  location: "Localização enviada",
  privacy: "Privacidade",
  business: "Perfil business",
  invite: "Link de convite",
  devices: "Dispositivos",
};

function Card({ p, fullscreen }: { p: Payload; fullscreen: boolean }) {
  const noop = () => {};
  const common = { fullscreen, onExpand: noop };
  switch (p.kind) {
    case "chats": return <ChatsCard data={p} {...common} onOpenChat={noop} />;
    case "messages": return <ThreadCard data={p} {...common} />;
    case "contacts": return <ContactsCard data={p} {...common} onPick={noop} />;
    case "contact": return <ContactCard data={p} />;
    case "groups": return <GroupsCard data={p} {...common} onOpen={noop} />;
    case "group": return <GroupCard data={p} {...common} />;
    case "participants": return <ParticipantsCard data={p} {...common} />;
    case "account": return <AccountCard data={p} />;
    case "sent": return <SentCard data={p} />;
    case "receipt": return <ReceiptCard data={p} />;
    case "numbers": return <NumbersCard data={p} />;
    case "media": return <MediaCard data={p} />;
    case "poll": return <PollCard data={p} />;
    case "location": return <LocationCard data={p} />;
    case "privacy": return <PrivacyCard data={p} />;
    case "business": return <BusinessCard data={p} />;
    case "invite": return <InviteCard data={p} />;
    case "devices": return <DevicesCard data={p} />;
    default: return null;
  }
}

function Harness() {
  const [dark, setDark] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [narrow, setNarrow] = useState(false);
  const vars = dark ? DARK : LIGHT;
  const style = Object.entries(vars).map(([k, v]) => `${k}:${v}`).join(";");

  return (
    <div
      data-tokens
      style={`${style};color-scheme:${dark ? "dark" : "light"};background:${vars["--color-background-tertiary"]};min-height:100vh;padding:20px`}
    >
      <div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:20px;position:sticky;top:0;z-index:9;padding:10px;background:var(--color-background-primary);border-radius:10px;border:0.5px solid var(--color-border-tertiary)">
        <button class="btn-secondary" onClick={() => setDark(!dark)}>{dark ? "☾ escuro" : "☀ claro"}</button>
        <button class="btn-secondary" onClick={() => setFullscreen(!fullscreen)}>{fullscreen ? "tela cheia" : "inline"}</button>
        <button class="btn-secondary" onClick={() => setNarrow(!narrow)}>{narrow ? "375px (mobile)" : "largura total"}</button>
      </div>
      <div style="display:flex;flex-wrap:wrap;gap:20px;align-items:flex-start">
        {ORDER.map((k) => {
          const p = FIXTURES[k];
          if (!p) return null;
          return (
            <section key={k} style={`width:${narrow ? "375px" : "560px"};max-width:100%`}>
              <h2 style="font:600 12px var(--font-sans,system-ui);color:var(--color-text-tertiary);margin:0 0 6px;text-transform:uppercase;letter-spacing:.04em">
                {TITLES[k] ?? k}
              </h2>
              <Card p={p} fullscreen={fullscreen} />
            </section>
          );
        })}
      </div>
    </div>
  );
}

render(<Harness />, document.getElementById("root")!);
