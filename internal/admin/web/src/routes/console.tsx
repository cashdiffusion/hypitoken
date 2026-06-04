import { Activity, Info, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link, Navigate } from "react-router-dom";
import { SpotlightCard } from "@/components/landing/interactions";
import { Reveal, RevealItem, RevealStagger } from "@/components/landing/reveal";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/hooks/use-auth";
import { OverviewPanel } from "@/legacy/components/overview-panel";
import { ApiError, api } from "@/legacy/lib/api";
import type { Summary } from "@/legacy/lib/types";
import { cn, fmtDate, fmtInt } from "@/legacy/lib/utils";
import { errMsg } from "@/lib/utils";

// /app/console exposes only the original CPA-Claude OVERVIEW panel —
// charts and fleet KPIs — wrapped in the SaaS shell. Visible to any
// signed-in user since the underlying /admin/api/summary endpoint
// is GET-only and the SSO bridge admits any authenticated user. No
// credential CRUD, no token management, no requests explorer here —
// those tabs are intentionally dropped.

// MetricCell renders one tile in the KPI strip. Plain Tailwind — the
// legacy `hud-strip-grid` / `metric-cell` classes lived in CPA-Claude's
// globals.css and weren't ported over (they used Tailwind v3 utilities).
// This shadcn-style card matches the rest of the SaaS shell and lays out
// horizontally inside the parent grid.
function MetricCell({
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
      className={cn("w-full rounded-xl p-4", accent && "ring-1 ring-primary/30")}
    >
      <div className="text-[11px] uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className="mt-2 flex items-baseline gap-1.5">
        <span
          className={cn(
            "font-mono text-2xl md:text-3xl leading-none font-semibold tabular-nums",
            accent ? "text-primary" : "text-foreground",
          )}
        >
          {value}
        </span>
        {unit && (
          <span className="font-mono text-xs text-muted-foreground uppercase tracking-wider">
            {unit}
          </span>
        )}
      </div>
      {hint && (
        <div className="mt-2 text-[11px] font-mono text-muted-foreground tabular-nums">{hint}</div>
      )}
    </SpotlightCard>
  );
}

