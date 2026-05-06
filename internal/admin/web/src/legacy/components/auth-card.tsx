import React from "react";
import type { AuthRow } from "@/legacy/lib/types";
import { Sparkline } from "./sparkline";
import { CardUpstreamQuota } from "./upstream-quota";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { GroupBadge } from "./group-badge";
import { cn, fmtDate, fmtInt, fmtUSD } from "@/legacy/lib/utils";
import {
  AlertTriangle,
  Ban,
  CheckCircle2,
  Pencil,
  Power,
  RefreshCw,
  ShieldOff,
  Trash2,
} from "lucide-react";

type Action = "toggle" | "refresh" | "clear-quota" | "clear-failure" | "delete";

interface Props {
  a: AuthRow;
  onAction: (a: AuthRow, act: Action) => void;
  onEdit: (a: AuthRow) => void;
}

function statusMeta(a: AuthRow) {
  if (a.disabled)
    return { label: "Disabled", tone: "text-muted-foreground", dot: "bg-muted-foreground" };
  if (a.quota_exceeded)
    return { label: "Quota", tone: "text-[color:var(--warning)]", dot: "bg-[color:var(--warning)]" };
  if (a.hard_failure)
    return { label: "Unhealthy", tone: "text-destructive", dot: "bg-destructive" };
  if (a.healthy)
    return { label: "Healthy", tone: "text-[color:var(--success)]", dot: "bg-[color:var(--success)]" };
  return { label: "Degraded", tone: "text-[color:var(--warning)]", dot: "bg-[color:var(--warning)]" };
}

