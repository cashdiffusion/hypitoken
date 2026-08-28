import { useCallback, useEffect, useState } from "react";
import { api } from "@/legacy/lib/api";
import type { HourBucket, HourlyResp, RequestsResp, Summary } from "@/legacy/lib/types";
import { DashboardBoard, type DashboardPool, type DashboardRequestsSlim } from "./dashboard-board";

interface Props {
  summary: Summary | null;
  refreshTick: number;
}

const DAYS = 14;
function pad(n: number) {
  return String(n).padStart(2, "0");
}

// Console wrapper. Fans out to the request-log endpoints that feed the
// platform tab, synthesizes a DashboardPool from summary.auths, and delegates
// rendering to DashboardBoard.
//
// Only shape-bearing endpoints are fetched. The old "saved by us" card pulled
// /api/v2/admin/wallet-totals — fleet lifetime revenue — and is gone along
// with the card; don't re-add the fetch for a value nothing renders.
export function OverviewPanel({ summary, refreshTick }: Props) {
  const [reqData, setReqData] = useState<RequestsResp | null>(null);
  const [lifetimeData, setLifetimeData] = useState<RequestsResp | null>(null);
  const [hourly, setHourly] = useState<HourBucket[] | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const today = new Date();
      const fromD = new Date(today);
      fromD.setDate(today.getDate() - (DAYS - 1));
      const from = `${fromD.getFullYear()}-${pad(fromD.getMonth() + 1)}-${pad(fromD.getDate())}`;
      const to = `${today.getFullYear()}-${pad(today.getMonth() + 1)}-${pad(today.getDate())}`;
      const [d, all, hr] = await Promise.all([
        // anon=1: the console must never expose real client identities (token
        // labels or masked sk-… tokens). For non-operator callers the backend
        // goes further and normalizes every series — see admin/public_redact.go.
        api<RequestsResp>(`/admin/api/requests?limit=1&from=${from}&to=${to}&anon=1`),
        api<RequestsResp>(`/admin/api/requests?limit=1&anon=1`),
        api<HourlyResp>(`/admin/api/requests/hourly?hours=24`),
      ]);
      setReqData(d);
      setLifetimeData(all);
      setHourly(hr.buckets || []);
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

  // Non-operator callers receive a redacted summary with no `auths` rows
  // (see handleSummary). Treat that as the public view: hide the credential
  // pool breakdown. Operators viewing this page get the real pool pie.
  const publicView = (summary?.auths?.length ?? 0) === 0;

  const pool: DashboardPool | null =
    summary && !publicView
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

  // by_client / by_model are deliberately not forwarded: the board no longer
  // ranks clients or models, and the backend nulls both for non-operators.
  const slim = (r: RequestsResp | null): DashboardRequestsSlim | null =>
    r ? { summary: r.summary, by_day: r.by_day } : null;

  return (
    <DashboardBoard
      pool={pool}
      reqData={slim(reqData)}
      lifetimeData={slim(lifetimeData)}
      hourly={hourly}
      busy={busy}
      publicView={publicView}
    />
  );
}