export default function ConsolePage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const [data, setData] = useState<Summary | null>(null);
  const [err, setErr] = useState("");
  const [lastTick, setLastTick] = useState(Date.now());
  const [refreshTick, setRefreshTick] = useState(0);
  const [refreshing, setRefreshing] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const d = await api<Summary>("/admin/api/summary");
      setData(d);
      setErr("");
      setLastTick(Date.now());
    } catch (x) {
      if (x instanceof ApiError && x.status === 401) {
        // SSO failed — JWT expired. Bounce to login.
        window.location.href = "/login";
        return;
      }
      setErr(errMsg(x, "fetch failed"));
    }
  }, []);

  const manualRefresh = useCallback(async () => {
    setRefreshing(true);
    await refresh();
    setRefreshTick((t) => t + 1);
    setTimeout(() => setRefreshing(false), 500);
  }, [refresh]);

  useEffect(() => {
    refresh();
    // 10s poll, paused while the tab is hidden so we don't burn cycles
    // refreshing dashboards no one is watching.
    const tick = () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      refresh();
    };
    const t = setInterval(tick, 10000);
    const onVisible = () => {
      if (document.visibilityState === "visible") refresh();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(t);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [refresh]);

  if (!user) return <Navigate to="/login" replace />;

  // Aggregate KPIs the original masthead displays.
  const auths = data?.auths || [];
  const oauths = auths.filter((a) => a.kind === "oauth");
  const apikeys = auths.filter((a) => a.kind === "apikey");
  const totalCreds = auths.length;
  const healthyCreds = auths.filter((a) => a.healthy).length;
  const totals = { in24: 0, out: 0 };
  for (const a of auths) {
    const t = a.usage?.total;
    if (t) totals.out += t.output_tokens || 0;
    const h24 = a.usage?.sum_24h;
    if (h24) totals.in24 += h24.input_tokens || 0;
  }

  return (
    <div className="space-y-8">
      {/* Masthead — slimmed: title + active rotation + manual refresh */}
      <header className="space-y-5">
        <div className="eyebrow flex items-center gap-2.5">
          <span className="relative inline-flex h-2 w-2">
            <span className="absolute inline-flex h-full w-full rounded-full bg-primary opacity-75 animate-ping" />
            <span className="relative inline-flex rounded-full h-2 w-2 bg-primary" />
          </span>
          <span>{t("console.eyebrow")}</span>
        </div>

        <div className="flex flex-col lg:flex-row lg:items-end lg:justify-between gap-5">
          <div className="space-y-2.5 max-w-3xl">
            <h1 className="font-display text-3xl sm:text-4xl lg:text-5xl leading-[0.95] tracking-tight">
              <Activity className="inline-block h-7 w-7 align-[-3px] text-primary" />{" "}
              {t("console.title")}
            </h1>
            <p className="text-sm lg:text-base text-muted-foreground max-w-2xl">
              {t("console.activeWindow", { min: data ? data.active_window_minutes : "···" })}
            </p>
            <div className="flex items-start gap-2 rounded-md border border-primary/30 bg-primary/[0.05] px-3 py-2 max-w-2xl">
              <Info className="h-4 w-4 shrink-0 text-primary mt-0.5" />
              <p className="text-xs text-muted-foreground leading-relaxed">
                <span className="text-foreground font-medium">{t("console.bannerStrong")}</span>{" "}
                <Trans
                  i18nKey="console.banner"
                  components={{
                    billing: (
                      <Link
                        to="/app/billing"
                        className="underline underline-offset-2 text-foreground hover:text-primary"
                      />
                    ),
                    logs: (
                      <Link
                        to="/app/logs"
                        className="underline underline-offset-2 text-foreground hover:text-primary"
                      />
                    ),
                  }}
                />
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2 lg:justify-end">
            <Button
              variant="outline"
              onClick={manualRefresh}
              className="gap-2"
              aria-label={t("common.refresh")}
            >
              <RefreshCw
                className={cn("h-4 w-4 transition-transform", refreshing && "animate-spin")}
              />
              <span className="hidden sm:inline">{t("common.refresh")}</span>
            </Button>
            <span className="eyebrow tabular opacity-60 hidden md:inline">
              {t("console.last", { when: fmtDate(new Date(lastTick).toISOString()) })}
            </span>
          </div>
        </div>

        {err && (
          <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive font-mono">
            {err}
          </div>
        )}
      </header>

      {/* KPI strip — same five metrics the original Overview leads with,
          laid out as a real horizontal grid (2 cols on mobile, 3 on small
          screens, 5 across on lg+). Each cell is a self-contained card
          using the same border/bg vocabulary as the SaaS dashboard. */}
      <RevealStagger className="grid gap-3 grid-cols-2 sm:grid-cols-3 lg:grid-cols-5">
        <RevealItem className="flex">
          <MetricCell
            label={t("console.metrics.credentials")}
            value={`${healthyCreds}`}
            unit={`/ ${totalCreds}`}
            hint={t("console.metrics.health")}
            accent
          />
        </RevealItem>
        <RevealItem className="flex">
          <MetricCell label={t("console.metrics.oauth")} value={fmtInt(oauths.length)} />
        </RevealItem>
        <RevealItem className="flex">
          <MetricCell label={t("console.metrics.apiKeys")} value={fmtInt(apikeys.length)} />
        </RevealItem>
        <RevealItem className="flex">
          <MetricCell
            label={t("console.metrics.sumIn24h")}
            value={fmtInt(totals.in24)}
            unit={t("console.metrics.tok")}
          />
        </RevealItem>
        <RevealItem className="flex">
          <MetricCell
            label={t("console.metrics.sumOut")}
            value={fmtInt(totals.out)}
            unit={t("console.metrics.tok")}
          />
        </RevealItem>
      </RevealStagger>

      {/* The original Overview panel — charts + fleet health visualisation. */}
      <Reveal>
        <OverviewPanel summary={data} pricing={data?.pricing} refreshTick={refreshTick} />
      </Reveal>
    </div>
  );
}
