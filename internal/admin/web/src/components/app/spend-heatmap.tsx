import { motion, useReducedMotion } from "motion/react";
import { useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { DaySpend } from "@/lib/types";
import { cn, fmtUSD } from "@/lib/utils";

const EASE = [0.22, 1, 0.36, 1] as const;

/* SpendHeatmap — the GitHub contribution grid, for money.
 *
 * Recharts can't express this (it's a calendar, not a plot), so it's a plain CSS
 * grid: columns = weeks, rows = weekday. Two things are worth knowing before
 * changing it.
 *
 * BUCKETING IS BY QUANTILE, NOT BY A FIXED DOLLAR SCALE. A hobbyist spends $0.02
 * on a busy day; an enterprise spends $400. Any fixed threshold would render one
 * of them uniformly pale and the other uniformly saturated — the grid would carry
 * zero information for at least one of them. Quantiles over the ACTIVE days make
 * the scale self-normalizing: it always says "busy for you".
 *
 * MOTION IS PER-COLUMN, NOT PER-CELL. A year is ~370 cells; giving each one its
 * own animated node drops frames on entry. Staggering 53 column containers looks
 * identical and costs 7× less.
 */

const GAP = 3;
const WEEKDAYS = 7;

/** Cell size scales with how many weeks are on screen. GitHub's 11px is right for
 * a year, but a 7-day filter at 11px is a postage stamp — the grid should fill
 * the panel it was given at every range. */
function cellSize(weeks: number): number {
  if (weeks <= 6) return 34;
  if (weeks <= 10) return 24;
  if (weeks <= 20) return 16;
  return 11;
}

export interface SpendHeatmapProps {
  /** The (already zero-filled, ascending) day series to render. The grid spans
   * exactly this window — a 30-day filter draws ~5 columns, not a mostly-empty
   * year. */
  days: DaySpend[];
  selectedDay?: string;
  onSelectDay?: (day: string) => void;
}

interface Cell {
  day: string;
  spent: number;
  events: number;
  level: 0 | 1 | 2 | 3 | 4;
  inRange: boolean;
}

/** Fill for each intensity level.
 *
 * color-mix against --primary means the ramp inverts with the theme for free —
 * deep teal in light mode, bright green in dark — with no dark: variants and no
 * new design tokens. */
const LEVEL_MIX = [0, 22, 44, 68, 100] as const;

function levelStyle(level: number): React.CSSProperties {
  if (level === 0) return {};
  return {
    backgroundColor: `color-mix(in oklch, var(--primary) ${LEVEL_MIX[level]}%, var(--muted))`,
  };
}

/** quantiles bucket the ACTIVE days into 4 intensity levels.
 *
 * Below 5 active days a quantile split is noise (p25 and p75 of three points are
 * meaningless), so we degrade to a median split — enough to distinguish "a bit"
 * from "a lot" without inventing precision we don't have. */
function bucketize(values: number[]): (v: number) => 1 | 2 | 3 | 4 {
  const sorted = [...values].sort((a, b) => a - b);
  if (sorted.length < 5) {
    const mid = sorted[Math.floor(sorted.length / 2)] ?? 0;
    return (v) => (v > mid ? 3 : 2);
  }
  const q = (p: number) => sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * p))];
  const [q25, q50, q75] = [q(0.25), q(0.5), q(0.75)];
  return (v) => (v <= q25 ? 1 : v <= q50 ? 2 : v <= q75 ? 3 : 4);
}

const iso = (d: Date) => d.toISOString().slice(0, 10);

