import { ShieldOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Credential } from "@/lib/types";
import { cn } from "@/lib/utils";

/* The five (plus one fallback) states a credential can be in, evaluated in
 * strict precedence. `paused` deliberately exists as its own state: a channel
 * that tripped the circuit breaker after repeated upstream errors otherwise
 * looks identical to an idle healthy one — which is exactly how a silently
 * dead relay goes unnoticed. */
export type CredStatusKey = "disabled" | "quota" | "paused" | "hardFail" | "healthy" | "cooldown";

export interface CredStatus {
  key: CredStatusKey;
  /** i18n key for the short status word rendered on the card. */
  labelKey: string;
  /** Text colour class for the status word. */
  tone: string;
  /** Background colour class for the status dot. */
  dot: string;
  /** True only when the credential is actively serving traffic. */
  live: boolean;
}

const STATES: Record<CredStatusKey, Omit<CredStatus, "key">> = {
  disabled: {
    labelKey: "admin.creds.status.disabled",
    tone: "text-muted-foreground",
    dot: "bg-muted-foreground",
    live: false,
  },
  quota: {
    labelKey: "admin.creds.status.quota",
    tone: "text-warning",
    dot: "bg-warning",
    live: false,
  },
  paused: {
    labelKey: "admin.creds.status.paused",
    tone: "text-warning",
    dot: "bg-warning",
    live: false,
  },
  hardFail: {
    labelKey: "admin.creds.status.hardFail",
    tone: "text-destructive",
    dot: "bg-destructive",
    live: false,
  },
  healthy: {
    labelKey: "admin.creds.status.healthy",
    tone: "text-success",
    dot: "bg-success",
    live: true,
  },
  cooldown: {
    labelKey: "admin.creds.status.cooldown",
    tone: "text-warning",
    dot: "bg-warning",
    live: false,
  },
};

/* credStatus — the single source of truth for how a credential is rendered.
 * Every badge, dot, accent hairline and filter reads from here, so a state can
 * never be shown two different ways in two places. */
export function credStatus(c: Credential): CredStatus {
  const key: CredStatusKey = c.disabled
    ? "disabled"
    : c.quota_exceeded
      ? "quota"
      : c.quarantined_until
        ? "paused"
        : c.hard_failure
          ? "hardFail"
          : c.healthy
            ? "healthy"
            : "cooldown";
  return { key, ...STATES[key] };
}

/* StatusDot — a 2px dot that only pulses while the credential is actually
 * eligible for traffic. The animation is the fleet's ambient heartbeat; a
 * static dot reads as "parked" at a glance. */
export function StatusDot({ status, className }: { status: CredStatus; className?: string }) {
  return (
    <span className={cn("relative inline-flex h-2 w-2 shrink-0", className)}>
      {status.live && (
        <span
          aria-hidden
          className={cn(
            "absolute inline-flex h-full w-full animate-ping rounded-full opacity-60",
            status.dot,
          )}
        />
      )}
      <span className={cn("relative inline-flex h-2 w-2 rounded-full", status.dot)} />
    </span>
  );
}

/* Expiry — OAuth access-token expiry as a relative distance.
 *
 * The background refresher in cc-core auth.Pool.RefreshExpiring skips disabled
 * and hard-failed credentials. When that's the case an "expired 5d ago" reads
 * as neglect when it's actually deliberate, so render "frozen" instead and put
 * the real reason in the tooltip. */
export function Expiry({ cred }: { cred: Credential }) {
  const { t } = useTranslation();
  const at = cred?.expires_at;
  if (!at || at.startsWith("0001-")) return <span className="text-muted-foreground">—</span>;
  const d = new Date(at);
  const dt = d.getTime() - Date.now();
  const days = Math.round(dt / 86400000);
  const absolute = d.toLocaleString();
  const rel = dt < 0 ? `${-days}d` : days < 1 ? `${Math.round(dt / 3600000)}h` : `${days}d`;

  if (cred?.refresh_suspended) {
    const reason =
      cred.refresh_suspended_reason ||
      (cred.disabled ? t("admin.creds.alerts.frozenDisabled") : t("admin.creds.alerts.frozenHard"));
    return (
      <span
        className="inline-flex items-center gap-1 text-muted-foreground"
        title={`${absolute}\n${t("admin.creds.alerts.frozenTitle")} · ${reason}`}
      >
        <ShieldOff className="size-3 text-destructive/70" />
        <span>{t("admin.creds.facts.frozen")}</span>
      </span>
    );
  }

  const cls = dt < 0 ? "text-destructive" : days < 7 ? "text-warning" : "text-foreground";
  return (
    <span className={cls} title={absolute}>
      {dt < 0 ? t("admin.creds.facts.expiredAgo", { rel }) : rel}
    </span>
  );
}
