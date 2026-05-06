import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  Cell,
  Pie,
  PieChart,
  XAxis,
  YAxis,
} from "recharts";
import { Info } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { HourBucket, Pricing, PricingEntry, RequestAgg } from "@/legacy/lib/types";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  Tooltip as UITooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn, fmtInt } from "@/legacy/lib/utils";

const DAYS = 14;

// ----- shared chart configs -----

const tokenConfig: ChartConfig = {
  input: { label: "Input", theme: { light: "oklch(0.5 0.13 215)", dark: "oklch(0.8 0.16 145)" } },
  output: { label: "Output", theme: { light: "oklch(0.62 0.15 150)", dark: "oklch(0.72 0.14 215)" } },
  cacheR: { label: "Cache read", theme: { light: "oklch(0.68 0.15 70)", dark: "oklch(0.82 0.16 72)" } },
  cacheW: { label: "Cache write", theme: { light: "oklch(0.55 0.18 25)", dark: "oklch(0.68 0.2 25)" } },
};
const costConfig: ChartConfig = {
  cost_usd: { label: "Cost (USD)", theme: { light: "oklch(0.38 0.09 215)", dark: "oklch(0.82 0.16 145)" } },
};
const reqConfig: ChartConfig = {
  requests: { label: "Requests", theme: { light: "oklch(0.48 0.1 215)", dark: "oklch(0.72 0.14 215)" } },
};
const hourlyConfig: ChartConfig = {
  input: { label: "Input", theme: { light: "oklch(0.58 0.17 285)", dark: "oklch(0.75 0.17 285)" } },
  output: { label: "Output", theme: { light: "oklch(0.62 0.19 330)", dark: "oklch(0.78 0.17 330)" } },
  cacheR: { label: "Cache read", theme: { light: "oklch(0.72 0.16 55)", dark: "oklch(0.84 0.16 62)" } },
  cacheW: { label: "Cache write", theme: { light: "oklch(0.6 0.2 15)", dark: "oklch(0.72 0.2 15)" } },
};
const weekConfig: ChartConfig = {
  cost_usd: { label: "Cost (USD)", theme: { light: "oklch(0.45 0.13 260)", dark: "oklch(0.78 0.16 260)" } },
};
const monthConfig: ChartConfig = {
  cost_usd: { label: "Cost (USD)", theme: { light: "oklch(0.5 0.12 175)", dark: "oklch(0.8 0.15 175)" } },
};
const healthConfig: ChartConfig = {
  healthy: { label: "Healthy", theme: { light: "oklch(0.58 0.12 150)", dark: "oklch(0.78 0.16 145)" } },
  quota: { label: "Quota", theme: { light: "oklch(0.68 0.15 70)", dark: "oklch(0.82 0.16 72)" } },
  unhealthy: { label: "Unhealthy", theme: { light: "oklch(0.52 0.18 25)", dark: "oklch(0.68 0.2 25)" } },
  disabled: { label: "Disabled", theme: { light: "oklch(0.7 0.01 85)", dark: "oklch(0.5 0.01 260)" } },
};

// ----- helpers -----

function pad(n: number) {
  return String(n).padStart(2, "0");
}
const fmtDay = (d: string) => d.slice(5).replace("-", "/");

function fmtTokensCompact(n: number): string {
  if (!isFinite(n) || n <= 0) return "—";
  if (n >= 1_000_000_000) return (n / 1_000_000_000).toFixed(2) + "B";
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "k";
  return String(Math.round(n));
}

function isoWeekInfo(d: Date): { year: number; week: number; monday: string } {
  const dt = new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()));
  const day = dt.getUTCDay() || 7;
  dt.setUTCDate(dt.getUTCDate() + 4 - day);
  const year = dt.getUTCFullYear();
  const yearStart = new Date(Date.UTC(year, 0, 1));
  const week = Math.ceil(((dt.getTime() - yearStart.getTime()) / 86400000 + 1) / 7);
  const mon = new Date(dt);
  mon.setUTCDate(mon.getUTCDate() - 3);
  return {
    year,
    week,
    monday: `${mon.getUTCFullYear()}-${pad(mon.getUTCMonth() + 1)}-${pad(mon.getUTCDate())}`,
  };
}

function parseIsoDay(s: string): Date {
  const [y, m, d] = s.split("-").map((p) => Number(p));
  return new Date(Date.UTC(y, (m || 1) - 1, d || 1));
}

// ----- types -----

export interface DashboardRequestsSlim {
  summary: RequestAgg;
  by_client: Record<string, RequestAgg>;
  by_model: Record<string, RequestAgg>;
  by_day: Record<string, RequestAgg>;
}

