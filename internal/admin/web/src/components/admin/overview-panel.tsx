import { useCallback, useEffect, useState } from "react";
import { FadeIn } from "@/components/admin/fade-in";
import { GlassPanel } from "@/components/app/page-primitives";
import { SpotlightCard } from "@/components/landing/interactions";
import { apiGet } from "@/lib/api";
import type { Credential } from "@/lib/types";
import { cn, fmtInt, fmtUSD } from "@/lib/utils";
import { Sparkline, type SparkPoint } from "./sparkline";

interface RequestAgg {
  count: number;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  errors: number;
}

interface RequestsResp {
  summary: RequestAgg;
  by_client: Record<string, RequestAgg>;
  by_model: Record<string, RequestAgg>;
  by_day: Record<string, RequestAgg>;
}

interface HourBucket {
  hour: string;
  count: number;
  input_tokens: number;
  output_tokens: number;
  cost_usd: number;
  errors: number;
}

interface PoolSummary {
  total: number;
  healthy: number;
  quota: number;
  unhealthy: number;
  disabled: number;
}

const DAYS = 14;
function pad(n: number) {
  return String(n).padStart(2, "0");
}

// Slim operator-overview panel. Fans out to /api/v2/admin/requests and
// /requests/hourly + /credentials, then renders a 4-card metric strip plus
// top-clients / top-models / 24h sparkline. The DashboardBoard from the
// legacy SPA is intentionally NOT ported wholesale — that 939-line
// component was tangled to the legacy summary shape; this stripped-down
// version captures the spirit while staying maintainable.
export function OverviewPanel({ refreshTick }: { refreshTick: number }) {
  const [reqData, setReqData] = useState<RequestsResp | null>(null);
  const [lifetime, setLifetime] = useState<RequestsResp | null>(null);
  const [hourly, setHourly] = useState<HourBucket[]>([]);
  const [pool, setPool] = useState<PoolSummary | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const today = new Date();
      const fromD = new Date(today);
      fromD.setDate(today.getDate() - (DAYS - 1));
      const from = `${fromD.getFullYear()}-${pad(fromD.getMonth() + 1)}-${pad(fromD.getDate())}`;
      const to = `${today.getFullYear()}-${pad(today.getMonth() + 1)}-${pad(today.getDate())}`;
      // dims asks for only the three rollups this panel renders — the by-day
      // bucket is never read here (the sparkline comes from /requests/hourly),
      // and each rollup the server skips is a GROUP BY it does not run.
      const [d, all, hr, creds] = await Promise.all([
        apiGet<RequestsResp>(
          `/admin/requests?limit=1&dims=summary,by_client,by_model&from=${from}&to=${to}`,
        ),
        apiGet<RequestsResp>(`/admin/requests?limit=1&dims=summary`),
        apiGet<{ buckets: HourBucket[] }>(`/admin/requests/hourly?hours=24`),
        apiGet<{ credentials: Credential[] }>(`/admin/credentials`),
      ]);
      setReqData(d);
      setLifetime(all);
      setHourly(hr.buckets || []);
      // Pool summary from credentials list — same logic the legacy
      // dashboard-board ran client-side.
      let healthy = 0,
        quota = 0,
        unhealthy = 0,
        disabled = 0;
      for (const a of creds.credentials || []) {
        if (a.disabled) disabled++;
        else if (a.quota_exceeded) quota++;
        else if (a.hard_failure) unhealthy++;
        else if (a.healthy) healthy++;
        else unhealthy++;
      }
      setPool({ total: (creds.credentials || []).length, healthy, quota, unhealthy, disabled });
    } catch {
      // ignore — surfaced elsewhere
    } finally {
      setBusy(false);
    }
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies: refreshTick is the intentional parent-driven refresh trigger; load is stable (useCallback []).
  useEffect(() => {
    load();
  }, [load, refreshTick]);

  const sparkData: SparkPoint[] = hourly.map((b) => ({
    label: `${b.hour.slice(11, 16)} UTC`,
    value: b.cost_usd || 0,
  }));

  const topClients = reqData
    ? Object.entries(reqData.by_client)
        .sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
        .slice(0, 6)
    : [];
  const topModels = reqData
    ? Object.entries(reqData.by_model)
        .sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
        .slice(0, 6)
    : [];

  return (
    <FadeIn className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <div className="flex">
          <MetricTile
            label="Pool"
            value={pool ? `${pool.healthy}/${pool.total}` : "—"}
            unit="healthy"
            accent
            hint={
              pool
                ? `${pool.quota || 0} quota · ${pool.unhealthy || 0} fail · ${pool.disabled || 0} off`
                : busy
                  ? "loading…"
                  : "no data"
            }
          />
        </div>
        <div className="flex">
          <MetricTile
            label={`Cost ${DAYS}d`}
            value={fmtUSD(reqData?.summary.cost_usd)}
            unit="usd"
            hint={reqData ? `${fmtInt(reqData.summary.count)} req` : ""}
          />
        </div>
        <div className="flex">
          <MetricTile
            label="Cost lifetime"
            value={fmtUSD(lifetime?.summary.cost_usd)}
            unit="usd"
            hint={lifetime ? `${fmtInt(lifetime.summary.count)} req` : ""}
          />
        </div>
        <div className="flex">
          <MetricTile
            label="Errors 14d"
            value={fmtInt(reqData?.summary.errors)}
            hint={
              reqData
                ? `${fmtInt(reqData.summary.input_tokens)} in / ${fmtInt(reqData.summary.output_tokens)} out`
                : ""
            }
          />
        </div>
      </div>

      <GlassPanel
        title="24h activity"
        description="Hourly cost (USD) — sparkline below"
        action={
          <span className="font-mono text-sm text-muted-foreground tabular-nums">
            {hourly.length > 0 ? fmtUSD(hourly.reduce((s, h) => s + (h.cost_usd || 0), 0)) : "—"}
          </span>
        }
      >
        <Sparkline data={sparkData} />
      </GlassPanel>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <GlassPanel title="Top clients · 14d">
          <TopTable rows={topClients} mono={false} />
        </GlassPanel>
        <GlassPanel title="Top models · 14d">
          <TopTable rows={topModels} mono />
        </GlassPanel>
      </div>
    </FadeIn>
  );
}

