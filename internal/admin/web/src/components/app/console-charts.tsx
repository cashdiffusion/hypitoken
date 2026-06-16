import { useTranslation } from "react-i18next";
import { cn, fmtCompact, fmtUSD } from "@/lib/utils";

// console-charts — the hand-rolled SVG / CSS charts for the /app/console
// "个人" tab. No chart-library dependency: mirrors the admin behaviour-section
// idiom (BehaviorTrend bars+line, BarList) so the visual language stays
// consistent across the app. Every series here is the caller's own usage.

export interface DayPoint {
  day: string; // YYYY-MM-DD (UTC)
  requests: number;
  tokens: number;
  cost: number;
}

export interface ModelPoint {
  model: string;
  requests: number;
  tokens: number;
  cost: number;
}

// Token-composition segment colours, reused by the donut + its legend.
const SEG = {
  input: "rgb(16 185 129)", // emerald
  output: "rgb(56 189 248)", // sky
  cacheRead: "rgb(167 139 250)", // violet
  cacheWrite: "rgb(251 191 36)", // amber
} as const;

// UsageTrend — daily requests (bars) with the token total (line) overlaid on a
// secondary scale. Hand-rolled SVG so it inherits the page's theme tokens.
export function UsageTrend({ days }: { days: DayPoint[] }) {
  const { t } = useTranslation();
  const empty = days.length === 0 || days.every((d) => d.requests === 0 && d.tokens === 0);
  if (empty) {
    return (
      <div className="grid h-40 place-items-center text-sm text-muted-foreground">
        {t("console.personal.noData")}
      </div>
    );
  }
  const maxR = Math.max(...days.map((d) => d.requests), 1);
  const maxT = Math.max(...days.map((d) => d.tokens), 1);
  const W = 100;
  const H = 60;
  const slot = W / days.length;
  const barW = slot * 0.55;
  const pts = days
    .map((d, i) => {
      const x = i * slot + slot / 2;
      const y = H - (d.tokens / maxT) * (H - 6);
      return `${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(" ");
  return (
    <div>
      <div className="mb-3 flex gap-4 font-mono text-xs text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-2 w-3 rounded-sm bg-primary/70" />
          {t("console.personal.trendReq")}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-2 w-3 rounded-sm bg-sky-500" />
          {t("console.personal.trendTok")}
        </span>
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className="block h-40 w-full overflow-visible"
        role="img"
        aria-label={t("console.personal.trend")}
      >
        <title>{t("console.personal.trend")}</title>
        <defs>
          <linearGradient id="consoleTrendBar" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--primary)" stopOpacity="0.85" />
            <stop offset="100%" stopColor="var(--primary)" stopOpacity="0.2" />
          </linearGradient>
        </defs>
        {days.map((d, i) => {
          const h = (d.requests / maxR) * (H - 6);
          const x = i * slot + (slot - barW) / 2;
          const y = H - h;
          return (
            <rect
              key={d.day}
              x={x}
              y={y}
              width={barW}
              height={h || 0.5}
              rx="0.6"
              className={cn(d.requests === 0 && "fill-border")}
              style={d.requests > 0 ? { fill: "url(#consoleTrendBar)" } : undefined}
            >
              <title>{`${d.day}: ${fmtCompact(d.requests)} ${t("console.personal.trendReq")}, ${fmtCompact(d.tokens)} ${t("console.personal.trendTok")}`}</title>
            </rect>
          );
        })}
        <polyline
          points={pts}
          fill="none"
          stroke="rgb(56 189 248)"
          strokeWidth="1.2"
          strokeLinejoin="round"
          strokeLinecap="round"
          vectorEffect="non-scaling-stroke"
        />
      </svg>
      <div className="mt-2 flex justify-between font-mono text-[10px] text-muted-foreground">
        <span>{days[0]?.day.slice(5)}</span>
        <span>{days[Math.floor(days.length / 2)]?.day.slice(5)}</span>
        <span>{days[days.length - 1]?.day.slice(5)}</span>
      </div>
    </div>
  );
}

// donutCircumference for r=38.
const R = 38;
const C = 2 * Math.PI * R;

// TokenDonut — a 4-segment ring showing the input / output / cache-read /
// cache-write split of all-time tokens, with a legend and the grand total in
// the hole.
export function TokenDonut({
  input,
  output,
  cacheRead,
  cacheWrite,
}: {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}) {
  const { t } = useTranslation();
  const total = input + output + cacheRead + cacheWrite;
  const segments = [
    { key: "input", label: t("console.personal.segInput"), value: input, color: SEG.input },
    { key: "output", label: t("console.personal.segOutput"), value: output, color: SEG.output },
    {
      key: "cacheRead",
      label: t("console.personal.segCacheRead"),
      value: cacheRead,
      color: SEG.cacheRead,
    },
    {
      key: "cacheWrite",
      label: t("console.personal.segCacheWrite"),
      value: cacheWrite,
      color: SEG.cacheWrite,
    },
  ];
  if (total === 0) {
    return (
      <div className="grid h-40 place-items-center text-sm text-muted-foreground">
        {t("console.personal.noData")}
      </div>
    );
  }
  let offset = 0;
  return (
    <div className="flex flex-col items-center gap-4 sm:flex-row sm:items-center sm:gap-5">
      <div className="relative h-32 w-32 shrink-0">
        <svg
          viewBox="0 0 100 100"
          className="h-full w-full -rotate-90"
          role="img"
          aria-label={t("console.personal.tokenComposition")}
        >
          <circle cx="50" cy="50" r={R} fill="none" stroke="var(--muted)" strokeWidth="13" />
          {segments.map((s) => {
            const frac = s.value / total;
            const dash = frac * C;
            const el = (
              <circle
                key={s.key}
                cx="50"
                cy="50"
                r={R}
                fill="none"
                stroke={s.color}
                strokeWidth="13"
                strokeDasharray={`${dash} ${C - dash}`}
                strokeDashoffset={-offset}
                strokeLinecap="butt"
              />
            );
            offset += dash;
            return el;
          })}
        </svg>
        <div className="absolute inset-0 flex flex-col items-center justify-center">
          <span className="font-mono text-lg font-semibold tabular-nums">{fmtCompact(total)}</span>
          <span className="text-[10px] uppercase tracking-wider text-muted-foreground">
            {t("console.metrics.tok")}
          </span>
        </div>
      </div>
      <div className="w-full space-y-1.5">
        {segments.map((s) => (
          <div key={s.key} className="flex items-center justify-between gap-2 text-xs">
            <span className="inline-flex items-center gap-1.5 text-foreground/90">
              <span
                className="inline-block h-2.5 w-2.5 rounded-sm"
                style={{ background: s.color }}
              />
              {s.label}
            </span>
            <span className="font-mono tabular-nums text-muted-foreground">
              {fmtCompact(s.value)} · {((s.value / total) * 100).toFixed(0)}%
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

// CacheGauge — a single-value progress ring for the cache-hit rate.
export function CacheGauge({ pct }: { pct: number }) {
  const { t } = useTranslation();
  const clamped = Math.max(0, Math.min(100, pct));
  const dash = (clamped / 100) * C;
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3">
      <div className="relative h-32 w-32">
        <svg
          viewBox="0 0 100 100"
          className="h-full w-full -rotate-90"
          role="img"
          aria-label={t("console.personal.cacheHit")}
        >
          <circle cx="50" cy="50" r={R} fill="none" stroke="var(--muted)" strokeWidth="13" />
          <circle
            cx="50"
            cy="50"
            r={R}
            fill="none"
            stroke="rgb(16 185 129)"
            strokeWidth="13"
            strokeDasharray={`${dash} ${C - dash}`}
            strokeLinecap="round"
          />
        </svg>
        <div className="absolute inset-0 flex flex-col items-center justify-center">
          <span className="font-mono text-2xl font-semibold tabular-nums text-emerald-500">
            {clamped.toFixed(1)}
            <span className="text-base">%</span>
          </span>
        </div>
      </div>
      <span className="text-xs text-muted-foreground">{t("console.personal.cacheHitHint")}</span>
    </div>
  );
}

// ModelBars — a horizontal bar per model, width scaled to the largest token
// total, with the request count and spend alongside. Mirrors admin BarList.
export function ModelBars({ items }: { items: ModelPoint[] }) {
  const { t } = useTranslation();
  if (items.length === 0) {
    return <p className="mt-4 text-sm text-muted-foreground">{t("console.personal.noData")}</p>;
  }
  const max = Math.max(...items.map((m) => m.tokens), 1);
  return (
    <div className="mt-4 space-y-3">
      {items.map((m) => (
        <div key={m.model} className="space-y-1">
          <div className="flex items-center justify-between gap-3 text-xs">
            <span className="font-mono text-foreground/90">{m.model}</span>
            <span className="shrink-0 font-mono tabular-nums text-muted-foreground">
              {fmtCompact(m.requests)} · {fmtCompact(m.tokens)} {t("console.metrics.tok")} ·{" "}
              {fmtUSD(m.cost)}
            </span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-muted/50">
            <div
              className="h-full rounded-full bg-primary/70 transition-all"
              style={{ width: `${Math.max((m.tokens / max) * 100, m.tokens > 0 ? 4 : 0)}%` }}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

// DailyTable — the most-recent days, newest first, with raw per-day figures.
export function DailyTable({ rows }: { rows: DayPoint[] }) {
  const { t } = useTranslation();
  const recent = [...rows].filter((r) => r.requests > 0 || r.tokens > 0).reverse();
  if (recent.length === 0) {
    return <p className="mt-4 text-sm text-muted-foreground">{t("console.personal.noData")}</p>;
  }
  return (
    <div className="mt-4 overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs uppercase tracking-wider text-muted-foreground">
            <th className="pb-2 pr-4 font-medium">{t("console.personal.colDate")}</th>
            <th className="pb-2 pr-4 text-right font-medium">
              {t("console.personal.colRequests")}
            </th>
            <th className="pb-2 pr-4 text-right font-medium">{t("console.personal.colTokens")}</th>
            <th className="pb-2 text-right font-medium">{t("console.personal.colCost")}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border/60">
          {recent.map((r) => (
            <tr key={r.day}>
              <td className="py-2 pr-4 font-mono text-xs">{r.day}</td>
              <td className="py-2 pr-4 text-right font-mono tabular-nums">
                {fmtCompact(r.requests)}
              </td>
              <td className="py-2 pr-4 text-right font-mono tabular-nums">
                {fmtCompact(r.tokens)}
              </td>
              <td className="py-2 text-right font-mono tabular-nums">{fmtUSD(r.cost)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
