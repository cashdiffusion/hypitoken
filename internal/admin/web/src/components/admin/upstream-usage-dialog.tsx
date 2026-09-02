import { Gauge, RefreshCw, Zap } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { apiPost } from "@/lib/api";
import type { AllotmentEstimate } from "@/lib/types";
import { cn, errMsg, fmtCompact, fmtHours, fmtUSD } from "@/lib/utils";

// ---- Anthropic shape (api.anthropic.com/api/oauth/usage) ----
interface UsageWindow {
  utilization?: number;
  resets_at?: string;
}

interface AnthropicResponse {
  usage?: {
    status?: number;
    error?: string;
    body?: {
      five_hour?: UsageWindow;
      seven_day?: UsageWindow;
      seven_day_oauth_apps?: UsageWindow;
      seven_day_opus?: UsageWindow;
      seven_day_sonnet?: UsageWindow;
      seven_day_cowork?: UsageWindow;
      iguana_necktie?: UsageWindow;
      // Newer authoritative structure. Fable's independent weekly allotment
      // (~50% of weekly) is a weekly_scoped entry scoped to the Fable model —
      // NOT a top-level window.
      limits?: Array<{
        kind?: string;
        group?: string;
        percent?: number; // already 0-100
        severity?: string;
        resets_at?: string | null;
        is_active?: boolean;
        scope?: {
          model?: { id?: string | null; display_name?: string | null };
          surface?: string | null;
        } | null;
      }>;
    };
  };
  // Server-side estimate of what each window is worth (cc-core/quotaestimate).
  allotment_estimates?: AllotmentEstimate[];
  profile?: {
    body?: {
      account?: {
        email_address?: string;
        email?: string;
        has_claude_max?: boolean;
        has_claude_pro?: boolean;
      };
      organization?: {
        rate_limit_tier?: string;
      };
    };
  };
}

// ---- Codex shape (chatgpt.com/backend-api/wham/usage) ----
interface CodexUsageRateWindow {
  used_percent?: number;
  limit_window_seconds?: number; // 18000 = 5h, 604800 = 7d, …
  reset_after_seconds?: number; // relative countdown — present even when not limited
  reset_at?: number; // absolute unix seconds — often 0 until the limit is reached
}

interface CodexRateLimit {
  allowed?: boolean;
  limit_reached?: boolean;
  primary_window?: CodexUsageRateWindow;
  secondary_window?: CodexUsageRateWindow;
}

// additional_rate_limits[] carries per-feature limiters (each with its own
// primary/secondary windows). Some plans surface the weekly window here.
interface CodexAdditionalRateLimit {
  limit_name?: string;
  metered_feature?: string;
  rate_limit?: CodexRateLimit;
}

interface CodexUsageResponse {
  // Server-side estimate of what each wham/usage window is worth
  // (cc-core/quotaestimate).
  allotment_estimates?: AllotmentEstimate[];
  usage?: {
    user_id?: string;
    account_id?: string;
    email?: string;
    plan_type?: string;
    updated?: string;
    rate_limit?: CodexRateLimit;
    additional_rate_limits?: CodexAdditionalRateLimit[];
    rate_limit_reset_credits?: { available_count?: number };
    credits?: {
      has_credits?: boolean;
      unlimited?: boolean;
      overage_limit_reached?: boolean;
      balance?: string;
      approx_local_messages?: number[];
      approx_cloud_messages?: number[];
    };
    spend_control?: {
      reached?: boolean;
      individual_limit?: number | null;
    };
    // The wham/usage backend returns this as a bare string ("primary"),
    // null, or (newer) an object — keep it permissive so a shape change
    // can't break rendering.
    rate_limit_reached_type?: string | Record<string, unknown> | null;
  };
}