export interface DashboardPool {
  total: number;
  healthy: number;
  quota: number;
  unhealthy: number;
  disabled: number;
}

export interface DashboardBoardProps {
  pool: DashboardPool | null;
  pricing?: Pricing;
  reqData: DashboardRequestsSlim | null;
  lifetimeData: DashboardRequestsSlim | null;
  hourly: HourBucket[] | null;
  busy?: boolean;
  /** When true, label Top clients with a "pseudonyms" hint/tooltip. */
  clientsAnonymized?: boolean;
  /**
   * Fleet-wide sum of what users actually paid us in USD (post per-group
   * multiplier). When provided, the "Saved" tile compares no-cache @
   * Anthropic-official-rate against this number rather than the upstream
   * cost — i.e. shows the savings from caching AND our discount combined.
   */
  userPaidUSD?: number | null;
}

// ----- component -----

export function DashboardBoard({
  pool,
  pricing,
  reqData,
  lifetimeData,
  hourly,
  busy = false,
  clientsAnonymized = false,
  userPaidUSD = null,
}: DashboardBoardProps) {
  const { t } = useTranslation();
  const lookupPrice = (model: string): PricingEntry | null => {
    if (!pricing) return null;
    const m = (model || "").toLowerCase().trim();
    const models = pricing.models || {};
    if (m && models[m]) return models[m];
    if (m) {
      for (let i = m.lastIndexOf("-"); i > 0; i = m.lastIndexOf("-", i - 1)) {
        const p = models[m.slice(0, i)];
        if (p) return p;
      }
    }
    return pricing.default || null;
  };

  const cacheStats = (() => {
    if (!lifetimeData) return null;
    const s = lifetimeData.summary;
    const input = s.input_tokens || 0;
    const cacheRead = s.cache_read_tokens || 0;
    const cacheCreate = s.cache_create_tokens || 0;
    const denom = input + cacheRead + cacheCreate;
    const hitRate = denom > 0 ? cacheRead / denom : 0;
    // What users actually paid us — the request_log emits cost_usd
    // POST-multiplier (see internal/server/proxy.go forward()), which is
    // the same value wallet_tx records on Charge(). Use this as the truth
    // for "users paid us"; the userPaidUSD prop from /wallet-totals is
    // only a sanity-check fallback.
    const usersPaid = s.cost_usd || 0;
    // Compute two reference points at OFFICIAL Anthropic rates from the
    // catalog (no multiplier, no SaaS discount):
    //   noCacheCost          — pretend caching never happened (input + cache
    //                          slots all priced as fresh input)
    //   officialWithCache    — real token mix priced with cache discount
    //                          (input_per_1m for input, cache_read_per_1m
    //                          for cache reads, cache_create_per_1m for
    //                          cache writes, output_per_1m for output)
    let noCacheCost = 0;
    let officialWithCache = 0;
    if (pricing) {
      for (const [name, a] of Object.entries(lifetimeData.by_model)) {
        const p = lookupPrice(name);
        if (!p) continue;
        const ain = a.input_tokens || 0;
        const acr = a.cache_read_tokens || 0;
        const acw = a.cache_create_tokens || 0;
        const aout = a.output_tokens || 0;
        noCacheCost += ((ain + acr + acw) * p.input_per_1m) / 1e6;
        noCacheCost += (aout * p.output_per_1m) / 1e6;
        officialWithCache += (ain * p.input_per_1m) / 1e6;
        officialWithCache += (acr * (p.cache_read_per_1m || p.input_per_1m * 0.1)) / 1e6;
        officialWithCache += (acw * (p.cache_create_per_1m || p.input_per_1m * 1.25)) / 1e6;
        officialWithCache += (aout * p.output_per_1m) / 1e6;
      }
    }
    const output = s.output_tokens || 0;
    const totalTokens = input + output + cacheRead + cacheCreate;
    // tokensPerDollar uses what the user actually paid — that's the
    // honest "tokens per buck" from the user's perspective.
    const tokensPerDollar = usersPaid > 0 ? totalTokens / usersPaid : 0;
    // Saved by us = official no-cache − user actually paid (covers BOTH
    // the caching benefit AND the multiplier discount).
    const savedByUs = Math.max(0, noCacheCost - usersPaid);
    // Cross-check: server-side wallet_tx aggregate. Should match usersPaid
    // for SaaS-only fleets; deviation hints at admin-token traffic that
    // bypassed the wallet ledger.
    const userPaidCrossCheck = typeof userPaidUSD === "number" && userPaidUSD > 0
      ? userPaidUSD : null;
    return {
      hitRate,
      usersPaid,
      noCacheCost,
      officialWithCache,
      userPaidCrossCheck,
      savedByUs,
      input,
      output,
      cacheRead,
      cacheCreate,
      totalTokens,
      tokensPerDollar,
      hasPricing: !!pricing,
    };
  })();

  // 14-day token throughput stacked area — derived from reqData.by_day so
  // this component doesn't need per-auth data.
  const trend = (() => {
    const today = new Date();
    today.setUTCHours(0, 0, 0, 0);
    const seed = new Map<
      string,
      { day: string; input: number; output: number; cacheR: number; cacheW: number; requests: number }
    >();
    for (let i = DAYS - 1; i >= 0; i--) {
      const d = new Date(today);
      d.setUTCDate(today.getUTCDate() - i);
      const key = `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
      seed.set(key, { day: key, input: 0, output: 0, cacheR: 0, cacheW: 0, requests: 0 });
    }
    if (reqData) {
      for (const [k, v] of Object.entries(reqData.by_day)) {
        const slot = seed.get(k);
        if (!slot) continue;
        slot.input = v.input_tokens || 0;
        slot.output = v.output_tokens || 0;
        slot.cacheR = v.cache_read_tokens || 0;
        slot.cacheW = v.cache_create_tokens || 0;
        slot.requests = v.count || 0;
      }
    }
    return Array.from(seed.values());
  })();

  const costSeries = (() => {
    const today = new Date();
    const seed = new Map<string, { day: string; cost_usd: number; requests: number }>();
    for (let i = DAYS - 1; i >= 0; i--) {
      const d = new Date(today);
      d.setDate(today.getDate() - i);
      const key = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
      seed.set(key, { day: key, cost_usd: 0, requests: 0 });
    }
    if (reqData) {
      for (const [k, v] of Object.entries(reqData.by_day)) {
        const slot = seed.get(k);
        if (slot) {
          slot.cost_usd = v.cost_usd;
          slot.requests = v.count;
        }
      }
    }
    return Array.from(seed.values());
  })();

  const hourlySeries = (() => {
    const out: {
      hour: string;
      label: string;
      input: number;
      output: number;
      cacheR: number;
      cacheW: number;
      cost_usd: number;
      requests: number;
    }[] = [];
    if (!hourly) return out;
    for (const b of hourly) {
      const dt = new Date(b.hour);
      const label = `${pad(dt.getHours())}:00`;
      out.push({
        hour: b.hour,
        label,
        input: b.input_tokens || 0,
        output: b.output_tokens || 0,
        cacheR: b.cache_read_tokens || 0,
        cacheW: b.cache_create_tokens || 0,
        cost_usd: b.cost_usd || 0,
        requests: b.count || 0,
      });
    }
    return out;
  })();

  const weekSeries = (() => {
    if (!lifetimeData) return [] as { key: string; label: string; monday: string; cost_usd: number; requests: number }[];
    const bucket = new Map<string, { label: string; monday: string; cost_usd: number; requests: number }>();
    for (const [day, agg] of Object.entries(lifetimeData.by_day)) {
      const info = isoWeekInfo(parseIsoDay(day));
      const key = `${info.year}-W${pad(info.week)}`;
      const cur = bucket.get(key) ?? {
        label: `W${info.week} · ${info.monday.slice(5)}`,
        monday: info.monday,
        cost_usd: 0,
        requests: 0,
      };
      cur.cost_usd += agg.cost_usd || 0;
      cur.requests += agg.count || 0;
      bucket.set(key, cur);
    }
    return Array.from(bucket.entries())
      .map(([key, v]) => ({ key, ...v }))
      .sort((a, b) => a.monday.localeCompare(b.monday));
  })();

  const monthSeries = (() => {
    if (!lifetimeData) return [] as { key: string; label: string; cost_usd: number; requests: number }[];
    const bucket = new Map<string, { cost_usd: number; requests: number }>();
    for (const [day, agg] of Object.entries(lifetimeData.by_day)) {
      const key = day.slice(0, 7);
      const cur = bucket.get(key) ?? { cost_usd: 0, requests: 0 };
      cur.cost_usd += agg.cost_usd || 0;
      cur.requests += agg.count || 0;
      bucket.set(key, cur);
    }
    return Array.from(bucket.entries())
      .map(([key, v]) => ({ key, label: key, ...v }))
      .sort((a, b) => a.key.localeCompare(b.key));
  })();

  const health = (() => {
    if (!pool) return [];
    return [
      { key: "healthy", label: "Healthy", value: pool.healthy },
      { key: "quota", label: "Quota", value: pool.quota },
      { key: "unhealthy", label: "Unhealthy", value: pool.unhealthy },
      { key: "disabled", label: "Disabled", value: pool.disabled },
    ].filter((x) => x.value > 0);
  })();

  if (!pool && !reqData && !lifetimeData) {
    return (
      <div className="py-16 text-center eyebrow animate-pulse bg-card border border-border-strong rounded-md">
        <span className="opacity-60">Loading telemetry…</span>
      </div>
    );
  }

  // Totals used to decide whether each chart has anything to draw.
  const trendTotal = trend.reduce((s, x) => s + x.input + x.output + x.cacheR + x.cacheW, 0);
  const hourlyTotal = hourlySeries.reduce((s, x) => s + x.input + x.output + x.cacheR + x.cacheW, 0);
  const costTotal = costSeries.reduce((s, x) => s + x.cost_usd, 0);
  const reqTotal = costSeries.reduce((s, x) => s + x.requests, 0);

  return (
    <div className="space-y-8">
      {/* Lifetime cache efficiency */}
      <section>
        <div className="flex items-baseline justify-between mb-3 gap-4">
          <div>
            <div className="eyebrow mb-1.5">{t("legacy.cacheEyebrow")}</div>
            <h3 className="font-display text-2xl md:text-3xl tracking-tight">
              {t("legacy.cacheTitleA")} <span className="text-muted-foreground">{t("legacy.cacheTitleB")}</span>
            </h3>
          </div>
          <span className="eyebrow tabular opacity-70 hidden sm:inline">{t("legacy.sinceFirst")}</span>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 md:gap-5">
          <CacheCard
            label={t("legacy.cacheHitRate")}
            value={cacheStats ? (cacheStats.hitRate * 100).toFixed(2) + "%" : busy ? "…" : "—"}
            ratio={cacheStats?.hitRate ?? 0}
            foot={
              cacheStats ? (
                <span className="mono tabular">
                  cacheR {fmtInt(cacheStats.cacheRead)} / (input {fmtInt(cacheStats.input)} + cacheR{" "}
                  {fmtInt(cacheStats.cacheRead)} + cacheW {fmtInt(cacheStats.cacheCreate)})
                </span>
              ) : null
            }
          />
          <CacheCard
            label={t("legacy.savedByUs")}
            value={
              cacheStats && cacheStats.hasPricing
                ? "$" + cacheStats.savedByUs.toFixed(2)
                : cacheStats && !cacheStats.hasPricing
                  ? t("legacy.pricingUnavailable")
                  : busy
                    ? "…"
                    : "—"
            }
            foot={
              cacheStats && cacheStats.hasPricing ? (
                <span className="mono tabular text-[10px] leading-relaxed block">
                  {t("legacy.noCacheOfficial", { n: cacheStats.noCacheCost.toFixed(2) })}
                  <br />
                  {t("legacy.withCacheOfficial", { n: cacheStats.officialWithCache.toFixed(2) })}
                  <br />
                  {t("legacy.actualPaid", { n: cacheStats.usersPaid.toFixed(2) })}
                </span>
              ) : null
            }
          />
          <CacheCard
            label={t("legacy.tokensPerDollar")}
            value={
              cacheStats && cacheStats.tokensPerDollar > 0
                ? fmtTokensCompact(cacheStats.tokensPerDollar)
                : cacheStats
                  ? "—"
                  : busy
                    ? "…"
                    : "—"
            }
            foot={
              cacheStats && cacheStats.tokensPerDollar > 0 ? (
                <span className="mono tabular">
                  {fmtInt(cacheStats.totalTokens)} tok / ${cacheStats.usersPaid.toFixed(2)}
                </span>
              ) : null
            }
          />
        </div>
      </section>

      {/* Token throughput — 14d area */}
      <section>
        <div className="flex items-baseline justify-between mb-3 gap-4">
          <div>
            <div className="eyebrow mb-1.5">{t("legacy.last14d")}</div>
            <h3 className="font-display text-2xl md:text-3xl tracking-tight">
              {t("legacy.tokenVolume")} <span className="text-muted-foreground">{t("legacy.byType")}</span>
            </h3>
          </div>
          <span className="eyebrow tabular opacity-70 hidden sm:inline">{fmtInt(trendTotal)} tok</span>
        </div>
        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
          {trendTotal === 0 ? (
            <ChartEmpty
              className="h-[240px] md:h-[280px] w-full"
              label="no token activity in the last 14 days"
              hint="waiting for the first request in this window"
            />
          ) : (
          <ChartContainer config={tokenConfig} className="h-[240px] md:h-[280px] aspect-auto w-full">
            <AreaChart data={trend} margin={{ top: 10, right: 12, left: -8, bottom: 0 }}>
              <defs>
                {(["input", "output", "cacheR", "cacheW"] as const).map((k) => (
                  <linearGradient key={k} id={`grad-${k}`} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={`var(--color-${k})`} stopOpacity={0.5} />
                    <stop offset="95%" stopColor={`var(--color-${k})`} stopOpacity={0} />
                  </linearGradient>
                ))}
              </defs>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="day" tickLine={false} axisLine={false} tickMargin={10} tickFormatter={fmtDay} minTickGap={16} />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={56}
                tickFormatter={(v: number) =>
                  v >= 1_000_000 ? `${(v / 1_000_000).toFixed(1)}M` : v >= 1000 ? `${Math.round(v / 1000)}k` : String(v)
                }
              />
              <ChartTooltip
                cursor={{ stroke: "var(--border)" }}
                content={
                  <ChartTooltipContent
                    indicator="dot"
                    labelFormatter={(v) => `Day · ${v}`}
                    valueFormatter={(v) => (typeof v === "number" ? v.toLocaleString() + " tok" : String(v))}
                  />
                }
              />
              <ChartLegend content={<ChartLegendContent />} />
              {(["cacheW", "cacheR", "output", "input"] as const).map((k) => (
                <Area
                  key={k}
                  type="monotone"
                  dataKey={k}
                  stackId="1"
                  stroke={`var(--color-${k})`}
                  fill={`url(#grad-${k})`}
                  strokeWidth={1.5}
                />
              ))}
            </AreaChart>
          </ChartContainer>
          )}
        </div>
      </section>

      {/* 24h hourly pulse */}
      <section>
        <div className="flex items-baseline justify-between mb-3 gap-4">
          <div>
            <div className="eyebrow mb-1.5">{t("legacy.pulse24")}</div>
            <h3 className="font-display text-2xl md:text-3xl tracking-tight">
              {t("legacy.liveRhythm")} <span className="text-muted-foreground">{t("legacy.rhythm")}</span>
            </h3>
          </div>
          <span className="eyebrow tabular opacity-70 hidden sm:inline">
            {fmtInt(hourlyTotal)} tok ·{" "}
            {fmtInt(hourlySeries.reduce((s, x) => s + x.requests, 0))} req
          </span>
        </div>
        <div className="relative overflow-hidden rounded-md border border-border-strong bg-gradient-to-br from-card via-card to-muted/30 p-4 md:p-5">
          <div
            aria-hidden
            className="pointer-events-none absolute -top-16 -right-16 h-48 w-48 rounded-full blur-3xl opacity-30"
            style={{ background: "var(--color-cacheW)" }}
          />
          {hourlyTotal === 0 ? (
            <ChartEmpty
              className="relative h-[220px] md:h-[260px] w-full"
              label="no traffic in the last 24 hours"
              hint="hourly buckets will light up as requests come in"
            />
          ) : (
          <ChartContainer config={hourlyConfig} className="relative h-[220px] md:h-[260px] aspect-auto w-full">
            <BarChart data={hourlySeries} margin={{ top: 10, right: 8, left: -8, bottom: 0 }} barCategoryGap={2}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} opacity={0.6} />
              <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={10} minTickGap={24} fontSize={11} />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={56}
                tickFormatter={(v: number) =>
                  v >= 1_000_000 ? `${(v / 1_000_000).toFixed(1)}M` : v >= 1000 ? `${Math.round(v / 1000)}k` : String(v)
                }
              />
              <ChartTooltip
                cursor={{ fill: "var(--muted)", opacity: 0.4 }}
                content={
                  <ChartTooltipContent
                    indicator="dot"
                    labelFormatter={(v) => `${v}`}
                    valueFormatter={(v) => (typeof v === "number" ? v.toLocaleString() + " tok" : String(v))}
                  />
                }
              />
              <ChartLegend content={<ChartLegendContent />} />
              {(["input", "output", "cacheR", "cacheW"] as const).map((k, i, arr) => (
                <Bar key={k} dataKey={k} stackId="h" fill={`var(--color-${k})`} radius={i === arr.length - 1 ? [3, 3, 0, 0] : 0} />
              ))}
            </BarChart>
          </ChartContainer>
          )}
        </div>
      </section>

      {/* Two-up: cost + requests */}
      <section className="grid grid-cols-1 lg:grid-cols-2 gap-4 md:gap-5">
        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
          <div className="flex items-baseline justify-between mb-3 gap-2">
            <div>
              <div className="eyebrow mb-1">{t("legacy.dailyCost")}</div>
              <h3 className="font-display text-xl tracking-tight">
                {t("legacy.spend")} <span className="text-muted-foreground">· {t("legacy.spend14d")}</span>
              </h3>
            </div>
            <span className="eyebrow tabular opacity-70">${costTotal.toFixed(2)}</span>
          </div>
          {costTotal === 0 ? (
            <ChartEmpty
              className="h-[220px] w-full"
              label="no spend in the last 14 days"
            />
          ) : (
          <ChartContainer config={costConfig} className={cn("h-[220px] aspect-auto w-full", busy && "opacity-70")}>
            <BarChart data={costSeries} margin={{ top: 8, right: 4, left: -12, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="day" tickLine={false} axisLine={false} tickMargin={8} tickFormatter={fmtDay} minTickGap={20} />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={48}
                tickFormatter={(v: number) => `$${v < 1 ? v.toFixed(2) : Math.round(v)}`}
              />
              <ChartTooltip
                cursor={{ fill: "var(--muted)", opacity: 0.5 }}
                content={
                  <ChartTooltipContent
                    indicator="dot"
                    labelFormatter={(v) => `Day · ${v}`}
                    valueFormatter={(v) => (typeof v === "number" ? `$${v.toFixed(4)}` : String(v))}
                  />
                }
              />
              <Bar dataKey="cost_usd" fill="var(--color-cost_usd)" radius={[3, 3, 0, 0]} />
            </BarChart>
          </ChartContainer>
          )}
        </div>

        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
          <div className="flex items-baseline justify-between mb-3 gap-2">
            <div>
              <div className="eyebrow mb-1">{t("legacy.dailyReq")}</div>
              <h3 className="font-display text-xl tracking-tight">
                {t("legacy.traffic14d")}
              </h3>
            </div>
            <span className="eyebrow tabular opacity-70">{fmtInt(reqTotal)} req</span>
          </div>
          {reqTotal === 0 ? (
            <ChartEmpty
              className="h-[220px] w-full"
              label="no requests in the last 14 days"
            />
          ) : (
          <ChartContainer config={reqConfig} className="h-[220px] aspect-auto w-full">
            <LineChart data={costSeries} margin={{ top: 8, right: 8, left: -12, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="day" tickLine={false} axisLine={false} tickMargin={8} tickFormatter={fmtDay} minTickGap={20} />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={48}
                tickFormatter={(v: number) => (v >= 1000 ? `${Math.round(v / 1000)}k` : String(Math.round(v)))}
              />
              <ChartTooltip
                cursor={{ stroke: "var(--border)" }}
                content={
                  <ChartTooltipContent
                    indicator="dot"
                    labelFormatter={(v) => `Day · ${v}`}
                    valueFormatter={(v) => (typeof v === "number" ? v.toLocaleString() + " req" : String(v))}
                  />
                }
              />
              <Line
                type="monotone"
                dataKey="requests"
                stroke="var(--color-requests)"
                strokeWidth={2}
                dot={{ r: 3, strokeWidth: 0, fill: "var(--color-requests)" }}
                activeDot={{ r: 5 }}
              />
            </LineChart>
          </ChartContainer>
          )}
        </div>
      </section>

      {/* Three-up: health mix + top models + top clients */}
      <section className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 md:gap-5">
        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5 md:col-span-2 lg:col-span-1">
          <div className="mb-4">
            <div className="eyebrow mb-1">{t("legacy.poolHealth")}</div>
            <h3 className="font-display text-xl tracking-tight">
              <span className="text-muted-foreground">{t("legacy.poolCount", { n: pool?.total ?? 0 })}</span>
            </h3>
          </div>
          {health.length === 0 ? (
            <ChartEmpty className="h-[220px] w-full" label="no credentials configured" />
          ) : (
            <ChartContainer config={healthConfig} className="h-[220px] aspect-auto w-full">
              <PieChart>
                <ChartTooltip content={<ChartTooltipContent hideLabel indicator="dot" />} />
                <Pie
                  data={health}
                  dataKey="value"
                  nameKey="key"
                  innerRadius={55}
                  outerRadius={85}
                  strokeWidth={2}
                  stroke="var(--card)"
                  paddingAngle={3}
                >
                  {health.map((h) => (
                    <Cell key={h.key} fill={`var(--color-${h.key})`} />
                  ))}
                </Pie>
                <ChartLegend content={<ChartLegendContent />} />
              </PieChart>
            </ChartContainer>
          )}
        </div>

        <TopList
          title={t("legacy.topModels")}
          sub={t("legacy.topModelsBy")}
          rows={
            reqData
              ? Object.entries(reqData.by_model)
                  .sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
                  .slice(0, 6)
                  .map(([k, v]) => ({ k, v: v.cost_usd, meta: `${fmtInt(v.count)} req`, fmt: "cost" as const }))
              : []
          }
        />

        <TopList
          title={t("legacy.topClients")}
          sub={t("legacy.topModelsBy")}
          titleAdornment={clientsAnonymized ? <PseudonymHint /> : undefined}
          rows={
            reqData
              ? Object.entries(reqData.by_client)
                  .sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
                  .slice(0, 6)
                  .map(([k, v]) => ({
                    k: k || "(unnamed)",
                    v: v.cost_usd,
                    meta: `${fmtInt(v.count)} req`,
                    fmt: "cost" as const,
                  }))
              : []
          }
        />
      </section>

      {/* Lifetime billing-week + calendar-month spend */}
      <section className="grid grid-cols-1 lg:grid-cols-2 gap-4 md:gap-5">
        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
          <div className="flex items-baseline justify-between mb-3 gap-2">
            <div>
              <div className="eyebrow mb-1">{t("legacy.billingWeek")}</div>
              <h3 className="font-display text-xl tracking-tight">
                {t("legacy.weekly")} <span className="text-muted-foreground">{t("legacy.spend")}</span>
              </h3>
            </div>
            <span className="eyebrow tabular opacity-70">
              ${weekSeries.reduce((s, x) => s + x.cost_usd, 0).toFixed(2)} · {weekSeries.length}w
            </span>
          </div>
          {weekSeries.length === 0 ? (
            <ChartEmpty
              className="h-[240px] w-full"
              label="no weekly history yet"
              hint="aggregates by ISO week (Mon → Sun)"
            />
          ) : (
            <ChartContainer config={weekConfig} className="h-[240px] aspect-auto w-full">
              <BarChart data={weekSeries} margin={{ top: 8, right: 4, left: -10, bottom: 0 }}>
                <defs>
                  <linearGradient id="grad-week" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="var(--color-cost_usd)" stopOpacity={0.95} />
                    <stop offset="95%" stopColor="var(--color-cost_usd)" stopOpacity={0.45} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={24} fontSize={11} />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  width={52}
                  tickFormatter={(v: number) => (v < 1 ? `$${v.toFixed(2)}` : `$${Math.round(v)}`)}
                />
                <ChartTooltip
                  cursor={{ fill: "var(--muted)", opacity: 0.5 }}
                  content={
                    <ChartTooltipContent
                      indicator="dot"
                      labelFormatter={(v, p) => {
                        const row = p?.[0]?.payload as { monday?: string } | undefined;
                        return row?.monday ? `Week of ${row.monday}` : String(v);
                      }}
                      valueFormatter={(v) => (typeof v === "number" ? `$${v.toFixed(2)}` : String(v))}
                    />
                  }
                />
                <Bar dataKey="cost_usd" fill="url(#grad-week)" radius={[4, 4, 0, 0]} />
              </BarChart>
            </ChartContainer>
          )}
        </div>

        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
          <div className="flex items-baseline justify-between mb-3 gap-2">
            <div>
              <div className="eyebrow mb-1">{t("legacy.calendarMonth")}</div>
              <h3 className="font-display text-xl tracking-tight">
                {t("legacy.monthly")} <span className="text-muted-foreground">{t("legacy.spend")}</span>
              </h3>
            </div>
            <span className="eyebrow tabular opacity-70">
              ${monthSeries.reduce((s, x) => s + x.cost_usd, 0).toFixed(2)} · {monthSeries.length}mo
            </span>
          </div>
          {monthSeries.length === 0 ? (
            <ChartEmpty className="h-[240px] w-full" label="no monthly history yet" />
          ) : (
            <ChartContainer config={monthConfig} className="h-[240px] aspect-auto w-full">
              <AreaChart data={monthSeries} margin={{ top: 8, right: 8, left: -10, bottom: 0 }}>
                <defs>
                  <linearGradient id="grad-month" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="var(--color-cost_usd)" stopOpacity={0.55} />
                    <stop offset="95%" stopColor="var(--color-cost_usd)" stopOpacity={0.02} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={24} fontSize={11} />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  width={52}
                  tickFormatter={(v: number) => (v < 1 ? `$${v.toFixed(2)}` : `$${Math.round(v)}`)}
                />
                <ChartTooltip
                  cursor={{ stroke: "var(--border)" }}
                  content={
                    <ChartTooltipContent
                      indicator="dot"
                      labelFormatter={(v) => `Month · ${v}`}
                      valueFormatter={(v) => (typeof v === "number" ? `$${v.toFixed(2)}` : String(v))}
                    />
                  }
                />
                <Area
                  type="monotone"
                  dataKey="cost_usd"
                  stroke="var(--color-cost_usd)"
                  strokeWidth={2}
                  fill="url(#grad-month)"
                  dot={{ r: 3, strokeWidth: 0, fill: "var(--color-cost_usd)" }}
                  activeDot={{ r: 5 }}
                />
              </AreaChart>
            </ChartContainer>
          )}
        </div>
      </section>
    </div>
  );
}

// ChartEmpty renders a muted placeholder sized to the chart's slot so the
// layout doesn't jump when a window has no traffic. Callers pass the same
// height class they'd use on the real ChartContainer.
function ChartEmpty({
  className,
  label = "no traffic in this window",
  hint,
}: {
  className?: string;
  label?: string;
  hint?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center text-center gap-1 rounded-sm border border-dashed border-border/60 bg-muted/10",
        className,
      )}
    >
      <span className="eyebrow opacity-70">{label}</span>
      {hint && <span className="text-[11px] text-muted-foreground mono">{hint}</span>}
    </div>
  );
}

