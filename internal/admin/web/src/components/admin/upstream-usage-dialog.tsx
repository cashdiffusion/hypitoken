import { useEffect, useState } from "react";
import { Gauge, RefreshCw } from "lucide-react";
import { apiPost } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

interface UsageWindow {
  utilization?: number;
  resets_at?: string;
}

interface UpstreamResponse {
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

const WINDOWS: [keyof NonNullable<NonNullable<UpstreamResponse["usage"]>["body"]>, string][] = [
  ["five_hour", "5-hour"],
  ["seven_day", "7-day"],
  ["seven_day_oauth_apps", "7-day OAuth"],
  ["seven_day_opus", "7-day Opus"],
  ["seven_day_sonnet", "7-day Sonnet"],
  ["seven_day_cowork", "7-day Cowork"],
  ["iguana_necktie", "iguana_necktie"],
];

function pctColor(raw: number | undefined | null): { pct: number | null; color: string } {
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

function fmtCountdown(at?: string): string {
  if (!at) return "—";
  const dt = new Date(at).getTime() - Date.now();
  if (dt < 0) return "now";
  const h = Math.floor(dt / 3600000);
  const m = Math.floor((dt % 3600000) / 60000);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

interface Props {
  authId: string | null;
  authLabel: string;
  onClose: () => void;
}

export function UpstreamUsageDialog({ authId, authLabel, onClose }: Props) {
  const [data, setData] = useState<UpstreamResponse | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const run = async () => {
    if (!authId) return;
    setBusy(true);
    setErr("");
    try {
      const d = await apiPost<UpstreamResponse>(
        `/admin/credentials/${encodeURIComponent(authId)}/anthropic-usage`,
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
  }, [authId]);

  const usageBody = data?.usage?.body;
  const profile = data?.profile?.body?.account;
  const tier = data?.profile?.body?.organization?.rate_limit_tier;

  return (
    <Dialog open={!!authId} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[640px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Gauge className="size-4" />
            Anthropic upstream — {authLabel}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="flex items-center justify-between text-sm text-muted-foreground">
            <div>
              {profile?.email_address || profile?.email || "—"}
              {tier ? <span className="ml-2 font-mono text-xs">tier: {tier}</span> : null}
              {profile?.has_claude_max ? <span className="ml-2 font-mono text-xs uppercase text-success">max</span> : null}
              {profile?.has_claude_pro ? <span className="ml-2 font-mono text-xs uppercase text-success">pro</span> : null}
            </div>
            <Button size="sm" variant="ghost" disabled={busy} onClick={run}>
              <RefreshCw className={cn("size-3", busy && "animate-spin")} /> Refresh
            </Button>
          </div>

          {err && <div className="rounded border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{err}</div>}

          {data?.usage?.status && data.usage.status >= 400 ? (
            <div className="rounded border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              HTTP {data.usage.status}: {data.usage.error || "upstream error"}
            </div>
          ) : null}

          {usageBody && (
            <div className="space-y-2">
              {WINDOWS.map(([k, label]) => {
                const w = usageBody[k];
                if (!w) return null;
                const { pct, color } = pctColor(w.utilization);
                return (
                  <div key={k} className="space-y-1">
                    <div className="flex items-center justify-between text-xs">
                      <span className="font-medium">{label}</span>
                      <span className="font-mono text-muted-foreground">
                        {pct != null ? `${pct}%` : "—"}
                        {w.resets_at ? ` · resets in ${fmtCountdown(w.resets_at)}` : ""}
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
            <div className="text-center py-6 text-sm text-muted-foreground">Querying upstream…</div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