// Response of POST /credentials/:id/reset-codex-credit. usage is the refreshed
// wham/usage snapshot taken right after the redeem (absent if that follow-up
// probe failed — the reset itself still succeeded).
interface CodexResetResponse {
  reset?: {
    code?: string;
    windows_reset?: number;
    credit?: {
      reset_type?: string;
      status?: string;
      redeemed_at?: string;
      expires_at?: string;
    };
  };
  usage?: CodexUsageResponse["usage"];
}

// fmtWindowSeconds turns a limit_window_seconds value into a human label
// ("5h", "7d", "30d") so each window is named by its real duration rather than
// a hard-coded "primary/secondary" guess.
function fmtWindowSeconds(s?: number): string | null {
  if (!s || s <= 0) return null;
  if (s % 86400 === 0) return `${s / 86400}d`;
  if (s % 3600 === 0) return `${s / 3600}h`;
  if (s % 60 === 0) return `${s / 60}m`;
  return `${s}s`;
}

// codexReset renders a window's reset countdown. Prefers reset_after_seconds
// (relative — populated even when the window isn't exhausted) and falls back to
// the absolute reset_at. This is why reset times previously didn't show: the
// portal returns reset_after_seconds while reset_at stays 0 until you hit the
// cap.
function codexReset(w?: CodexUsageRateWindow): string {
  if (!w) return "—";
  let ms: number | null = null;
  if (typeof w.reset_after_seconds === "number" && w.reset_after_seconds > 0) {
    ms = w.reset_after_seconds * 1000;
  } else if (typeof w.reset_at === "number" && w.reset_at > 0) {
    ms = w.reset_at * 1000 - Date.now();
  }
  if (ms == null) return "—";
  if (ms < 0) return "now";
  const d = Math.floor(ms / 86400000);
  const h = Math.floor((ms % 86400000) / 3600000);
  const m = Math.floor((ms % 3600000) / 60000);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

// collectCodexWindows flattens the main rate_limit and every
// additional_rate_limits[] entry into a single labelled list, so all windows —
// including the weekly one wherever the portal puts it — render.
function collectCodexWindows(
  u: CodexUsageResponse["usage"],
): { label: string; w: CodexUsageRateWindow }[] {
  const out: { label: string; w: CodexUsageRateWindow }[] = [];
  const push = (w: CodexUsageRateWindow | undefined, fallback: string, prefix?: string) => {
    if (!w) return;
    const lbl = fmtWindowSeconds(w.limit_window_seconds) || fallback;
    out.push({ label: prefix ? `${prefix} · ${lbl}` : lbl, w });
  };
  const rl = u?.rate_limit;
  if (rl) {
    push(rl.primary_window, "5h");
    push(rl.secondary_window, "7d");
  }
  for (const a of u?.additional_rate_limits ?? []) {
    const name = a.limit_name || a.metered_feature || "extra";
    push(a.rate_limit?.primary_window, "5h", name);
    push(a.rate_limit?.secondary_window, "7d", name);
  }
  return out;
}

const ANTHROPIC_WINDOWS: [
  Exclude<keyof NonNullable<NonNullable<AnthropicResponse["usage"]>["body"]>, "limits">,
  string,
][] = [
  ["five_hour", "5-hour"],
  ["seven_day", "7-day"],
  ["seven_day_oauth_apps", "7-day OAuth"],
  ["seven_day_opus", "7-day Opus"],
  ["seven_day_sonnet", "7-day Sonnet"],
  ["seven_day_cowork", "7-day Cowork"],
  ["iguana_necktie", "iguana_necktie"],
];

function pctColor(raw: number | undefined | null): { pct: number | null; color: string } {
  // Anthropic returns 0..1 OR 0..100; Codex returns 0..100. Detect by magnitude.
  const pct = typeof raw === "number" ? Math.round(raw <= 1 ? raw * 100 : raw) : null;
  const color =
    pct == null
      ? "bg-muted"
      : pct >= 90
        ? "bg-red-500"
        : pct >= 70
          ? "bg-amber-500"
          : "bg-emerald-500";
  return { pct, color };
}

// pctColorPct is for values that are ALREADY a 0–100 percentage (Codex
// wham/usage `used_percent`, e.g. 1 = 1%). Unlike pctColor it does NOT apply
// the "<=1 ? *100" fraction heuristic — that heuristic turned a real 1% into
// 100% and made the Codex 5h window read as fully exhausted when it wasn't.
function pctColorPct(raw: number | undefined | null): { pct: number | null; color: string } {
  const pct = typeof raw === "number" ? Math.round(Math.max(0, Math.min(100, raw))) : null;
  const color =
    pct == null
      ? "bg-muted"
      : pct >= 90
        ? "bg-red-500"
        : pct >= 70
          ? "bg-amber-500"
          : "bg-emerald-500";
  return { pct, color };
}

function fmtCountdown(at?: string | number): string {
  if (!at) return "—";
  const ts = typeof at === "number" ? at * 1000 : new Date(at).getTime();
  const dt = ts - Date.now();
  if (dt < 0) return "now";
  const h = Math.floor(dt / 3600000);
  const m = Math.floor((dt % 3600000) / 60000);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

interface Props {
  authId: string | null;
  authLabel: string;
  // "anthropic" probes api.anthropic.com/api/oauth/usage; "openai" probes
  // chatgpt.com/backend-api/wham/usage. Defaults to anthropic for
  // backwards-compat with callers that haven't been updated.
  provider?: "anthropic" | "openai";
  onClose: () => void;
}

/* UpstreamUsagePanel — the probe body without any dialog chrome, so it can be
 * embedded in a larger surface (the credential detail dialog) without nesting
 * one Dialog inside another. */
export function UpstreamUsagePanel({
  authId,
  provider = "anthropic",
}: {
  authId: string | null;
  provider?: "anthropic" | "openai";
}) {
  const [data, setData] = useState<AnthropicResponse | CodexUsageResponse | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const endpoint = provider === "openai" ? "codex-usage" : "anthropic-usage";

  const run = async () => {
    if (!authId) return;
    setBusy(true);
    setErr("");
    try {
      const d = await apiPost<AnthropicResponse | CodexUsageResponse>(
        `/admin/credentials/${encodeURIComponent(authId)}/${endpoint}`,
      );
      setData(d);
    } catch (e) {
      setErr(errMsg(e, String(e)));
    } finally {
      setBusy(false);
    }
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: run is defined in the component body and intentionally excluded; authId/provider are the real triggers
  useEffect(() => {
    if (authId) {
      setData(null);
      setErr("");
      run();
    }
  }, [authId, provider]);

  return provider === "openai" ? (
    <CodexUsageBody
      authId={authId}
      data={data as CodexUsageResponse | null}
      busy={busy}
      err={err}
      onRefresh={run}
    />
  ) : (
    <AnthropicUsageBody
      data={data as AnthropicResponse | null}
      busy={busy}
      err={err}
      onRefresh={run}
    />
  );
}

export function UpstreamUsageDialog({ authId, authLabel, provider = "anthropic", onClose }: Props) {
  const { t } = useTranslation();
  return (
    <Dialog open={!!authId} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[640px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Gauge className="size-4" />
            {t("legacy.upstreamUsage.title", { label: authLabel })}
            {provider === "openai" && (
              <span className="rounded border border-violet-500/30 bg-violet-500/10 px-1.5 py-0.5 font-mono text-[10px] uppercase text-violet-500">
                wham/usage
              </span>
            )}
          </DialogTitle>
        </DialogHeader>
        <UpstreamUsagePanel authId={authId} provider={provider} />
      </DialogContent>
    </Dialog>
  );
}

function AnthropicUsageBody({
  data,
  busy,
  err,
  onRefresh,
}: {
  data: AnthropicResponse | null;
  busy: boolean;
  err: string;
  onRefresh: () => void;
}) {
  const { t } = useTranslation();
  const usageBody = data?.usage?.body;
  const profile = data?.profile?.body?.account;
  const tier = data?.profile?.body?.organization?.rate_limit_tier;
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <div>
          {profile?.email_address || profile?.email || "—"}
          {tier ? (
            <span className="ml-2 font-mono text-xs">
              {t("legacy.upstreamUsage.tierLabel", { tier })}
            </span>
          ) : null}
          {profile?.has_claude_max ? (
            <span className="ml-2 font-mono text-xs uppercase text-success">
              {t("legacy.upstreamUsage.maxBadge")}
            </span>
          ) : null}
          {profile?.has_claude_pro ? (
            <span className="ml-2 font-mono text-xs uppercase text-success">
              {t("legacy.upstreamUsage.proBadge")}
            </span>
          ) : null}
        </div>
        <Button size="sm" variant="ghost" disabled={busy} onClick={onRefresh}>
          <RefreshCw className={cn("size-3", busy && "animate-spin")} />{" "}
          {t("legacy.upstreamUsage.probe")}
        </Button>
      </div>

      {err && (
        <div className="rounded border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {err}
        </div>
      )}

      {data?.usage?.status && data.usage.status >= 400 ? (
        <div className="rounded border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {t("legacy.upstreamUsage.httpError", {
            status: data.usage.status,
            message: data.usage.error || t("legacy.upstreamUsage.upstreamError"),
          })}
        </div>
      ) : null}

      {usageBody && (
        <div className="space-y-2">
          {ANTHROPIC_WINDOWS.map(([k, label]) => {
            const w = usageBody[k];
            if (!w) return null;
            const { pct, color } = pctColor(w.utilization);
            return (
              <div key={k} className="space-y-1">
                <div className="flex items-center justify-between text-xs">
                  <span className="font-medium">{label}</span>
                  <span className="font-mono text-muted-foreground">
                    {pct != null ? `${pct}%` : "—"}
                    {w.resets_at
                      ? t("legacy.upstreamUsage.resetsIn", { when: fmtCountdown(w.resets_at) })
                      : ""}
                  </span>
                </div>
                <div className="h-2 rounded-full bg-muted">
                  <div
                    className={cn("h-full rounded-full", color)}
                    style={{ width: `${pct ?? 0}%` }}
                  />
                </div>
              </div>
            );
          })}
          {(() => {
            // Fable's independent weekly allotment lives in limits[] as a
            // weekly_scoped entry scoped to the Fable model — render it as one
            // more row. percent is already 0-100.
            const fable = (usageBody.limits || []).find(
              (l) =>
                (l?.scope?.model?.display_name || "").toLowerCase() === "fable" ||
                (l?.kind === "weekly_scoped" && !!l?.scope?.model),
            );
            if (!fable) return null;
            const label = `7-day ${fable.scope?.model?.display_name || "Fable"}`;
            const { pct, color } = pctColorPct(fable.percent);
            return (
              <div key="fable-scoped" className="space-y-1">
                <div className="flex items-center justify-between text-xs">
                  <span className="font-medium">{label}</span>
                  <span className="font-mono text-muted-foreground">
                    {pct != null ? `${pct}%` : "—"}
                    {fable.resets_at
                      ? t("legacy.upstreamUsage.resetsIn", { when: fmtCountdown(fable.resets_at) })
                      : ""}
                  </span>
                </div>
                <div className="h-2 rounded-full bg-muted">
                  <div
                    className={cn("h-full rounded-full", color)}
                    style={{ width: `${pct ?? 0}%` }}
                  />
                </div>
              </div>
            );
          })()}
        </div>
      )}

      <AllotmentEstimates list={data?.allotment_estimates} />

      {!data && !err && busy && (
        <div className="text-center py-6 text-sm text-muted-foreground">
          {t("legacy.upstreamUsage.querying")}
        </div>
      )}
    </div>
  );
}

