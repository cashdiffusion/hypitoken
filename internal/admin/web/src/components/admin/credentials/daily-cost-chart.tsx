import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { UsageDay } from "@/lib/types";
import { cn } from "@/lib/utils";

// Exact, never abbreviated: operators read this to reconcile spend, so no
// compact/elided forms. 2 decimals from $1 up, 4 below so micro-days survive.
const fmtCostExact = (v: number): string => (v >= 1 ? `$${v.toFixed(2)}` : `$${v.toFixed(4)}`);

/* DailyCostChart — 14 bars of per-day USD spend for one credential, with a
 * hover tooltip carrying the full unrounded numbers. Pure CSS, no chart lib:
 * the series is tiny and the tooltip content matters more than the marks. */
export function DailyCostChart({ days }: { days: UsageDay[] }) {
  const { t } = useTranslation();
  const [hover, setHover] = useState<number | null>(null);

  const max = days.reduce((m, d) => Math.max(m, d.cost_usd), 0);
  const total = days.reduce((s, d) => s + d.cost_usd, 0);
  if (days.length === 0 || max <= 0) {
    return <p className="text-xs text-muted-foreground">{t("admin.creds.detail.noHistory")}</p>;
  }

  return (
    <div>
      <div className="mb-2 flex items-baseline justify-end gap-1.5">
        <span className="text-[10px] text-muted-foreground">
          {t("admin.creds.detail.spend14dTotal")}
        </span>
        <span className="mono text-xs tabular-nums">{fmtCostExact(total)}</span>
      </div>
      <div className="flex items-end gap-1">
        {days.map((d, i) => (
          <button
            key={d.day}
            type="button"
            aria-label={`${d.day} ${fmtCostExact(d.cost_usd)}`}
            className="relative min-w-0 flex-1 cursor-default"
            onMouseEnter={() => setHover(i)}
            onMouseLeave={() => setHover((h) => (h === i ? null : h))}
            onFocus={() => setHover(i)}
            onBlur={() => setHover((h) => (h === i ? null : h))}
          >
            {hover === i && (
              <div
                className={cn(
                  "glass pointer-events-none absolute bottom-full z-10 mb-2 whitespace-nowrap rounded-lg px-3 py-2 shadow-lg",
                  // Clamp edge tooltips inside the dialog instead of centering.
                  i <= 2
                    ? "left-0"
                    : i >= days.length - 3
                      ? "right-0"
                      : "left-1/2 -translate-x-1/2",
                )}
              >
                <div className="mono mb-1 text-[11px] tabular-nums">{d.day}</div>
                <div className="space-y-0.5 text-[11px]">
                  <div className="flex items-baseline justify-between gap-4">
                    <span className="text-muted-foreground">{t("admin.creds.detail.tipCost")}</span>
                    <span className="mono tabular-nums">{fmtCostExact(d.cost_usd)}</span>
                  </div>
                  <div className="flex items-baseline justify-between gap-4">
                    <span className="text-muted-foreground">{t("admin.creds.detail.tipIO")}</span>
                    <span className="mono tabular-nums">
                      {d.input_tokens.toLocaleString()} / {d.output_tokens.toLocaleString()}
                    </span>
                  </div>
                  <div className="flex items-baseline justify-between gap-4">
                    <span className="text-muted-foreground">
                      {t("admin.creds.detail.tipRequests")}
                    </span>
                    <span className="mono tabular-nums">{d.requests.toLocaleString()}</span>
                  </div>
                </div>
              </div>
            )}
            <div className="flex h-24 items-end">
              <div
                className={cn(
                  "w-full rounded-[3px] transition-colors",
                  d.cost_usd > 0
                    ? hover === i
                      ? "bg-primary"
                      : "bg-primary/75"
                    : "bg-muted-foreground/20",
                )}
                style={{
                  // Zero-days keep a visible sliver so the axis reads as 14
                  // real days rather than gaps.
                  height: d.cost_usd > 0 ? `${Math.max((d.cost_usd / max) * 100, 4)}%` : "3px",
                }}
              />
            </div>
            <div className="mt-1 text-center text-[9px] tabular-nums text-muted-foreground">
              {/* Sparse labels — odd indices so the last bar (today) is labeled. */}
              {i % 2 === 1 ? Number.parseInt(d.day.slice(8), 10) : " "}
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}
