import { motion } from "motion/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Area, AreaChart, CartesianGrid, Cell, Pie, PieChart, XAxis, YAxis } from "recharts";
import { Badge } from "@/components/ui/badge";
import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { categorySlots, categoryVar, spendConfig } from "@/lib/chart-theme";
import type { DaySpend, ModelSpend, TokenSpend } from "@/lib/types";
import { cn, fmtCompact, fmtUSD } from "@/lib/utils";

/* Charts for the spend dashboard.
 *
 * All three go through ChartContainer, which turns a config's { theme: {light,
 * dark} } into --color-* CSS vars and swaps them on theme change — which is why
 * none of this file contains a single dark: variant.
 *
 * ChartContainer defaults to aspect-video; every chart below overrides that with
 * an explicit height, or the layout goes haywire inside a flex parent.
 */

const CHART_CLASS = "h-[280px] aspect-auto w-full";

function EmptyChart({ hint }: { hint: string }) {
  return (
    <div className="flex h-[280px] items-center justify-center text-sm text-muted-foreground">
      {hint}
    </div>
  );
}

/* --- Spend over time ------------------------------------------------------ */

export function SpendTrend({ days }: { days: DaySpend[] }) {
  const { t } = useTranslation();
  if (!days.some((d) => d.spent_usd > 0)) return <EmptyChart hint={t("usage.chart.empty")} />;

  return (
    <ChartContainer config={spendConfig} className={CHART_CLASS}>
      <AreaChart data={days} margin={{ left: 4, right: 8, top: 8 }}>
        <defs>
          <linearGradient id="usage-spend-fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%" stopColor="var(--color-spent_usd)" stopOpacity={0.35} />
            <stop offset="95%" stopColor="var(--color-spent_usd)" stopOpacity={0.02} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" vertical={false} className="stroke-border/50" />
        <XAxis
          dataKey="day"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={32}
          tickFormatter={(v: string) => v.slice(5)}
          className="text-xs"
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          width={52}
          tickFormatter={(v: number) => fmtUSD(v)}
          className="text-xs"
        />
        <ChartTooltip
          content={
            <ChartTooltipContent indicator="dot" valueFormatter={(v) => fmtUSD(Number(v))} />
          }
        />
        <Area
          dataKey="spent_usd"
          type="monotone"
          stroke="var(--color-spent_usd)"
          strokeWidth={2}
          fill="url(#usage-spend-fill)"
        />
      </AreaChart>
    </ChartContainer>
  );
}

/* --- Spend by key: cross-token comparison --------------------------------- */

export interface TokenComparisonProps {
  tokens: TokenSpend[];
  selectedTokenId?: number;
  onSelectToken?: (tokenId: number) => void;
  showMember?: boolean;
}

type CompareMetric = "spend" | "events" | "tokens";
const METRICS: CompareMetric[] = ["spend", "events", "tokens"];

const tokenCount = (k: TokenSpend) =>
  k.input_tokens + k.output_tokens + k.cache_read_tokens + k.cache_create_tokens;

/** Ranked, side-by-side comparison of EVERY key in the range — the answer to
 * "which of the keys I handed out cost what". Always shows the full set (not a
 * top-N slice), scrolls when there are many, and lets you compare on three
 * axes: money, billable events, or raw tokens. Clicking a row filters the whole
 * page to that key, so comparison and drill-down share one control. Fed from
 * the unfiltered-by-token universe, so selecting one key never hides the rest. */