const CONFIDENCE_CLASS: Record<AllotmentEstimate["confidence"], string> = {
  high: "text-success",
  medium: "text-warning",
  low: "text-muted-foreground",
};

/* AllotmentEstimates — what each reported window is worth in our own ledger's
 * units. The upstream says how full a window is and when it reopens, never
 * what 100% is; the server derives it from the request log (window start =
 * reset − length, spend since ÷ utilization), and a usage-limit 429 makes it
 * a direct measurement. */
function AllotmentEstimates({ list }: { list?: AllotmentEstimate[] }) {
  const { t } = useTranslation();
  if (!list?.length) return null;
  // Codex names windows by role and states each one's length; show both.
  const windowLabel = (e: AllotmentEstimate) => {
    switch (e.window) {
      case "five_hour":
        return t("legacy.upstreamUsage.allotment.window5h");
      case "seven_day":
        return t("legacy.upstreamUsage.allotment.window7d");
      case "primary":
        return `${t("legacy.upstreamUsage.allotment.windowPrimary")} · ${fmtHours(e.window_hours)}`;
      case "secondary":
        return `${t("legacy.upstreamUsage.allotment.windowSecondary")} · ${fmtHours(e.window_hours)}`;
      default:
        return e.window;
    }
  };
  const basisLabel = (b: AllotmentEstimate["basis"]) =>
    b === "quota_hit"
      ? t("legacy.upstreamUsage.allotment.basisQuotaHit")
      : b === "utilization"
        ? t("legacy.upstreamUsage.allotment.basisUtilization")
        : t("legacy.upstreamUsage.allotment.basisObserved");
  return (
    <div className="space-y-2 border-t border-border/60 pt-3">
      <div className="text-xs font-medium">{t("legacy.upstreamUsage.allotment.title")}</div>
      <div className="space-y-2">
        {list.map((e) => {
          const full = e.full_window;
          const rem = e.remaining;
          const windowFull = e.basis === "quota_hit" || e.utilization >= 1;
          return (
            <div key={e.window} className="rounded border border-border/60 px-3 py-2 text-xs">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{windowLabel(e)}</span>
                  <span className={cn("font-mono text-[10px]", CONFIDENCE_CLASS[e.confidence])}>
                    {basisLabel(e.basis)} ·{" "}
                    {t(`legacy.upstreamUsage.allotment.conf.${e.confidence}`)}
                  </span>
                </div>
                <div className="font-mono tabular-nums">
                  {full ? (
                    <>
                      <span className="text-muted-foreground">
                        {t("legacy.upstreamUsage.allotment.full")}{" "}
                      </span>
                      <span className="font-semibold">{fmtUSD(full.cost_usd)}</span>
                      <span className="text-muted-foreground">
                        {" "}
                        · {fmtCompact(full.weighted_tokens)}{" "}
                        {t("legacy.upstreamUsage.allotment.wtok")}
                      </span>
                    </>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </div>
              </div>
              <div className="mt-1 flex flex-wrap items-center justify-between gap-2 font-mono text-[11px] text-muted-foreground tabular-nums">
                <span>
                  {t("legacy.upstreamUsage.allotment.observed")} {fmtUSD(e.observed.cost_usd)} ·{" "}
                  {fmtCompact(e.observed.weighted_tokens)}{" "}
                  {t("legacy.upstreamUsage.allotment.wtok")}
                  {" · "}
                  {t("legacy.upstreamUsage.allotment.over", {
                    hours: fmtHours(e.observed_hours),
                    pct: Math.round(e.utilization * 100),
                  })}
                  {e.quota_hit_at ? ` · 429 ${new Date(e.quota_hit_at).toLocaleString()}` : ""}
                </span>
                <span>
                  {windowFull && full
                    ? t("legacy.upstreamUsage.allotment.windowFull")
                    : rem
                      ? `${t("legacy.upstreamUsage.allotment.remaining")} ${fmtUSD(rem.cost_usd)}`
                      : ""}
                </span>
              </div>
              {e.spend_error && (
                <div className="mt-1 text-[11px] text-destructive">
                  {t("legacy.upstreamUsage.allotment.spendError", { err: e.spend_error })}
                </div>
              )}
            </div>
          );
        })}
      </div>
      <div className="text-[11px] leading-snug text-muted-foreground">
        {t("legacy.upstreamUsage.allotment.note")}
      </div>
    </div>
  );
}

function CodexUsageBody({
  authId,
  data,
  busy,
  err,
  onRefresh,
}: {
  authId: string | null;
  data: CodexUsageResponse | null;
  busy: boolean;
  err: string;
  onRefresh: () => void;
}) {
  const { t } = useTranslation();
  const [resetting, setResetting] = useState(false);
  const [resetMsg, setResetMsg] = useState("");
  const [resetErr, setResetErr] = useState("");
  const u = data?.usage;
  const rl = u?.rate_limit;
  const credits = u?.credits;
  const spend = u?.spend_control;
  const resetCredits = u?.rate_limit_reset_credits?.available_count ?? 0;
  const windows = collectCodexWindows(u);

  // Consume one rate-limit reset credit. Irreversible (burns a card), so we
  // confirm first, then re-query usage via onRefresh so the countdowns and the
  // remaining-credit count both update.
  const doReset = async () => {
    if (!authId || resetting || resetCredits <= 0) return;
    if (!window.confirm(t("legacy.upstreamUsage.resetConfirm", { n: resetCredits }))) return;
    setResetting(true);
    setResetMsg("");
    setResetErr("");
    try {
      const d = await apiPost<CodexResetResponse>(
        `/admin/credentials/${encodeURIComponent(authId)}/reset-codex-credit`,
      );
      setResetMsg(t("legacy.upstreamUsage.resetDone", { n: d.reset?.windows_reset ?? 0 }));
      onRefresh();
    } catch (e) {
      setResetErr(errMsg(e, String(e)));
    } finally {
      setResetting(false);
    }
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <div className="flex flex-wrap items-center gap-2">
          <span>{u?.email || "—"}</span>
          {u?.plan_type && (
            <span className="rounded border border-success/30 bg-success/15 px-1.5 py-0.5 font-mono text-[10px] uppercase text-success">
              {u.plan_type}
            </span>
          )}
          {rl?.limit_reached && (
            <span className="rounded border border-destructive/30 bg-destructive/15 px-1.5 py-0.5 font-mono text-[10px] uppercase text-destructive">
              limit reached
              {typeof u?.rate_limit_reached_type === "string" && u.rate_limit_reached_type
                ? ` (${u.rate_limit_reached_type})`
                : ""}
            </span>
          )}
          {spend?.reached && (
            <span className="rounded border border-warning/30 bg-warning/15 px-1.5 py-0.5 font-mono text-[10px] uppercase text-warning">
              spend cap reached
            </span>
          )}
        </div>
        <Button size="sm" variant="ghost" disabled={busy} onClick={onRefresh}>
          <RefreshCw className={cn("size-3", busy && "animate-spin")} /> 重新查询
        </Button>
      </div>

      {err && (
        <div className="rounded border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {err}
        </div>
      )}

      {windows.length > 0 && (
        <div className="overflow-hidden rounded-md border border-border">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-border bg-muted/40 text-[10px] uppercase tracking-wider text-muted-foreground">
                <th className="px-3 py-1.5 text-left font-medium">
                  {t("legacy.upstreamUsage.window")}
                </th>
                <th className="px-2 py-1.5 text-right font-medium">
                  {t("legacy.upstreamUsage.used")}
                </th>
                <th className="w-24 px-2 py-1.5 font-medium" />
                <th className="px-3 py-1.5 text-right font-medium">
                  {t("legacy.upstreamUsage.resetsInCol")}
                </th>
              </tr>
            </thead>
            <tbody>
              {windows.map(({ label, w }, i) => {
                const { pct, color } = pctColorPct(w.used_percent);
                return (
                  // biome-ignore lint/suspicious/noArrayIndexKey: codex windows have no stable unique id; label alone can collide when duplicated
                  <tr key={`${label}-${i}`} className="border-b border-border/60 last:border-b-0">
                    <td className="px-3 py-1.5 font-medium">{label}</td>
                    <td className="px-2 py-1.5 text-right font-mono tabular-nums">
                      {pct != null ? `${pct}%` : "—"}
                    </td>
                    <td className="px-2 py-1.5">
                      <div className="h-1.5 rounded-full bg-muted">
                        <div
                          className={cn("h-full rounded-full", color)}
                          style={{ width: `${pct ?? 0}%` }}
                        />
                      </div>
                    </td>
                    <td className="px-3 py-1.5 text-right font-mono tabular-nums text-muted-foreground">
                      {codexReset(w)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <AllotmentEstimates list={data?.allotment_estimates} />

      {u && (
        <div className="flex items-center justify-between gap-2 border-t border-border/60 pt-2">
          <div className="text-xs text-muted-foreground">
            {t("legacy.upstreamUsage.resetCredits", { n: resetCredits })}
          </div>
          <div className="flex items-center gap-2">
            {resetMsg && <span className="text-xs text-success">{resetMsg}</span>}
            {resetErr && <span className="text-xs text-destructive">{resetErr}</span>}
            <Button
              size="sm"
              variant="outline"
              disabled={resetting || resetCredits <= 0}
              onClick={doReset}
              title={
                resetCredits > 0
                  ? t("legacy.upstreamUsage.resetButton")
                  : t("legacy.upstreamUsage.resetNone")
              }
            >
              <Zap className={cn("size-3", resetting && "animate-pulse")} />
              {resetting
                ? t("legacy.upstreamUsage.resetting")
                : t("legacy.upstreamUsage.resetButton")}
            </Button>
          </div>
        </div>
      )}

      {credits &&
        (credits.has_credits ||
          credits.unlimited ||
          (credits.balance && credits.balance !== "0")) && (
          <div className="rounded-md border border-border bg-muted/30 p-3 text-xs space-y-1">
            <div className="font-medium uppercase text-muted-foreground text-[10px]">Credits</div>
            <div className="flex items-baseline justify-between">
              <span className="text-muted-foreground">余额</span>
              <span className="font-mono tabular-nums">
                {credits.unlimited ? "∞" : (credits.balance ?? "0")}
              </span>
            </div>
            {credits.approx_local_messages && credits.approx_local_messages.length > 0 && (
              <div className="flex items-baseline justify-between">
                <span className="text-muted-foreground">本地剩余消息（近似）</span>
                <span className="font-mono tabular-nums">
                  {credits.approx_local_messages.join(" – ")}
                </span>
              </div>
            )}
            {credits.approx_cloud_messages && credits.approx_cloud_messages.length > 0 && (
              <div className="flex items-baseline justify-between">
                <span className="text-muted-foreground">云端剩余消息（近似）</span>
                <span className="font-mono tabular-nums">
                  {credits.approx_cloud_messages.join(" – ")}
                </span>
              </div>
            )}
            {credits.overage_limit_reached && (
              <div className="text-destructive font-mono">已超出 overage 限额</div>
            )}
          </div>
        )}

      {spend?.individual_limit != null && (
        <div className="text-xs text-muted-foreground">
          个人消费上限：<span className="font-mono">{spend.individual_limit}</span>
        </div>
      )}

      {u?.updated && (
        <div className="text-[11px] text-muted-foreground">
          上次查询：{new Date(u.updated).toLocaleString()}
        </div>
      )}

      {!data && !err && busy && (
        <div className="text-center py-6 text-sm text-muted-foreground">正在查询 wham/usage…</div>
      )}
    </div>
  );
}
