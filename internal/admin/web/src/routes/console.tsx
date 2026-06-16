import { Activity, Globe, Info, RefreshCw, User } from "lucide-react";
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
import { cn, fmtDate } from "@/legacy/lib/utils";
import { apiGet } from "@/lib/api";
import { errMsg, fmtCompact, fmtUSD } from "@/lib/utils";

// PersonalAgg mirrors cc-core requestlog.Aggregate (snake_case JSON). All
// counters are this user's own — never fleet-wide.
interface PersonalAgg {
  count: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  cost_usd: number;
  errors: number;
  total_duration_ms: number;
}
// PersonalConsole is the /api/v2/me/console payload: account-level aggregates
// for the "个人" tab. `total` is all-time, `today` is the current UTC day.
interface PersonalConsole {
  total: PersonalAgg;
  today: PersonalAgg;
  today_key: string;
  by_model: Record<string, PersonalAgg>;
  balance_usd: number;
}

const ZERO_AGG: PersonalAgg = {
  count: 0,
  input_tokens: 0,
  output_tokens: 0,
  cache_read_tokens: 0,
  cache_create_tokens: 0,
  cost_usd: 0,
  errors: 0,
  total_duration_ms: 0,
};

const aggTotalTokens = (a: PersonalAgg | undefined): number =>
  a ? a.input_tokens + a.output_tokens + a.cache_read_tokens + a.cache_create_tokens : 0;

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

// HealthCell renders the coarse service-health badge that replaces the old
// credential-count tile. It exposes only "can the service serve right now",
// never the underlying pool size or composition.
function HealthCell({ health }: { health?: "operational" | "down" }) {
  const { t } = useTranslation();
  const ok = health === "operational";
  const pending = health == null; // summary not loaded yet — stay neutral
  return (
    <SpotlightCard tiltDeg={0} className="w-full rounded-xl p-4 ring-1 ring-primary/30">
      <div className="text-[11px] uppercase tracking-wider text-muted-foreground">
        {t("console.metrics.serviceHealth")}
      </div>
      <div className="mt-2 flex items-center gap-2">
        <span className="relative inline-flex h-2.5 w-2.5">
          {ok && (
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-500 opacity-75" />
          )}
          <span
            className={cn(
              "relative inline-flex h-2.5 w-2.5 rounded-full",
              pending ? "bg-muted-foreground/40" : ok ? "bg-emerald-500" : "bg-destructive",
            )}
          />
        </span>
        <span
          className={cn(
            "font-mono text-xl md:text-2xl leading-none font-semibold",
            pending ? "text-muted-foreground" : ok ? "text-emerald-500" : "text-destructive",
          )}
        >
          {pending ? "···" : ok ? t("console.metrics.operational") : t("console.metrics.down")}
        </span>
      </div>
    </SpotlightCard>
  );
}

