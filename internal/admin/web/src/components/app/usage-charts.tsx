import { useTranslation } from "react-i18next";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  XAxis,
  YAxis,
} from "recharts";
import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { categorySlots, categoryVar, spendConfig } from "@/lib/chart-theme";
import type { DaySpend, ModelSpend, TokenSpend } from "@/lib/types";
import { fmtUSD } from "@/lib/utils";

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

/* --- Spend by key --------------------------------------------------------- */

const TOP_N = 8;

export interface TokenBreakdownProps {
  tokens: TokenSpend[];
  selectedTokenId?: number;
  onSelectToken?: (tokenId: number) => void;
  showMember?: boolean;
}

/** Horizontal bars: key names are long and variable-length, so a vertical layout
 * would shred the x-axis labels. Clicking a bar filters the whole page to that
 * key — the fastest possible path to "what did THIS key cost me". */
export function TokenBreakdown({
  tokens,
  selectedTokenId,
  onSelectToken,
  showMember,
}: TokenBreakdownProps) {
  const { t } = useTranslation();
  if (tokens.length === 0) return <EmptyChart hint={t("usage.chart.empty")} />;

  const top = tokens.slice(0, TOP_N);
  const rest = tokens.slice(TOP_N);
  const data = top.map((k) => ({
    token_id: k.token_id,
    label: labelFor(
      k,
      showMember,
      t("usage.token.deleted", { id: k.token_id }),
      t("usage.token.unattributed"),
    ),
    spent_usd: k.spent_usd,
  }));
  if (rest.length > 0) {
    data.push({
      token_id: -1,
      label: t("usage.chart.others", { n: rest.length }),
      spent_usd: rest.reduce((s, k) => s + k.spent_usd, 0),
    });
  }

  return (
    <ChartContainer config={spendConfig} className={CHART_CLASS}>
      <BarChart data={data} layout="vertical" margin={{ left: 4, right: 16 }}>
        <CartesianGrid strokeDasharray="3 3" horizontal={false} className="stroke-border/50" />
        <XAxis type="number" hide />
        <YAxis
          type="category"
          dataKey="label"
          tickLine={false}
          axisLine={false}
          width={130}
          className="text-xs"
          tickFormatter={(v: string) => (v.length > 18 ? `${v.slice(0, 17)}…` : v)}
        />
        <ChartTooltip
          cursor={false}
          content={<ChartTooltipContent valueFormatter={(v) => fmtUSD(Number(v))} />}
        />
        <Bar dataKey="spent_usd" radius={[0, 6, 6, 0]} isAnimationActive>
          {data.map((d) => (
            <Cell
              key={d.token_id}
              fill="var(--color-spent_usd)"
              fillOpacity={selectedTokenId && selectedTokenId !== d.token_id ? 0.35 : 1}
              cursor={d.token_id > 0 ? "pointer" : "default"}
              onClick={() => d.token_id > 0 && onSelectToken?.(d.token_id)}
            />
          ))}
        </Bar>
      </BarChart>
    </ChartContainer>
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
