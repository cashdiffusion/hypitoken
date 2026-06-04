import { useCallback, useEffect, useState } from "react";
import { api } from "@/legacy/lib/api";
import type { HourBucket, HourlyResp, Pricing, RequestsResp, Summary } from "@/legacy/lib/types";
import { DashboardBoard, type DashboardPool, type DashboardRequestsSlim } from "./dashboard-board";

interface Props {
  summary: Summary | null;
  pricing?: Pricing;
  refreshTick: number;
}

const DAYS = 14;
function pad(n: number) {
  return String(n).padStart(2, "0");
}

// Admin wrapper. Fans out to the three admin API endpoints that feed the
// dashboard, synthesizes a DashboardPool from summary.auths, and delegates
// rendering to the shared DashboardBoard.
export function OverviewPanel({ summary, pricing, refreshTick }: Props) {
  const [reqData, setReqData] = useState<RequestsResp | null>(null);
  const [lifetimeData, setLifetimeData] = useState<RequestsResp | null>(null);
  const [hourly, setHourly] = useState<HourBucket[] | null>(null);
  const [userPaid, setUserPaid] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const today = new Date();
      const fromD = new Date(today);
      fromD.setDate(today.getDate() - (DAYS - 1));
      const from = `${fromD.getFullYear()}-${pad(fromD.getMonth() + 1)}-${pad(fromD.getDate())}`;
      const to = `${today.getFullYear()}-${pad(today.getMonth() + 1)}-${pad(today.getDate())}`;
      const [d, all, hr, walletTotals] = await Promise.all([
        api<RequestsResp>(`/admin/api/requests?limit=1&from=${from}&to=${to}`),
        api<RequestsResp>(`/admin/api/requests?limit=1`),
        api<HourlyResp>(`/admin/api/requests/hourly?hours=24`),
        // Fleet wallet totals — sum across all SaaS users of charges
        // post-multiplier. Powers the "Saved by us" card. SaaS-only
        // endpoint; tolerate failure on non-SaaS deploys.
        api<{ user_paid_usd: number }>(`/api/v2/admin/wallet-totals`).catch(() => null),
      ]);
      setReqData(d);
      setLifetimeData(all);
      setHourly(hr.buckets || []);
      setUserPaid(walletTotals ? walletTotals.user_paid_usd : null);
    } catch {
      // ignore
    } finally {
      setBusy(false);
    }
  }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: refreshTick is a prop-based refresh counter; keeping it triggers reload when the parent increments it
  useEffect(() => {
    load();
  }, [load, refreshTick]);

  const pool: DashboardPool | null = summary
    ? (() => {
        let healthy = 0,
          quota = 0,
          unhealthy = 0,
          disabled = 0;
        for (const a of summary.auths) {
          if (a.disabled) disabled++;
          else if (a.quota_exceeded) quota++;
          else if (a.hard_failure) unhealthy++;
          else if (a.healthy) healthy++;
          else unhealthy++;
        }
        return { total: summary.auths.length, healthy, quota, unhealthy, disabled };
      })()
    : null;

  const slim = (r: RequestsResp | null): DashboardRequestsSlim | null =>
    r
      ? { summary: r.summary, by_client: r.by_client, by_model: r.by_model, by_day: r.by_day }
      : null;

  return (
    <DashboardBoard
      pool={pool}
      pricing={pricing}
      reqData={slim(reqData)}
      lifetimeData={slim(lifetimeData)}
      hourly={hourly}
      busy={busy}
      userPaidUSD={userPaid}
    />
  );
}
