import { useCallback, useEffect, useState } from "react";
import { apiGet } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { fmtInt, fmtUSD } from "@/lib/utils";
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
      const [d, all, hr, creds] = await Promise.all([
        apiGet<RequestsResp>(`/admin/requests?limit=1&from=${from}&to=${to}`),
        apiGet<RequestsResp>(`/admin/requests?limit=1`),
        apiGet<{ buckets: HourBucket[] }>(`/admin/requests/hourly?hours=24`),
        apiGet<{ credentials: any[] }>(`/admin/credentials`),
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

  useEffect(() => {
    load();
  }, [load, refreshTick]);

  const sparkData: SparkPoint[] = hourly.map((b) => ({
    label: b.hour.slice(11, 16) + " UTC",
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
    <div className="space-y-4">
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <MetricCard
          label="Pool"
          value={pool ? `${pool.healthy}/${pool.total}` : "—"}
          unit="healthy"
          hint={
            pool
              ? `${pool.quota || 0} quota · ${pool.unhealthy || 0} fail · ${pool.disabled || 0} off`
              : busy
              ? "loading…"
              : "no data"
          }
        />
        <MetricCard
          label={`Cost ${DAYS}d`}
          value={fmtUSD(reqData?.summary.cost_usd)}
          unit="usd"
          hint={reqData ? `${fmtInt(reqData.summary.count)} req` : ""}
        />
        <MetricCard
          label="Cost lifetime"
          value={fmtUSD(lifetime?.summary.cost_usd)}
          unit="usd"
          hint={lifetime ? `${fmtInt(lifetime.summary.count)} req` : ""}
        />
        <MetricCard
          label="Errors 14d"
          value={fmtInt(reqData?.summary.errors)}
          unit=""
          hint={reqData ? `${fmtInt(reqData.summary.input_tokens)} in / ${fmtInt(reqData.summary.output_tokens)} out` : ""}
        />
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-baseline justify-between">
            <div>
              <CardTitle>24h activity</CardTitle>
              <CardDescription>Hourly cost (USD) — sparkline below</CardDescription>
            </div>
            <span className="text-sm text-muted-foreground font-mono">
              {hourly.length > 0 ? fmtUSD(hourly.reduce((s, h) => s + (h.cost_usd || 0), 0)) : "—"}
            </span>
          </div>
        </CardHeader>
        <CardContent>
          <Sparkline data={sparkData} />
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Card>
          <CardHeader>
            <CardTitle>Top clients · 14d</CardTitle>
          </CardHeader>
          <CardContent>
            {topClients.length === 0 ? (
              <div className="text-sm text-muted-foreground">—</div>
            ) : (
              <table className="w-full text-sm">
                <tbody>
                  {topClients.map(([k, a]) => (
                    <tr key={k} className="border-b last:border-0">
                      <td className="py-1.5 pr-3 font-medium">
                        {k || <span className="text-muted-foreground">(unnamed)</span>}
                      </td>
                      <td className="py-1.5 font-mono text-right">{fmtUSD(a.cost_usd)}</td>
                      <td className="py-1.5 font-mono text-right text-muted-foreground w-20">
                        {fmtInt(a.count)} req
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Top models · 14d</CardTitle>
          </CardHeader>
          <CardContent>
            {topModels.length === 0 ? (
              <div className="text-sm text-muted-foreground">—</div>
            ) : (
              <table className="w-full text-sm">
                <tbody>
                  {topModels.map(([k, a]) => (
                    <tr key={k} className="border-b last:border-0">
                      <td className="py-1.5 pr-3 font-mono">{k}</td>
                      <td className="py-1.5 font-mono text-right">{fmtUSD(a.cost_usd)}</td>
                      <td className="py-1.5 font-mono text-right text-muted-foreground w-20">
                        {fmtInt(a.count)} req
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function MetricCard({
  label,
  value,
  unit,
  hint,
}: {
  label: string;
  value: string | number;
  unit?: string;
  hint?: string;
}) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
        <div className="mt-2 flex items-baseline gap-1.5">
          <span className="font-mono text-2xl md:text-3xl leading-none font-medium tracking-tight tabular-nums">
            {value}
          </span>
          {unit && <span className="font-mono text-xs text-muted-foreground uppercase">{unit}</span>}
        </div>
        {hint && <div className="mt-2 text-xs font-mono text-muted-foreground tabular-nums">{hint}</div>}
      </CardContent>
    </Card>
  );
}