export default function ConsolePage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  // view selects the statistics scope: platform-wide aggregate (default) vs
  // this account's own usage. The two pull from different endpoints.
  const [view, setView] = useState<"platform" | "personal">("platform");
  const [data, setData] = useState<Summary | null>(null);
  const [personal, setPersonal] = useState<PersonalConsole | null>(null);
  const [err, setErr] = useState("");
  const [lastTick, setLastTick] = useState(Date.now());
  const [refreshTick, setRefreshTick] = useState(0);
  const [refreshing, setRefreshing] = useState(false);

  const refresh = useCallback(async () => {
    try {
      if (view === "personal") {
        const p = await apiGet<PersonalConsole>("/me/console");
        setPersonal(p);
      } else {
        const d = await api<Summary>("/admin/api/summary");
        setData(d);
      }
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
  }, [view]);

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

  // Usage KPIs + a single service-health verb — sourced from the redacted
  // aggregate the backend computes, never from per-credential rows. We
  // intentionally surface usage (the bridge users care about) and a coarse
  // health badge, but no credential / OAuth / API-key counts.
  const health = data?.service_health;
  const tot = data?.usage_totals?.total;
  const h24 = data?.usage_totals?.sum_24h;
  const req24 = h24?.requests || 0;
  const in24 = h24?.input_tokens || 0;
  const out24 = h24?.output_tokens || 0;
  const totalTokens = tot
    ? (tot.input_tokens || 0) +
      (tot.output_tokens || 0) +
      (tot.cache_read_tokens || 0) +
      (tot.cache_create_tokens || 0)
    : 0;

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
              {view === "personal" ? (
                <User className="inline-block h-7 w-7 align-[-3px] text-primary" />
              ) : (
                <Activity className="inline-block h-7 w-7 align-[-3px] text-primary" />
              )}{" "}
              {view === "personal" ? t("console.titlePersonal") : t("console.titlePlatform")}
            </h1>
            <p className="text-sm lg:text-base text-muted-foreground max-w-2xl">
              {view === "personal"
                ? t("console.subPersonal")
                : t("console.activeWindow", { min: data ? data.active_window_minutes : "···" })}
            </p>
            <ConsoleTabs view={view} onChange={setView} />
            <div className="flex items-start gap-2 rounded-md border border-primary/30 bg-primary/[0.05] px-3 py-2 max-w-2xl">
              <Info className="h-4 w-4 shrink-0 text-primary mt-0.5" />
              <p className="text-xs text-muted-foreground leading-relaxed">
                <span className="text-foreground font-medium">
                  {view === "personal"
                    ? t("console.bannerStrongPersonal")
                    : t("console.bannerStrong")}
                </span>{" "}
                {view === "personal" ? (
                  t("console.bannerPersonal")
                ) : (
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
                )}
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

      {view === "platform" ? (
        <>
          {/* Platform KPI strip — a single coarse service-health badge plus
              usage metrics. Deliberately no credential / OAuth / API-key
              counts: fleet pool size is operator-internal and not exposed.
              fmtCompact keeps 9–10-digit token totals inside the tile. */}
          <RevealStagger className="grid gap-3 grid-cols-2 sm:grid-cols-3 lg:grid-cols-5">
            <RevealItem className="flex">
              <HealthCell health={health} />
            </RevealItem>
            <RevealItem className="flex">
              <MetricCell label={t("console.metrics.requests24h")} value={fmtCompact(req24)} />
            </RevealItem>
            <RevealItem className="flex">
              <MetricCell
                label={t("console.metrics.tokensIn24h")}
                value={fmtCompact(in24)}
                unit={t("console.metrics.tok")}
              />
            </RevealItem>
            <RevealItem className="flex">
              <MetricCell
                label={t("console.metrics.tokensOut24h")}
                value={fmtCompact(out24)}
                unit={t("console.metrics.tok")}
              />
            </RevealItem>
            <RevealItem className="flex">
              <MetricCell
                label={t("console.metrics.tokensTotal")}
                value={fmtCompact(totalTokens)}
                unit={t("console.metrics.tok")}
              />
            </RevealItem>
          </RevealStagger>

          {/* The original Overview panel — charts + fleet health. */}
          <Reveal>
            <OverviewPanel summary={data} pricing={data?.pricing} refreshTick={refreshTick} />
          </Reveal>
        </>
      ) : (
        <PersonalView personal={personal} />
      )}
    </div>
  );
}

// ConsoleTabs — pill switch between the platform-wide and personal scopes.
function ConsoleTabs({
  view,
  onChange,
}: {
  view: "platform" | "personal";
  onChange: (v: "platform" | "personal") => void;
}) {
  const { t } = useTranslation();
  const tabs = [
    { id: "platform" as const, label: t("console.tabs.platform"), icon: Globe },
    { id: "personal" as const, label: t("console.tabs.personal"), icon: User },
  ];
  return (
    <div className="inline-flex items-center gap-1 rounded-full border border-border bg-card/50 p-1">
      {tabs.map((tab) => {
        const Icon = tab.icon;
        const active = view === tab.id;
        return (
          <button
            type="button"
            key={tab.id}
            onClick={() => onChange(tab.id)}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-full px-4 py-1.5 text-sm font-medium transition-colors",
              active
                ? "bg-primary text-primary-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Icon className="h-3.5 w-3.5" />
            {tab.label}
          </button>
        );
      })}
    </div>
  );
}