function TopTable({ rows, mono }: { rows: [string, RequestAgg][]; mono: boolean }) {
  if (rows.length === 0) return <div className="text-sm text-muted-foreground">—</div>;
  return (
    <table className="w-full text-sm">
      <tbody>
        {rows.map(([k, a]) => (
          <tr
            key={k}
            className="border-b border-border/50 transition-colors last:border-0 hover:bg-primary/[0.03]"
          >
            <td className={cn("py-1.5 pr-3", mono ? "font-mono" : "font-medium")}>
              {k || <span className="text-muted-foreground">(unnamed)</span>}
            </td>
            <td className="py-1.5 text-right font-mono tabular-nums">{fmtUSD(a.cost_usd)}</td>
            <td className="w-20 py-1.5 text-right font-mono tabular-nums text-muted-foreground">
              {fmtInt(a.count)} req
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function MetricTile({
  label,
  value,
  unit,
  hint,
  accent,
}: {
  label: string;
  value: string | number;
  unit?: string;
  hint?: string;
  accent?: boolean;
}) {
  return (
    <SpotlightCard
      tiltDeg={0}
      className={cn("h-full w-full p-4", accent && "ring-1 ring-primary/30")}
    >
      <div className="text-[11px] uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className="mt-2 flex items-baseline gap-1.5">
        <span
          className={cn(
            "font-mono text-2xl font-semibold leading-none tracking-tight tabular-nums md:text-3xl",
            accent ? "text-primary" : "text-foreground",
          )}
        >
          {value}
        </span>
        {unit && (
          <span className="font-mono text-xs uppercase tracking-wider text-muted-foreground">
            {unit}
          </span>
        )}
      </div>
      {hint && (
        <div className="mt-2 font-mono text-[11px] tabular-nums text-muted-foreground">{hint}</div>
      )}
    </SpotlightCard>
  );
}
