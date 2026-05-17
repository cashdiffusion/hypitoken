import { useEffect, useState } from "react";
import { Gauge, RefreshCw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { apiPost } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

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
    };
  };
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
  limit_window_seconds?: number;
  reset_after_seconds?: number;
  reset_at?: number; // unix seconds
}

interface CodexUsageResponse {
  usage?: {
    user_id?: string;
    account_id?: string;
    email?: string;
    plan_type?: string;
    updated?: string;
    rate_limit?: {
      allowed?: boolean;
      limit_reached?: boolean;
      primary_window?: CodexUsageRateWindow;
      secondary_window?: CodexUsageRateWindow;
    };
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
    rate_limit_reached_type?: string | null;
  };
}

const ANTHROPIC_WINDOWS: [keyof NonNullable<NonNullable<AnthropicResponse["usage"]>["body"]>, string][] = [
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

export function UpstreamUsageDialog({ authId, authLabel, provider = "anthropic", onClose }: Props) {
  const { t } = useTranslation();
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
    } catch (e: any) {
      setErr(e.message || String(e));
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    if (authId) {
      setData(null);
      setErr("");
      run();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [authId, provider]);

  return (
    <Dialog open={!!authId} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[640px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Gauge className="size-4" />
            {t("legacy.upstreamUsage.title", { label: authLabel })}
            {provider === "openai" && (
              <span className="rounded border border-violet-500/30 bg-violet-500/10 px-1.5 py-0.5 text-[10px] font-mono uppercase text-violet-500">
                wham/usage
              </span>
            )}
          </DialogTitle>
        </DialogHeader>
        {provider === "openai" ? (
          <CodexUsageBody data={data as CodexUsageResponse | null} busy={busy} err={err} onRefresh={run} />
        ) : (
          <AnthropicUsageBody data={data as AnthropicResponse | null} busy={busy} err={err} onRefresh={run} />
        )}
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
          {tier ? <span className="ml-2 font-mono text-xs">{t("legacy.upstreamUsage.tierLabel", { tier })}</span> : null}
          {profile?.has_claude_max ? <span className="ml-2 font-mono text-xs uppercase text-success">{t("legacy.upstreamUsage.maxBadge")}</span> : null}
          {profile?.has_claude_pro ? <span className="ml-2 font-mono text-xs uppercase text-success">{t("legacy.upstreamUsage.proBadge")}</span> : null}
        </div>
        <Button size="sm" variant="ghost" disabled={busy} onClick={onRefresh}>
          <RefreshCw className={cn("size-3", busy && "animate-spin")} /> {t("legacy.upstreamUsage.probe")}
        </Button>
      </div>

      {err && <div className="rounded border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{err}</div>}

      {data?.usage?.status && data.usage.status >= 400 ? (
        <div className="rounded border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {t("legacy.upstreamUsage.httpError", { status: data.usage.status, message: data.usage.error || t("legacy.upstreamUsage.upstreamError") })}
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
                    {w.resets_at ? t("legacy.upstreamUsage.resetsIn", { when: fmtCountdown(w.resets_at) }) : ""}
                  </span>
                </div>
                <div className="h-2 rounded-full bg-muted">
                  <div className={cn("h-full rounded-full", color)} style={{ width: `${pct ?? 0}%` }} />
                </div>
              </div>
            );
          })}
        </div>
      )}

      {!data && !err && busy && (
        <div className="text-center py-6 text-sm text-muted-foreground">{t("legacy.upstreamUsage.querying")}</div>
      )}
    </div>
  );
}

function CodexUsageBody({
  data,
  busy,
  err,
  onRefresh,
}: {
  data: CodexUsageResponse | null;
  busy: boolean;
  err: string;
  onRefresh: () => void;
}) {
  const u = data?.usage;
  const rl = u?.rate_limit;
  const credits = u?.credits;
  const spend = u?.spend_control;
  const primary = rl?.primary_window;
  const secondary = rl?.secondary_window;
  const primaryPct = pctColor(primary?.used_percent);
  const secondaryPct = pctColor(secondary?.used_percent);

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
              limit reached{u?.rate_limit_reached_type ? ` (${u.rate_limit_reached_type})` : ""}
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

      {err && <div className="rounded border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{err}</div>}

      {rl && (
        <div className="space-y-3">
          {primary && (
            <div className="space-y-1">
              <div className="flex items-center justify-between text-xs">
                <span className="font-medium">主窗口（5h）</span>
                <span className="font-mono text-muted-foreground">
                  {primaryPct.pct != null ? `${primaryPct.pct}%` : "—"}
                  {primary.reset_at ? ` · 重置于 ${fmtCountdown(primary.reset_at)}` : ""}
                </span>
              </div>
              <div className="h-2 rounded-full bg-muted">
                <div className={cn("h-full rounded-full", primaryPct.color)} style={{ width: `${primaryPct.pct ?? 0}%` }} />
              </div>
            </div>
          )}
          {secondary && (
            <div className="space-y-1">
              <div className="flex items-center justify-between text-xs">
                <span className="font-medium">次窗口（7d）</span>
                <span className="font-mono text-muted-foreground">
                  {secondaryPct.pct != null ? `${secondaryPct.pct}%` : "—"}
                  {secondary.reset_at ? ` · 重置于 ${fmtCountdown(secondary.reset_at)}` : ""}
                </span>
              </div>
              <div className="h-2 rounded-full bg-muted">
                <div className={cn("h-full rounded-full", secondaryPct.color)} style={{ width: `${secondaryPct.pct ?? 0}%` }} />
              </div>
            </div>
          )}
        </div>
      )}

      {credits && (credits.has_credits || credits.unlimited || (credits.balance && credits.balance !== "0")) && (
        <div className="rounded-md border border-border bg-muted/30 p-3 text-xs space-y-1">
          <div className="font-medium uppercase text-muted-foreground text-[10px]">Credits</div>
          <div className="flex items-baseline justify-between">
            <span className="text-muted-foreground">余额</span>
            <span className="font-mono tabular-nums">{credits.unlimited ? "∞" : credits.balance ?? "0"}</span>
          </div>
          {credits.approx_local_messages && credits.approx_local_messages.length > 0 && (
            <div className="flex items-baseline justify-between">
              <span className="text-muted-foreground">本地剩余消息（近似）</span>
              <span className="font-mono tabular-nums">{credits.approx_local_messages.join(" – ")}</span>
            </div>
          )}
          {credits.approx_cloud_messages && credits.approx_cloud_messages.length > 0 && (
            <div className="flex items-baseline justify-between">
              <span className="text-muted-foreground">云端剩余消息（近似）</span>
              <span className="font-mono tabular-nums">{credits.approx_cloud_messages.join(" – ")}</span>
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
