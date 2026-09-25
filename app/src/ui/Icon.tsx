/**
 * Monochrome outline icons that inherit `currentColor`, so they follow the host
 * text tokens in both themes. Icons support understanding; they never carry it
 * alone (every icon-only control has an aria-label).
 */
type Name =
  | "chat" | "group" | "megaphone" | "user" | "users" | "search" | "refresh"
  | "expand" | "check" | "check-double" | "clock" | "image" | "video" | "mic"
  | "doc" | "sticker" | "pin" | "poll" | "phone" | "mail" | "link" | "block"
  | "shield" | "store" | "send" | "reply" | "download" | "close" | "chevron-right"
  | "chevron-left" | "alert" | "info" | "globe" | "lock" | "eye" | "trash" | "edit";

const P: Record<Name, string> = {
  chat: "M21 11.5a8.4 8.4 0 0 1-9 8.4 9 9 0 0 1-3.6-.7L3 21l1.9-5A8.4 8.4 0 0 1 12 3.1a8.4 8.4 0 0 1 9 8.4Z",
  group: "M17 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9.5 7a3.5 3.5 0 1 1-7 0 3.5 3.5 0 0 1 7 0ZM22 21v-2a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8",
  megaphone: "M3 11v2a1 1 0 0 0 1 1h2l4 4V6L6 10H4a1 1 0 0 0-1 1ZM15 8.5a4 4 0 0 1 0 7M18 5.5a8 8 0 0 1 0 13",
  user: "M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2M16 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z",
  users: "M17 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9.5 7a3.5 3.5 0 1 1-7 0 3.5 3.5 0 0 1 7 0ZM22 21v-2a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8",
  search: "M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16ZM21 21l-4.3-4.3",
  refresh: "M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6",
  expand: "M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7",
  check: "M20 6 9 17l-5-5",
  "check-double": "M18 6 7 17l-4-4M22 10l-6.5 6.5",
  clock: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18ZM12 7v5l3 2",
  image: "M3 5h18v14H3zM8.5 11a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3ZM21 15l-5-5L5 19",
  video: "M3 6h12v12H3zM15 10l6-3v10l-6-3",
  mic: "M12 15a3 3 0 0 0 3-3V6a3 3 0 1 0-6 0v6a3 3 0 0 0 3 3ZM19 11a7 7 0 0 1-14 0M12 18v3",
  doc: "M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8zM14 3v5h5M9 13h6M9 17h6",
  sticker: "M15 21H7a4 4 0 0 1-4-4V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4v8zM14 21v-4a2 2 0 0 1 2-2h4",
  pin: "M12 21s7-6.3 7-11a7 7 0 1 0-14 0c0 4.7 7 11 7 11ZM12 12.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z",
  poll: "M6 20V10M12 20V4M18 20v-6",
  phone: "M21 16.9v2.6a1.7 1.7 0 0 1-1.9 1.7 17 17 0 0 1-7.4-2.6 16.6 16.6 0 0 1-5.1-5.1A17 17 0 0 1 4 6a1.7 1.7 0 0 1 1.7-1.9h2.6a1.7 1.7 0 0 1 1.7 1.5c.1.8.3 1.7.6 2.5a1.7 1.7 0 0 1-.4 1.8l-1.1 1.1a13.6 13.6 0 0 0 5.1 5.1l1.1-1.1a1.7 1.7 0 0 1 1.8-.4c.8.3 1.7.5 2.5.6a1.7 1.7 0 0 1 1.4 1.7Z",
  mail: "M3 6h18v12H3zM3 7l9 6 9-6",
  link: "M10 13a5 5 0 0 0 7.5.5l3-3A5 5 0 0 0 13.5 3.5l-1.7 1.7M14 11a5 5 0 0 0-7.5-.5l-3 3A5 5 0 0 0 10.5 20.5l1.7-1.7",
  block: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18ZM5.6 5.6l12.8 12.8",
  shield: "M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z",
  store: "M3 9h18l-1.5-5h-15zM4 9v11h16V9M9 20v-6h6v6",
  send: "M22 2 11 13M22 2l-7 20-4-9-9-4z",
  reply: "M9 17l-6-5 6-5M3 12h10a7 7 0 0 1 7 7v1",
  download: "M12 3v12M7 11l5 5 5-5M4 21h16",
  close: "M18 6 6 18M6 6l12 12",
  "chevron-right": "M9 18l6-6-6-6",
  "chevron-left": "M15 18l-6-6 6-6",
  alert: "M12 9v4M12 17h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z",
  info: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18ZM12 16v-4M12 8h.01",
  globe: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18ZM3 12h18M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18Z",
  lock: "M5 11h14v10H5zM8 11V7a4 4 0 1 1 8 0v4",
  eye: "M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7ZM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z",
  trash: "M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14M10 11v6M14 11v6",
  edit: "M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z",
};

export function Icon({ name, size = 16 }: { name: Name; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.75"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
      style={{ flex: "none" }}
    >
      {P[name].split("M").filter(Boolean).map((d, i) => (
        <path key={i} d={`M${d}`} />
      ))}
    </svg>
  );
}

export type IconName = Name;