export function AuthCard({ a, onAction, onEdit }: Props) {
  const slot =
    a.max_concurrent > 0 ? `${a.active_clients}/${a.max_concurrent}` : `${a.active_clients}/∞`;
  const slotRatio =
    a.max_concurrent > 0
      ? Math.min(100, Math.round((a.active_clients / a.max_concurrent) * 100))
      : 0;
  const status = statusMeta(a);
  const u = a.usage;
  const kindLabel = a.kind === "apikey" ? "API key" : "OAuth";
  const recentCancel =
    a.last_client_cancel && Date.now() - new Date(a.last_client_cancel).getTime() < 3600 * 1000;

  return (
    <article className="relative group bg-card border border-border-strong rounded-md overflow-hidden transition-all duration-300 hover:-translate-y-0.5 hover:shadow-xl hover:shadow-primary/5 hover:border-primary/40">
      {/* Thin accent bar that tinges on hover */}
      <div
        aria-hidden
        className={cn(
          "absolute inset-x-0 top-0 h-[2px] transition-all",
          a.healthy && !a.disabled && !a.quota_exceeded
            ? "bg-gradient-to-r from-transparent via-primary/40 to-transparent opacity-60 group-hover:opacity-100"
            : "bg-transparent",
        )}
      />

      <header className="px-5 py-4 border-b border-border flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2.5">
            <span className="relative inline-flex h-2 w-2 shrink-0">
              {!a.disabled && a.healthy && (
                <span
                  className={cn(
                    "absolute inline-flex h-full w-full rounded-full opacity-60 animate-ping",
                    status.dot,
                  )}
                />
              )}
              <span className={cn("relative inline-flex h-2 w-2 rounded-full", status.dot)} />
            </span>
            <h3 className="font-display text-xl leading-tight truncate">{a.label || a.id}</h3>
          </div>
          <div className="mt-1 mono text-[11px] text-muted-foreground truncate pl-4.5">{a.id}</div>
        </div>
        <div className="flex flex-col items-end gap-1 shrink-0">
          <Badge variant={a.provider === "openai" ? "violet" : "slate"} title="Upstream provider">
            {a.provider === "openai" ? "Codex" : "Claude"}
          </Badge>
          <Badge variant={a.kind === "apikey" ? "blue" : "slate"}>{kindLabel}</Badge>
          {a.plan_type && (
            <Badge variant="slate" title="ChatGPT subscription plan (from id_token)">
              {a.plan_type}
            </Badge>
          )}
          <GroupBadge group={a.group} />

          <span className={cn("eyebrow !text-[10px]", status.tone)}>{status.label}</span>
        </div>
      </header>

      {a.quota_exceeded && (
        <AlertStrip tone="warning" icon={<AlertTriangle className="h-3.5 w-3.5" />} label="Quota exceeded">
          {a.quota_reset_at ? `resets ${fmtDate(a.quota_reset_at)}` : "no reset time reported"}
        </AlertStrip>
      )}
      {!a.quota_exceeded && a.failure_reason && (
        <AlertStrip
          tone={a.hard_failure ? "error" : "warning"}
          icon={a.hard_failure ? <ShieldOff className="h-3.5 w-3.5" /> : <AlertTriangle className="h-3.5 w-3.5" />}
          label={a.hard_failure ? "Unhealthy" : "Recent failure"}
          title={a.failure_reason}
        >
          {a.failure_reason}
        </AlertStrip>
      )}
      {recentCancel && (
        <AlertStrip
          tone="muted"
          icon={<Ban className="h-3.5 w-3.5" />}
          label="Client canceled"
          title={
            fmtDate(a.last_client_cancel!) +
            (a.client_cancel_reason ? " · " + a.client_cancel_reason : "")
          }
        >
          {fmtDate(a.last_client_cancel!)}
          {a.client_cancel_reason ? " · " + a.client_cancel_reason : ""}
        </AlertStrip>
      )}

      <dl className="px-5 py-4 grid grid-cols-2 gap-x-6 gap-y-3.5 text-sm">
        <div className="relative group/slot">
          <dt className="eyebrow mb-1.5">Slots</dt>
          <dd className="mono font-medium">
            <div className="tabular text-base">{slot}</div>
            {a.max_concurrent > 0 && (
              <div className="mt-1.5 h-1 w-full max-w-[120px] bg-muted rounded-full overflow-hidden">
                <div
                  className="h-full transition-all"
                  style={{
                    width: `${slotRatio}%`,
                    background: slotRatio > 80 ? "var(--warning)" : "var(--success)",
                  }}
                />
              </div>
            )}
          </dd>
          {a.active_clients > 0 && a.client_tokens?.length > 0 && (
            <div className="pointer-events-none absolute left-0 top-full mt-2 z-20 min-w-[180px] max-w-[260px] opacity-0 translate-y-1 group-hover/slot:opacity-100 group-hover/slot:translate-y-0 transition duration-200 rounded-md border border-border-strong bg-popover shadow-xl px-3 py-2 text-xs">
              <div className="eyebrow mb-1">Active clients</div>
              <ul className="space-y-0.5">
                {a.client_tokens.map((t) => (
                  <li key={t} className="truncate mono">
                    {t}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
        <div>
          <dt className="eyebrow mb-1.5">Token exp</dt>
          <dd className="mono text-sm">
            {a.expires_at ? fmtDate(a.expires_at) : <span className="text-muted-foreground">—</span>}
          </dd>
        </div>
        {a.email && (
          <div className="col-span-2">
            <dt className="eyebrow mb-1.5">Email</dt>
            <dd className="text-sm truncate">{a.email}</dd>
          </div>
        )}
        <div className="col-span-2">
          <dt className="eyebrow mb-1.5">Proxy</dt>
          <dd className="mono text-[11px] break-all">
            {a.proxy_url || <span className="text-muted-foreground">direct</span>}
          </dd>
        </div>
        {a.base_url && (
          <div className="col-span-2">
            <dt className="eyebrow mb-1.5">Base URL</dt>
            <dd className="mono text-[11px] break-all">{a.base_url}</dd>
          </div>
        )}
        {a.model_map && Object.keys(a.model_map).length > 0 && (
          <div className="col-span-2">
            <dt className="eyebrow mb-1.5">Model map ({Object.keys(a.model_map).length})</dt>
            <dd className="mt-1 space-y-0.5">
              {Object.keys(a.model_map)
                .sort()
                .map((k) => (
                  <div key={k} className="mono text-[11px] break-all leading-relaxed">
                    <span>{k}</span>
                    {a.model_map![k] ? (
                      <>
                        <span className="text-muted-foreground"> → </span>
                        <span>{a.model_map![k]}</span>
                      </>
                    ) : (
                      <span className="text-muted-foreground"> (no rewrite)</span>
                    )}
                  </div>
                ))}
            </dd>
          </div>
        )}
      </dl>

      {u && (
        <div className="px-5 py-4 bg-muted/30 border-y border-border">
          <div className="grid grid-cols-3 gap-4 text-sm">
            <div>
              <div className="eyebrow mb-1.5">24h in/out</div>
              <div className="mono tabular font-medium">
                {fmtInt(u.sum_24h.input_tokens)} / {fmtInt(u.sum_24h.output_tokens)}
              </div>
            </div>
            <div>
              <div className="eyebrow mb-1.5">Total req</div>
              <div className="mono tabular font-medium">
                {fmtInt(u.total.requests)}
                {u.total.errors && u.total.errors > 0 ? (
                  <span className="text-destructive text-xs ml-1">
                    ({fmtInt(u.total.errors)})
                  </span>
                ) : null}
              </div>
              <div className="eyebrow mt-2 mb-0.5">Total cost</div>
              <div className="mono tabular text-xs font-medium">
                {fmtUSD(u.total_cost_usd)}
              </div>
            </div>
            <div>
              <div className="eyebrow mb-1.5">14-day</div>
              <div>
                {u.daily && u.daily.length > 0 ? (
                  <Sparkline daily={u.daily} />
                ) : (
                  <span className="text-muted-foreground text-xs mono">no data</span>
                )}
              </div>
            </div>
          </div>
        </div>
      )}

      {a.kind === "oauth" && a.provider === "anthropic" && <CardUpstreamQuota auth={a} />}
      {a.kind === "oauth" && a.provider === "openai" && u && (
        <div className="px-5 py-3 border-t border-border bg-muted/20">
          <div className="flex items-center justify-between gap-2">
            <div className="eyebrow">Rolling 5h (local)</div>
            <div className="mono tabular text-xs">
              in {fmtInt(u.sum_5h.input_tokens)} · out {fmtInt(u.sum_5h.output_tokens)}
              {u.sum_5h.cache_read_tokens ? (
                <span className="text-muted-foreground"> · cache {fmtInt(u.sum_5h.cache_read_tokens)}</span>
              ) : null}
            </div>
          </div>
          <div className="mt-1 text-[10px] text-muted-foreground leading-snug">
            Local counter from our own request log. Backend-reported quota, when available,
            appears below.
          </div>
        </div>
      )}

      {a.kind === "oauth" && a.provider === "openai" && a.codex_rate_limits && (
        <CodexRateLimitPanel
          limits={a.codex_rate_limits}
          capturedAt={a.codex_rate_limits_at}
        />
      )}

      <footer className="px-5 py-3 flex gap-1.5 flex-wrap">
        {a.kind === "oauth" && (
          <>
            <Button size="sm" variant="outline" onClick={() => onEdit(a)}>
              <Pencil className="h-3 w-3" /> Edit
            </Button>
            <Button size="sm" variant="outline" onClick={() => onAction(a, "toggle")}>
              <Power className="h-3 w-3" />
              {a.disabled ? "Enable" : "Disable"}
            </Button>
            <Button size="sm" variant="outline" onClick={() => onAction(a, "refresh")}>
              <RefreshCw className="h-3 w-3" /> Refresh
            </Button>
            {a.quota_exceeded && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-quota")}>
                Clear quota
              </Button>
            )}
            {(a.hard_failure || (!a.healthy && !a.quota_exceeded && !a.disabled)) && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-failure")}>
                <CheckCircle2 className="h-3 w-3" /> Mark healthy
              </Button>
            )}
            <Button
              size="sm"
              variant="outline"
              className="ml-auto border-destructive/40 text-destructive hover:bg-destructive/10"
              onClick={() => onAction(a, "delete")}
            >
              <Trash2 className="h-3 w-3" />
            </Button>
          </>
        )}
        {a.kind === "apikey" && (
          <>
            {a.file_backed && (
              <>
                <Button size="sm" variant="outline" onClick={() => onEdit(a)}>
                  <Pencil className="h-3 w-3" /> Edit
                </Button>
                <Button size="sm" variant="outline" onClick={() => onAction(a, "toggle")}>
                  <Power className="h-3 w-3" />
                  {a.disabled ? "Enable" : "Disable"}
                </Button>
              </>
            )}
            {a.quota_exceeded && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-quota")}>
                Clear quota
              </Button>
            )}
            {(a.hard_failure || (!a.healthy && !a.quota_exceeded && !a.disabled)) && (
              <Button size="sm" variant="warning" onClick={() => onAction(a, "clear-failure")}>
                <CheckCircle2 className="h-3 w-3" /> Mark healthy
              </Button>
            )}
            {a.file_backed && (
              <Button
                size="sm"
                variant="outline"
                className="ml-auto border-destructive/40 text-destructive hover:bg-destructive/10"
                onClick={() => onAction(a, "delete")}
              >
                <Trash2 className="h-3 w-3" />
              </Button>
            )}
          </>
        )}
      </footer>
    </article>
  );
}

function AlertStrip({
  tone,
  icon,
  label,
  title,
  children,
}: {
  tone: "warning" | "error" | "muted";
  icon: React.ReactNode;
  label: string;
  title?: string;
  children: React.ReactNode;
}) {
  const tones = {
    warning:
      "bg-[color:var(--warning)]/10 text-[color:var(--warning)] border-[color:var(--warning)]/25",
    error: "bg-destructive/10 text-destructive border-destructive/25",
    muted: "bg-muted text-muted-foreground border-border",
  };
  return (
    <div
      className={cn(
        "px-5 py-2.5 border-b flex items-center gap-3 text-xs cursor-help",
        tones[tone],
      )}
      title={title}
    >
      <span className="shrink-0">{icon}</span>
      <span className="eyebrow !tracking-wider">{label}</span>
      <span className="mono truncate text-[11px] opacity-90 ml-auto max-w-[60%] text-right">
        {children}
      </span>
    </div>
  );
}

// CodexRateLimitPanel renders the latest x-codex-* headers captured from
// chatgpt.com responses. The backend's field set changes over time and some
// are only present on a subset of responses, so this component stays loose:
// it highlights the few fields we recognize (primary/secondary used-percent
// + window-resets) and dumps the rest in a small key/value table.
function CodexRateLimitPanel({
  limits,
  capturedAt,
}: {
  limits: Record<string, string>;
  capturedAt?: string;
}) {
  // Known keys we promote to the prominent row. Matches what the Codex CLI
  // actually displays to users.
  const primaryUsed = pct(limits["x-codex-primary-used-percent"]);
  const secondaryUsed = pct(limits["x-codex-secondary-used-percent"]);
  const primaryReset =
    limits["x-codex-primary-reset-after-seconds"] ||
    limits["x-codex-primary-window-expires-at-iso"];
  const secondaryReset =
    limits["x-codex-secondary-reset-after-seconds"] ||
    limits["x-codex-secondary-window-expires-at-iso"];
  const known = new Set([
    "x-codex-primary-used-percent",
    "x-codex-secondary-used-percent",
    "x-codex-primary-reset-after-seconds",
    "x-codex-secondary-reset-after-seconds",
    "x-codex-primary-window-expires-at-iso",
    "x-codex-secondary-window-expires-at-iso",
  ]);
  const others = Object.entries(limits).filter(([k]) => !known.has(k));
  return (
    <div className="px-5 py-3 border-t border-border bg-muted/20">
      <div className="flex items-center justify-between gap-2">
        <div className="eyebrow">Backend quota (x-codex)</div>
        {capturedAt && (
          <div className="text-[10px] text-muted-foreground mono">
            as of {new Date(capturedAt).toLocaleTimeString()}
          </div>
        )}
      </div>
      <div className="mt-2 grid grid-cols-2 gap-2 text-xs">
        <QuotaBar label="Primary (5h)" percent={primaryUsed} reset={primaryReset} />
        <QuotaBar label="Secondary (weekly)" percent={secondaryUsed} reset={secondaryReset} />
      </div>
      {others.length > 0 && (
        <details className="mt-2 text-[10px]">
          <summary className="cursor-pointer text-muted-foreground">
            other headers ({others.length})
          </summary>
          <div className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 mono">
            {others.map(([k, v]) => (
              <React.Fragment key={k}>
                <span className="text-muted-foreground">{k.replace(/^x-codex-/, "")}</span>
                <span className="truncate">{v}</span>
              </React.Fragment>
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

function pct(raw?: string): number | null {
  if (!raw) return null;
  const n = parseFloat(raw);
  return Number.isFinite(n) ? n : null;
}

function QuotaBar({
  label,
  percent,
  reset,
}: {
  label: string;
  percent: number | null;
  reset?: string;
}) {
  if (percent === null) {
    return (
      <div>
        <div className="text-muted-foreground">{label}</div>
        <div className="mono text-[10px] text-muted-foreground">—</div>
      </div>
    );
  }
  const pctDisplay = percent.toFixed(1);
  const tone =
    percent >= 90 ? "bg-destructive" : percent >= 70 ? "bg-[color:var(--warning)]" : "bg-primary";
  return (
    <div>
      <div className="flex items-baseline justify-between">
        <span className="text-muted-foreground">{label}</span>
        <span className="mono tabular">{pctDisplay}%</span>
      </div>
      <div className="h-1.5 mt-0.5 rounded-full bg-muted overflow-hidden">
        <div className={cn("h-full", tone)} style={{ width: `${Math.min(100, percent)}%` }} />
      </div>
      {reset && (
        <div className="text-[10px] text-muted-foreground mono mt-0.5">
          resets {formatReset(reset)}
        </div>
      )}
    </div>
  );
}

function formatReset(raw: string): string {
  // ISO timestamp → relative time; seconds → relative seconds
  if (/^\d+$/.test(raw)) {
    const s = parseInt(raw, 10);
    if (s < 60) return `in ${s}s`;
    if (s < 3600) return `in ${Math.round(s / 60)}m`;
    if (s < 86400) return `in ${Math.round(s / 3600)}h`;
    return `in ${Math.round(s / 86400)}d`;
  }
  const d = new Date(raw);
  if (Number.isNaN(d.getTime())) return raw;
  const delta = (d.getTime() - Date.now()) / 1000;
  if (delta < 0) return "now";
  if (delta < 3600) return `in ${Math.round(delta / 60)}m`;
  if (delta < 86400) return `in ${Math.round(delta / 3600)}h`;
  return `in ${Math.round(delta / 86400)}d`;
}
