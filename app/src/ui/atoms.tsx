/** Shared presentational atoms: avatar, badge, states. No host calls here. */
import type { ComponentChildren } from "preact";
import { Icon, type IconName } from "./Icon";

/** Deterministic initials from a display name (never the raw JID digits). */
export function initials(name: string): string {
  const parts = String(name ?? "")
    .trim()
    .split(/\s+/)
    .filter((p) => /\p{L}/u.test(p));
  if (!parts.length) return "";
  const first = parts[0]?.[0] ?? "";
  const last = parts.length > 1 ? parts[parts.length - 1]?.[0] ?? "" : "";
  return (first + last).toUpperCase();
}

export function Avatar({
  name,
  url,
  size = "md",
  icon,
}: {
  name?: string;
  url?: string;
  size?: "sm" | "md" | "lg";
  icon?: IconName;
}) {
  const cls = `avatar${size === "sm" ? " avatar-sm" : size === "lg" ? " avatar-lg" : ""}`;
  if (url) return <img class={cls} src={url} alt="" loading="lazy" />;
  const text = initials(name ?? "");
  return (
    <div class={cls} aria-hidden="true">
      {text || <Icon name={icon ?? "user"} size={size === "lg" ? 22 : size === "sm" ? 13 : 17} />}
    </div>
  );
}

export function Badge({
  tone = "neutral",
  icon,
  children,
}: {
  tone?: "neutral" | "ok" | "warn" | "danger" | "info" | "accent";
  icon?: IconName;
  children: ComponentChildren;
}) {
  return (
    <span class={`badge badge-${tone}`}>
      {icon ? <Icon name={icon} size={11} /> : null}
      {children}
    </span>
  );
}

/** Skeleton that mirrors a list row, so the swap to real content is seamless. */
export function SkeletonRows({ rows = 4, avatar = true }: { rows?: number; avatar?: boolean }) {
  return (
    <div aria-busy="true" aria-live="polite">
      <span class="sr-only">Carregando…</span>
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} class="item" style={{ pointerEvents: "none" }}>
          {avatar ? <div class="avatar skel" style={{ animationDelay: `${i * 90}ms` }} /> : null}
          <div class="grow stack" style={{ gap: "7px" }}>
            <div class="skel" style={{ width: `${58 - i * 6}%`, animationDelay: `${i * 90}ms` }} />
            <div class="skel" style={{ width: `${82 - i * 7}%`, height: "10px", animationDelay: `${i * 90}ms` }} />
          </div>
        </div>
      ))}
    </div>
  );
}

export function Empty({ icon = "info", title, hint }: { icon?: IconName; title: string; hint?: string }) {
  return (
    <div class="empty">
      <Icon name={icon} size={22} />
      <div class="body strong">{title}</div>
      {hint ? <div class="cap">{hint}</div> : null}
    </div>
  );
}

export function ErrorNote({ message }: { message: string }) {
  return (
    <div class="error" role="alert">
      <Icon name="alert" size={16} />
      <div class="grow">{message}</div>
    </div>
  );
}

/** Card header: title, optional subtitle/count and trailing actions. */
export function CardHeader({
  icon,
  title,
  subtitle,
  actions,
}: {
  icon?: IconName;
  title: string;
  subtitle?: string;
  actions?: ComponentChildren;
}) {
  return (
    <div class="row-between card-pad" style={{ paddingBottom: "10px" }}>
      <div class="row grow">
        {icon ? (
          <span style={{ color: "var(--accent)", display: "flex" }}>
            <Icon name={icon} size={18} />
          </span>
        ) : null}
        <div class="grow" style={{ minWidth: 0 }}>
          <h1 class="h truncate">{title}</h1>
          {subtitle ? <div class="cap truncate">{subtitle}</div> : null}
        </div>
      </div>
      {actions ? <div class="row" style={{ gap: "2px", flex: "none" }}>{actions}</div> : null}
    </div>
  );
}

export function IconButton({
  icon,
  label,
  onClick,
  disabled,
}: {
  icon: IconName;
  label: string;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button class="btn-icon" onClick={onClick} disabled={disabled} aria-label={label} title={label}>
      <Icon name={icon} size={16} />
    </button>
  );
}