// PersonalView renders account-scoped statistics: the same KPI-tile layout as
// the platform view but driven by this user's own request log, plus a
// cumulative summary row and a per-model usage breakdown. All numbers are this
// account's — never fleet-wide.
function PersonalView({ personal }: { personal: PersonalConsole | null }) {
  const { t } = useTranslation();
  const today = personal?.today ?? ZERO_AGG;
  const total = personal?.total ?? ZERO_AGG;
  const cacheDenom = total.input_tokens + total.cache_read_tokens + total.cache_create_tokens;
  const cacheHit = cacheDenom > 0 ? (total.cache_read_tokens / cacheDenom) * 100 : 0;
  const models = personal
    ? Object.entries(personal.by_model).sort((a, b) => aggTotalTokens(b[1]) - aggTotalTokens(a[1]))
    : [];
  return (
    <>
      {/* KPI strip — wallet balance + today's traffic + all-time tokens. */}
      <RevealStagger className="grid gap-3 grid-cols-2 sm:grid-cols-3 lg:grid-cols-5">
        <RevealItem className="flex">
          <MetricCell
            label={t("console.metrics.balance")}
            value={fmtUSD(personal?.balance_usd)}
            accent
          />
        </RevealItem>
        <RevealItem className="flex">
          <MetricCell label={t("console.metrics.requestsToday")} value={fmtCompact(today.count)} />
        </RevealItem>
        <RevealItem className="flex">
          <MetricCell
            label={t("console.metrics.tokensInToday")}
            value={fmtCompact(today.input_tokens)}
            unit={t("console.metrics.tok")}
          />
        </RevealItem>
        <RevealItem className="flex">
          <MetricCell
            label={t("console.metrics.tokensOutToday")}
            value={fmtCompact(today.output_tokens)}
            unit={t("console.metrics.tok")}
          />
        </RevealItem>
        <RevealItem className="flex">
          <MetricCell
            label={t("console.metrics.tokensTotal")}
            value={fmtCompact(aggTotalTokens(total))}
            unit={t("console.metrics.tok")}
          />
        </RevealItem>
      </RevealStagger>

      {/* Cumulative all-time summary. */}
      <Reveal>
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <MetricCell label={t("console.personal.totalRequests")} value={fmtCompact(total.count)} />
          <MetricCell
            label={t("console.personal.totalSpent")}
            value={fmtUSD(total.cost_usd)}
            accent
          />
          <MetricCell label={t("console.personal.cacheHit")} value={cacheHit.toFixed(1)} unit="%" />
          <MetricCell label={t("console.personal.errors")} value={fmtCompact(total.errors)} />
        </div>
      </Reveal>

      {/* Per-model usage breakdown. */}
      <Reveal>
        <SpotlightCard tiltDeg={0} className="rounded-xl p-5">
          <h3 className="text-sm font-semibold tracking-tight">{t("console.personal.byModel")}</h3>
          {models.length === 0 ? (
            <p className="mt-4 text-sm text-muted-foreground">{t("console.personal.noData")}</p>
          ) : (
            <div className="mt-4 overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border text-left text-xs uppercase tracking-wider text-muted-foreground">
                    <th className="pb-2 pr-4 font-medium">{t("console.personal.colModel")}</th>
                    <th className="pb-2 pr-4 text-right font-medium">
                      {t("console.personal.colRequests")}
                    </th>
                    <th className="pb-2 pr-4 text-right font-medium">
                      {t("console.personal.colTokens")}
                    </th>
                    <th className="pb-2 text-right font-medium">{t("console.personal.colCost")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border/60">
                  {models.map(([model, agg]) => (
                    <tr key={model}>
                      <td className="py-2 pr-4 font-mono text-xs">{model}</td>
                      <td className="py-2 pr-4 text-right font-mono tabular-nums">
                        {fmtCompact(agg.count)}
                      </td>
                      <td className="py-2 pr-4 text-right font-mono tabular-nums">
                        {fmtCompact(aggTotalTokens(agg))}
                      </td>
                      <td className="py-2 text-right font-mono tabular-nums">
                        {fmtUSD(agg.cost_usd)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </SpotlightCard>
      </Reveal>
    </>
  );
}