export function SpendHeatmap({ days, selectedDay, onSelectDay }: SpendHeatmapProps) {
  const { t, i18n } = useTranslation();
  const lang = i18n.language.startsWith("zh") ? "zh" : "en";
  const reduce = useReducedMotion();
  const [hover, setHover] = useState<{ cell: Cell; x: number; y: number } | null>(null);
  const gridRef = useRef<HTMLDivElement>(null);

  const columns = useMemo(() => {
    if (days.length === 0) return [];

    const byDay = new Map(days.map((d) => [d.day, d]));
    const active = days.filter((d) => d.spent_usd > 0).map((d) => d.spent_usd);
    const levelOf = bucketize(active);

    // The grid spans exactly the filtered window, snapped out to whole weeks
    // (back to the Sunday on or before the first day; forward to the Saturday on
    // or after the last). A fixed 53-week grid would leave a 30-day filter
    // rendering as a wall of empty cells with a smudge of colour in one corner.
    const first = new Date(`${days[0].day}T00:00:00Z`);
    const last = new Date(`${days[days.length - 1].day}T00:00:00Z`);
    const start = new Date(first);
    start.setUTCDate(first.getUTCDate() - first.getUTCDay());
    const end = new Date(last);
    end.setUTCDate(last.getUTCDate() + (6 - last.getUTCDay()));

    const today = iso(new Date());
    const cols: Cell[][] = [];
    for (let cursor = new Date(start); cursor <= end; cursor.setUTCDate(cursor.getUTCDate() + 7)) {
      const col: Cell[] = [];
      for (let d = 0; d < WEEKDAYS; d++) {
        const date = new Date(cursor);
        date.setUTCDate(cursor.getUTCDate() + d);
        const key = iso(date);
        const hit = byDay.get(key);
        const spent = hit?.spent_usd ?? 0;
        col.push({
          day: key,
          spent,
          events: hit?.charge_events ?? 0,
          level: spent > 0 ? levelOf(spent) : 0,
          // Cells outside the window (the week-snap padding, or the future) are
          // rendered invisible so the grid keeps its shape without implying
          // there was zero spend on a day we simply didn't ask about.
          inRange: byDay.has(key) && key <= today,
        });
      }
      cols.push(col);
    }
    return cols;
  }, [days]);

  const CELL = cellSize(columns.length);

  // Month labels sit in their own grid row so they can't disturb cell alignment.
  const monthLabels = useMemo(
    () =>
      columns.map((col) => {
        const first = col.find((c) => c.day.endsWith("-01"));
        if (!first) return "";
        const m = Number(first.day.slice(5, 7));
        return lang === "zh"
          ? `${m}月`
          : new Date(first.day).toLocaleString("en", { month: "short" });
      }),
    [columns, lang],
  );

  return (
    <div className="relative">
      {/* Centered: at a short range the grid is only a few columns wide and would
          otherwise hang off the left edge of a very wide panel. Still scrolls
          horizontally at a full-year range. */}
      <div className="flex justify-center overflow-x-auto pb-1">
        <div className="inline-flex flex-col gap-1" style={{ minWidth: "min-content" }}>
          {/* month row */}
          <div
            className="grid text-[9px] text-muted-foreground"
            style={{
              gridTemplateColumns: `repeat(${columns.length}, ${CELL}px)`,
              gap: `${GAP}px`,
              marginLeft: 24,
            }}
          >
            {monthLabels.map((label, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: columns are a fixed positional grid
              <span key={i} className="whitespace-nowrap">
                {label}
              </span>
            ))}
          </div>

          <div className="flex gap-1">
            {/* weekday gutter — Mon/Wed/Fri only, like GitHub */}
            <div
              className="grid text-[9px] text-muted-foreground"
              style={{
                gridTemplateRows: `repeat(${WEEKDAYS}, ${CELL}px)`,
                gap: `${GAP}px`,
                width: 20,
              }}
            >
              {[0, 1, 2, 3, 4, 5, 6].map((d) => (
                <span key={d} className="leading-[11px]">
                  {d === 1
                    ? t("usage.heatmap.mon")
                    : d === 3
                      ? t("usage.heatmap.wed")
                      : d === 5
                        ? t("usage.heatmap.fri")
                        : ""}
                </span>
              ))}
            </div>

            {/* the grid: one motion node per COLUMN, not per cell */}
            <motion.div
              ref={gridRef}
              className="grid"
              style={{
                gridTemplateColumns: `repeat(${columns.length}, ${CELL}px)`,
                gap: `${GAP}px`,
              }}
              initial={reduce ? false : "hidden"}
              animate="show"
              variants={{ show: { transition: { staggerChildren: 0.012 } } }}
              aria-label={t("usage.heatmap.title")}
            >
              {columns.map((col, ci) => (
                <motion.div
                  // biome-ignore lint/suspicious/noArrayIndexKey: columns are a fixed positional grid
                  key={ci}
                  className="grid"
                  style={{
                    gridTemplateRows: `repeat(${WEEKDAYS}, ${CELL}px)`,
                    gap: `${GAP}px`,
                    transformOrigin: "bottom",
                  }}
                  variants={{
                    hidden: { opacity: 0, scaleY: 0.4 },
                    show: { opacity: 1, scaleY: 1, transition: { duration: 0.35, ease: EASE } },
                  }}
                >
                  {col.map((cell) => (
                    <button
                      key={cell.day}
                      type="button"
                      aria-label={
                        cell.spent > 0
                          ? t("usage.heatmap.cell", {
                              day: cell.day,
                              spend: fmtUSD(cell.spent),
                              n: cell.events,
                            })
                          : t("usage.heatmap.cellEmpty", { day: cell.day })
                      }
                      disabled={!cell.inRange}
                      className={cn(
                        "rounded-[2px] transition-[outline] duration-150",
                        cell.level === 0 && "bg-muted/50 ring-1 ring-inset ring-border/40",
                        !cell.inRange && "opacity-0",
                        cell.inRange &&
                          "cursor-pointer hover:outline hover:outline-1 hover:outline-primary",
                        selectedDay === cell.day && "outline outline-2 outline-primary",
                      )}
                      style={{ width: CELL, height: CELL, ...levelStyle(cell.level) }}
                      onMouseEnter={(e) => {
                        const r = e.currentTarget.getBoundingClientRect();
                        const g = gridRef.current?.getBoundingClientRect();
                        if (!g) return;
                        setHover({ cell, x: r.left - g.left + r.width / 2, y: r.top - g.top });
                      }}
                      onMouseLeave={() => setHover(null)}
                      onClick={() => cell.inRange && onSelectDay?.(cell.day)}
                    />
                  ))}
                </motion.div>
              ))}
            </motion.div>
          </div>
        </div>
      </div>

      {/* legend */}
      <div className="mt-3 flex items-center justify-end gap-1.5 text-[10px] text-muted-foreground">
        <span>{t("usage.heatmap.less")}</span>
        {[0, 1, 2, 3, 4].map((l) => (
          <span
            key={l}
            className={cn(
              "rounded-[2px]",
              l === 0 && "bg-muted/50 ring-1 ring-inset ring-border/40",
            )}
            style={{ width: CELL, height: CELL, ...levelStyle(l) }}
          />
        ))}
        <span>{t("usage.heatmap.more")}</span>
      </div>

      {/* A single shared tooltip node — 370 Radix Tooltip instances would be
          absurd, and the grid is positioned, so one absolute div does the job. */}
      {hover && (
        <div
          className="pointer-events-none absolute z-20 -translate-x-1/2 -translate-y-full rounded-lg border border-border/60 bg-popover px-2.5 py-1.5 text-xs shadow-lg"
          style={{ left: hover.x + 24, top: hover.y - 6 }}
        >
          {hover.cell.spent > 0 ? (
            <>
              <span className="font-mono font-semibold text-primary">
                {fmtUSD(hover.cell.spent)}
              </span>
              <span className="text-muted-foreground">
                {" · "}
                {t("usage.heatmap.events", { n: hover.cell.events })}
              </span>
              <div className="text-[10px] text-muted-foreground">{hover.cell.day}</div>
            </>
          ) : (
            <span className="text-muted-foreground">
              {t("usage.heatmap.cellEmpty", { day: hover.cell.day })}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
