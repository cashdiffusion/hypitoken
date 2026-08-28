import { EyeOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart,
  XAxis,
} from "recharts";
import {
  type ChartConfig,
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import type { HourBucket, RequestAgg } from "@/legacy/lib/types";
import { cn } from "@/legacy/lib/utils";

const DAYS = 14;

// This board is the platform-wide tab of /app/console, which every signed-in
// customer can open. Fleet request volume and fleet revenue are the business
// itself, so nothing here prints a magnitude: no axis ticks, no tooltips, no
// running totals in the section headers, no per-model or per-client ranking.
// What stays is the SHAPE of the curves plus a cache hit rate, which is a
// ratio of a hidden total and therefore reveals nothing about size.
//
// The backend enforces the same rule independently (see admin/public_redact.go
// — a non-operator receives series normalized to an arbitrary ceiling), so
// opening devtools on this page yields shapes too. Do not reintroduce a
// <YAxis>, a <ChartTooltip>, or a header total on this component.

// ----- shared chart configs -----
//
// Recharts pulls `label` directly from these configs to render legend
// items + tooltip names. To keep them language-aware we build the
// configs inside the component with the live t() instead of hoisting
// them as module constants. The colour themes stay constant.

type ConfigBuilder = (t: (k: string) => string) => Record<string, ChartConfig>;

const buildConfigs: ConfigBuilder = (t) => ({
  tokenConfig: {
    input: {
      label: t("legacy.chartLegendInput"),
      theme: { light: "oklch(0.5 0.13 215)", dark: "oklch(0.8 0.16 145)" },
    },
    output: {
      label: t("legacy.chartLegendOutput"),
      theme: { light: "oklch(0.62 0.15 150)", dark: "oklch(0.72 0.14 215)" },
    },
    cacheR: {
      label: t("legacy.chartLegendCacheR"),
      theme: { light: "oklch(0.68 0.15 70)", dark: "oklch(0.82 0.16 72)" },
    },
    cacheW: {
      label: t("legacy.chartLegendCacheW"),
      theme: { light: "oklch(0.55 0.18 25)", dark: "oklch(0.68 0.2 25)" },
    },
  },
  reqConfig: {
    requests: {
      label: t("legacy.chartLegendReq"),
      theme: { light: "oklch(0.48 0.1 215)", dark: "oklch(0.72 0.14 215)" },
    },
  },
  hourlyConfig: {
    input: {
      label: t("legacy.chartLegendInput"),
      theme: { light: "oklch(0.58 0.17 285)", dark: "oklch(0.75 0.17 285)" },
    },
    output: {
      label: t("legacy.chartLegendOutput"),
      theme: { light: "oklch(0.62 0.19 330)", dark: "oklch(0.78 0.17 330)" },
    },
    cacheR: {
      label: t("legacy.chartLegendCacheR"),
      theme: { light: "oklch(0.72 0.16 55)", dark: "oklch(0.84 0.16 62)" },
    },
    cacheW: {
      label: t("legacy.chartLegendCacheW"),
      theme: { light: "oklch(0.6 0.2 15)", dark: "oklch(0.72 0.2 15)" },
    },
  },
  healthConfig: {
    healthy: {
      label: t("legacy.healthy"),
      theme: { light: "oklch(0.58 0.12 150)", dark: "oklch(0.78 0.16 145)" },
    },
    quota: {
      label: t("legacy.quota"),
      theme: { light: "oklch(0.68 0.15 70)", dark: "oklch(0.82 0.16 72)" },
    },
    unhealthy: {
      label: t("legacy.unhealthy"),
      theme: { light: "oklch(0.52 0.18 25)", dark: "oklch(0.68 0.2 25)" },
    },
    disabled: {
      label: t("legacy.disabled"),
      theme: { light: "oklch(0.7 0.01 85)", dark: "oklch(0.5 0.01 260)" },
    },
  },
});

// ----- helpers -----

function pad(n: number) {
  return String(n).padStart(2, "0");
}
const fmtDay = (d: string) => d.slice(5).replace("-", "/");

// ----- types -----

export interface DashboardRequestsSlim {
  summary: RequestAgg;
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
  reqData: DashboardRequestsSlim | null;
  lifetimeData: DashboardRequestsSlim | null;
  hourly: HourBucket[] | null;
  busy?: boolean;
  /**
   * Public/non-operator view. Hides the credential-pool breakdown entirely —
   * pool size and composition are fleet-internal. Defaults to false so an
   * operator opening the same page still sees the pool pie.
   */
  publicView?: boolean;
}

// ----- component -----

export function DashboardBoard({
  pool,
  reqData,
  lifetimeData,
  hourly,
  busy = false,
  publicView = false,
}: DashboardBoardProps) {
  const { t } = useTranslation();
  const { tokenConfig, reqConfig, hourlyConfig, healthConfig } = buildConfigs(t);

  // Lifetime cache hit rate. A ratio of counters we never print, so it stays
  // safe to show — and it is the one platform number customers actually act
  // on, since cache reads are what makes their bill small.
  const hitRate = (() => {
    if (!lifetimeData) return null;
    const s = lifetimeData.summary;
    const denom = (s.input_tokens || 0) + (s.cache_read_tokens || 0) + (s.cache_create_tokens || 0);
    return denom > 0 ? (s.cache_read_tokens || 0) / denom : 0;
  })();

  // 14-day token throughput stacked area — derived from reqData.by_day so
  // this component doesn't need per-auth data.
  const trend = (() => {
    const today = new Date();
    today.setUTCHours(0, 0, 0, 0);
    const seed = new Map<
      string,
      {
        day: string;
        input: number;
        output: number;
        cacheR: number;
        cacheW: number;
        requests: number;
      }
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

  const hourlySeries = (() => {
    const out: {
      hour: string;
      label: string;
      input: number;
      output: number;
      cacheR: number;
      cacheW: number;
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
        requests: b.count || 0,
      });
    }
    return out;
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

  // Totals used only to decide whether each chart has anything to draw. They
  // are never rendered — see the file header.
  const trendTotal = trend.reduce((s, x) => s + x.input + x.output + x.cacheR + x.cacheW, 0);
  const hourlyTotal = hourlySeries.reduce(
    (s, x) => s + x.input + x.output + x.cacheR + x.cacheW,
    0,
  );
  const reqTotal = trend.reduce((s, x) => s + x.requests, 0);

  return (
    <div className="space-y-8">
      {/* Lifetime cache efficiency + the notice explaining the missing axes */}
      <section>
        <div className="flex items-baseline justify-between mb-3 gap-4">
          <div>
            <div className="eyebrow mb-1.5">{t("legacy.cacheEyebrow")}</div>
            <h3 className="font-display text-2xl md:text-3xl tracking-tight">
              {t("legacy.cacheTitleA")}{" "}
              <span className="text-muted-foreground">{t("legacy.cacheTitleB")}</span>
            </h3>
          </div>
          <span className="eyebrow tabular opacity-70 hidden sm:inline">
            {t("legacy.sinceFirst")}
          </span>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 md:gap-5">
          <CacheCard
            label={t("legacy.cacheHitRate")}
            value={hitRate != null ? `${(hitRate * 100).toFixed(2)}%` : busy ? "…" : "—"}
            ratio={hitRate ?? 0}
          />
          <RelativeOnlyNotice />
        </div>
      </section>

      {/* Token throughput — 14d shape, no scale */}
      <section>
        <div className="flex items-baseline justify-between mb-3 gap-4">
          <div>
            <div className="eyebrow mb-1.5">{t("legacy.last14d")}</div>
            <h3 className="font-display text-2xl md:text-3xl tracking-tight">
              {t("legacy.tokenVolume")}{" "}
              <span className="text-muted-foreground">{t("legacy.byType")}</span>
            </h3>
          </div>
          <span className="eyebrow opacity-70 hidden sm:inline">{t("legacy.relativeScale")}</span>
        </div>
        <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
          {trendTotal === 0 ? (
            <ChartEmpty
              className="h-[240px] md:h-[280px] w-full"
              label="no token activity in the last 14 days"
              hint="waiting for the first request in this window"
            />
          ) : (
            <ChartContainer
              config={tokenConfig}
              className="h-[240px] md:h-[280px] aspect-auto w-full"
            >
              <AreaChart data={trend} margin={{ top: 10, right: 12, left: 4, bottom: 0 }}>
                <defs>
                  {(["input", "output", "cacheR", "cacheW"] as const).map((k) => (
                    <linearGradient key={k} id={`grad-${k}`} x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor={`var(--color-${k})`} stopOpacity={0.5} />
                      <stop offset="95%" stopColor={`var(--color-${k})`} stopOpacity={0} />
                    </linearGradient>
                  ))}
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="day"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                  tickFormatter={fmtDay}
                  minTickGap={16}
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
                    isAnimationActive={false}
                    activeDot={false}
                  />
                ))}
              </AreaChart>
            </ChartContainer>
          )}
        </div>
      </section>

      {/* 24h hourly pulse — shape only */}
      <section>
        <div className="flex items-baseline justify-between mb-3 gap-4">
          <div>
            <div className="eyebrow mb-1.5">{t("legacy.pulse24")}</div>
            <h3 className="font-display text-2xl md:text-3xl tracking-tight">
              {t("legacy.liveRhythm")}{" "}
              <span className="text-muted-foreground">{t("legacy.rhythm")}</span>
            </h3>
          </div>
          <span className="eyebrow opacity-70 hidden sm:inline">{t("legacy.relativeScale")}</span>
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
            <ChartContainer
              config={hourlyConfig}
              className="relative h-[220px] md:h-[260px] aspect-auto w-full"
            >
              <BarChart
                data={hourlySeries}
                margin={{ top: 10, right: 8, left: 4, bottom: 0 }}
                barCategoryGap={2}
              >
                <CartesianGrid strokeDasharray="3 3" vertical={false} opacity={0.6} />
                <XAxis
                  dataKey="label"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                  minTickGap={24}
                  fontSize={11}
                />
                <ChartLegend content={<ChartLegendContent />} />
                {(["input", "output", "cacheR", "cacheW"] as const).map((k, i, arr) => (
                  <Bar
                    key={k}
                    dataKey={k}
                    stackId="h"
                    fill={`var(--color-${k})`}
                    radius={i === arr.length - 1 ? [3, 3, 0, 0] : 0}
                    isAnimationActive={false}
                  />
                ))}
              </BarChart>
            </ChartContainer>
          )}
        </div>
      </section>

      {/* Request trend + (operator only) pool health */}
      <section className={cn("grid grid-cols-1 gap-4 md:gap-5", !publicView && "lg:grid-cols-3")}>
        <div
          className={cn(
            "bg-card border border-border-strong rounded-md p-4 md:p-5",
            !publicView && "lg:col-span-2",
          )}
        >
          <div className="flex items-baseline justify-between mb-3 gap-2">
            <div>
              <div className="eyebrow mb-1">{t("legacy.dailyReq")}</div>
              <h3 className="font-display text-xl tracking-tight">{t("legacy.traffic14d")}</h3>
            </div>
            <span className="eyebrow opacity-70">{t("legacy.relativeScale")}</span>
          </div>
          {reqTotal === 0 ? (
            <ChartEmpty className="h-[220px] w-full" label="no requests in the last 14 days" />
          ) : (
            <ChartContainer config={reqConfig} className="h-[220px] aspect-auto w-full">
              <LineChart data={trend} margin={{ top: 8, right: 8, left: 4, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="day"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={8}
                  tickFormatter={fmtDay}
                  minTickGap={20}
                />
                <Line
                  type="monotone"
                  dataKey="requests"
                  stroke="var(--color-requests)"
                  strokeWidth={2}
                  dot={{ r: 3, strokeWidth: 0, fill: "var(--color-requests)" }}
                  activeDot={false}
                  isAnimationActive={false}
                />
              </LineChart>
            </ChartContainer>
          )}
        </div>

        {/* Pool composition is operator-only: credential count and health mix
            tell a reader how much upstream capacity we hold. */}
        {!publicView && (
          <div className="bg-card border border-border-strong rounded-md p-4 md:p-5">
            <div className="mb-4">
              <div className="eyebrow mb-1">{t("legacy.poolHealth")}</div>
              <h3 className="font-display text-xl tracking-tight">
                <span className="text-muted-foreground">
                  {t("legacy.poolCount", { n: pool?.total ?? 0 })}
                </span>
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
        )}
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

// RelativeOnlyNotice tells the reader the axes are missing on purpose, so an
// unlabelled chart reads as a policy rather than as a rendering bug.
function RelativeOnlyNotice() {
  const { t } = useTranslation();
  return (
    <div className="md:col-span-2 flex items-start gap-3 rounded-md border border-border-strong bg-muted/20 p-5">
      <EyeOff className="h-4 w-4 shrink-0 text-muted-foreground mt-0.5" />
      <div>
        <div className="eyebrow mb-1.5">{t("legacy.relativeOnlyTitle")}</div>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t("legacy.relativeOnlyBody")}
        </p>
      </div>
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