export function TokenComparison({
  tokens,
  selectedTokenId,
  onSelectToken,
  showMember,
}: TokenComparisonProps) {
  const { t } = useTranslation();
  const [metric, setMetric] = useState<CompareMetric>("spend");

  const metricValue = (k: TokenSpend) =>
    metric === "spend" ? k.spent_usd : metric === "events" ? k.charge_events : tokenCount(k);
  const fmt = (n: number) => (metric === "spend" ? fmtUSD(n) : fmtCompact(Math.round(n)));

  const ranked = [...tokens]
    .sort((a, b) => metricValue(b) - metricValue(a))
    .filter((k) => metricValue(k) > 0);
  if (ranked.length === 0) return <EmptyChart hint={t("usage.chart.empty")} />;

  const max = metricValue(ranked[0]) || 1;
  const grand = ranked.reduce((s, k) => s + metricValue(k), 0);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-xs text-muted-foreground">
          {t("usage.compare.summary", { n: ranked.length, total: fmt(grand) })}
        </span>
        <div className="flex items-center gap-0.5 rounded-full bg-muted/60 p-0.5">
          {METRICS.map((m) => (
            <button
              key={m}
              type="button"
              onClick={() => setMetric(m)}
              className={cn(
                "relative rounded-full px-3 py-1 text-xs font-medium transition-colors",
                metric === m
                  ? "text-primary-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {metric === m && (
                <motion.span
                  layoutId="usage-compare-metric"
                  className="absolute inset-0 rounded-full bg-primary"
                  transition={{ type: "spring", stiffness: 380, damping: 30 }}
                />
              )}
              <span className="relative z-10">{t(`usage.compare.metric.${m}`)}</span>
            </button>
          ))}
        </div>
      </div>

      <div className="max-h-[420px] space-y-1 overflow-y-auto pr-1">
        {ranked.map((k, i) => {
          const v = metricValue(k);
          const pct = grand > 0 ? (v / grand) * 100 : 0;
          const active = selectedTokenId === k.token_id;
          const dimmed = selectedTokenId != null && !active;
          const clickable = k.token_id > 0;
          return (
            <button
              key={k.token_id}
              type="button"
              disabled={!clickable}
              onClick={() => clickable && onSelectToken?.(k.token_id)}
              className={cn(
                "flex w-full flex-col gap-1 rounded-lg px-2.5 py-2 text-left transition-colors",
                clickable && "hover:bg-muted/60",
                active && "bg-primary/10 ring-1 ring-primary/30",
                dimmed && "opacity-55",
              )}
            >
              <div className="flex items-center gap-2">
                <span className="w-5 shrink-0 text-center font-mono text-xs text-muted-foreground">
                  {i + 1}
                </span>
                <span className="min-w-0 flex-1 truncate text-sm font-medium">
                  {labelFor(
                    k,
                    showMember,
                    t("usage.token.deleted", { id: k.token_id }),
                    t("usage.token.unattributed"),
                  )}
                </span>
                {k.tags?.slice(0, 2).map((tag) => (
                  <Badge key={tag} variant="outline" className="hidden text-[10px] sm:inline-flex">
                    {tag}
                  </Badge>
                ))}
                <span className="shrink-0 font-mono text-sm tabular-nums">{fmt(v)}</span>
                <span className="w-11 shrink-0 text-right font-mono text-[11px] tabular-nums text-muted-foreground">
                  {pct.toFixed(1)}%
                </span>
              </div>
              <div className="ml-7 h-2 overflow-hidden rounded-full bg-muted">
                <motion.div
                  className="h-full rounded-full bg-primary"
                  initial={{ width: 0 }}
                  animate={{ width: `${(v / max) * 100}%` }}
                  transition={{ type: "spring", stiffness: 120, damping: 20 }}
                  style={{ opacity: active ? 1 : selectedTokenId != null ? 0.5 : 0.85 }}
                />
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}

function labelFor(
  k: TokenSpend,
  showMember: boolean | undefined,
  deleted: string,
  unattributed: string,
): string {
  if (k.token_id === 0) return unattributed;
  const name = k.name || deleted;
  return showMember && k.email ? `${k.email.split("@")[0]} · ${name}` : name;
}

/* --- Spend by model ------------------------------------------------------- */

export function ModelBreakdown({ models }: { models: ModelSpend[] }) {
  const { t } = useTranslation();
  const data = models.filter((m) => m.spent_usd > 0);
  if (data.length === 0) return <EmptyChart hint={t("usage.chart.empty")} />;

  const total = data.reduce((s, m) => s + m.spent_usd, 0);

  return (
    <div className="relative">
      <ChartContainer config={categorySlots} className={CHART_CLASS}>
        <PieChart>
          <ChartTooltip
            content={<ChartTooltipContent hideLabel valueFormatter={(v) => fmtUSD(Number(v))} />}
          />
          <Pie
            data={data}
            dataKey="spent_usd"
            nameKey="model"
            innerRadius={64}
            outerRadius={98}
            paddingAngle={2}
            strokeWidth={2}
          >
            {data.map((m, i) => (
              <Cell key={m.model} fill={categoryVar(i)} />
            ))}
          </Pie>
        </PieChart>
      </ChartContainer>

      {/* total, in the donut hole */}
      <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
        <span className="font-mono text-2xl font-semibold tabular-nums">{fmtUSD(total)}</span>
        <span className="text-xs text-muted-foreground">{t("usage.chart.totalSpend")}</span>
      </div>

      <div className="mt-3 flex flex-wrap justify-center gap-x-4 gap-y-1.5 text-xs">
        {data.map((m, i) => (
          <span key={m.model} className="flex items-center gap-1.5">
            <span
              className="h-2.5 w-2.5 shrink-0 rounded-[2px]"
              style={{ backgroundColor: categoryVar(i) }}
            />
            <span className="text-muted-foreground">
              {m.model || t("usage.chart.unknownModel")}
            </span>
            <span className="font-mono tabular-nums">{fmtUSD(m.spent_usd)}</span>
          </span>
        ))}
      </div>
    </div>
  );
}