function CacheCard({
  label,
  value,
  foot,
  ratio,
}: {
  label: string;
  value: string;
  foot?: React.ReactNode;
  ratio?: number;
}) {
  const pct = typeof ratio === "number" ? Math.round(Math.max(0, Math.min(1, ratio)) * 100) : null;
  const bar =
    pct == null
      ? "bg-muted-foreground/40"
      : pct >= 60
        ? "bg-emerald-500"
        : pct >= 30
          ? "bg-amber-500"
          : "bg-slate-400";
  return (
    <div className="bg-card border border-border-strong rounded-md p-5">
      <div className="eyebrow mb-1.5">{label}</div>
      <div className="font-display text-3xl md:text-4xl tracking-tight tabular">{value}</div>
      {pct != null && (
        <div className="mt-3 h-1.5 w-full bg-muted rounded-full overflow-hidden">
          <div className={cn("h-full transition-all", bar)} style={{ width: `${pct}%` }} />
        </div>
      )}
      {foot && <div className="mt-2 text-[11px] text-muted-foreground">{foot}</div>}
    </div>
  );
}

function TopList({
  title,
  sub,
  rows,
  titleAdornment,
}: {
  title: string;
  sub: string;
  rows: { k: string; v: number; meta: string; fmt: "cost" | "int" }[];
  titleAdornment?: React.ReactNode;
}) {
  const max = Math.max(1e-9, ...rows.map((r) => r.v));
  return (
    <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
      <div className="mb-3">
        <div className="eyebrow mb-1 flex items-center gap-1.5">
          <span>{title}</span>
          {titleAdornment}
        </div>
        <h3 className="font-display text-xl tracking-tight">
          <span className="text-muted-foreground">{sub}</span>
        </h3>
      </div>
      {rows.length === 0 ? (
        <ChartEmpty className="h-[180px] w-full" label="no entries to rank" />
      ) : (
        <ul className="space-y-2.5">
          {rows.map((r) => (
            <li key={r.k} className="group">
              <div className="flex items-baseline justify-between gap-3 text-sm mb-1">
                <span className="mono text-xs truncate flex-1" title={r.k}>
                  {r.k}
                </span>
                <span className="mono tabular font-medium shrink-0">
                  {r.fmt === "cost" ? `$${r.v.toFixed(4)}` : fmtInt(r.v)}
                </span>
                <span className="eyebrow opacity-60 tabular shrink-0">{r.meta}</span>
              </div>
              <div className="h-1 w-full bg-muted rounded-full overflow-hidden">
                <div className="h-full bg-primary/70 group-hover:bg-primary transition-all" style={{ width: `${Math.round((r.v / max) * 100)}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// Small badge shown next to "Top clients" on the public status page to
// clarify that names like "Alice/Bob" are stable pseudonyms, not real
// customer identities. Hover reveals the fuller explanation.
function PseudonymHint() {
  return (
    <TooltipProvider delayDuration={150}>
      <UITooltip>
        <TooltipTrigger asChild>
          <span
            className="inline-flex items-center gap-0.5 rounded-sm border border-border/60 bg-muted/40 px-1 py-0.5 text-[9px] font-medium tracking-wider uppercase text-muted-foreground cursor-help"
            aria-label="Pseudonyms"
          >
            <Info className="h-2.5 w-2.5" />
            pseudonym
          </span>
        </TooltipTrigger>
        <TooltipContent side="top" className="max-w-[260px] text-xs leading-relaxed">
          Client names shown here (Alice / Bob / …) are deterministic pseudonyms, not real user
          identities. Real token labels are never exposed on the public page.
        </TooltipContent>
      </UITooltip>
    </TooltipProvider>
  );
}
