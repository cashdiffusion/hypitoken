// Site-wide visitor-behaviour section of the admin "Growth" tab. Renders below
// the channel manager and talks only to /api/v2/admin/analytics/overview (the
// backend internal/saas/analytics module). Unlike the channel stats above it,
// this covers EVERY landing-page visitor — first action, dwell, flow, source —
// not just ?ref= channel traffic. Hand-rolled SVG / bars to match the existing
// tab's idiom (no extra chart dependency).

import { ArrowRight, Clock, Eye, LogOut, MousePointerClick, Users } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { GlassPanel } from "@/components/app/page-primitives";
import { apiGet } from "@/lib/api";
import { cn, errMsg, fmtInt } from "@/lib/utils";

interface Bucket {
  key: string;
  count: number;
}
interface PathCount {
  path: string;
  count: number;
}
interface OverviewTotals {
  sessions: number;
  visitors: number;
  pageviews: number;
  bounce_rate: number;
  median_dwell_ms: number;
  avg_dwell_ms: number;
}
interface BehaviorDaily {
  day: string;
  sessions: number;
  visitors: number;
  pageviews: number;
}
interface Overview {
  totals: OverviewTotals;
  daily: BehaviorDaily[];
  first_actions: Bucket[];
  dwell_buckets: Bucket[];
  sources: Bucket[];
  referrers: Bucket[];
  paths: PathCount[];
}

function fmtPct(r: number): string {
  return `${(r * 100).toFixed(1)}%`;
}

function fmtDwell(ms: number): string {
  if (!ms || ms <= 0) return "—";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  return rem ? `${m}m ${rem}s` : `${m}m`;
}

export function VisitorBehaviorSection() {
  const { t } = useTranslation();
  const [ov, setOv] = useState<Overview | null>(null);

  useEffect(() => {
    apiGet<Overview>("/admin/analytics/overview?days=14")
      .then(setOv)
      .catch((e) => toast.error(errMsg(e)));
  }, []);

  // Map raw action / source keys → localized labels. returnObjects avoids
  // i18next treating the ':' in "nav:pricing" as a namespace separator.
  const actionLabels = t("admin.behavior.actions", { returnObjects: true }) as Record<
    string,
    string
  >;
  const sourceLabels = t("admin.behavior.sources", { returnObjects: true }) as Record<
    string,
    string
  >;
  const actionLabel = (k: string) => actionLabels?.[k] ?? k;
  const sourceLabel = (k: string) => sourceLabels?.[k] ?? k;

  const tot = ov?.totals;
  const hasData = (tot?.sessions ?? 0) > 0;

  return (
    <div className="space-y-6">
      <div>
        <h3 className="text-base font-semibold">{t("admin.behavior.heading")}</h3>
        <p className="mt-0.5 text-xs text-muted-foreground">{t("admin.behavior.sub")}</p>
      </div>

      {/* headline totals */}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Tile
          icon={<Users className="h-4 w-4" />}
          label={t("admin.behavior.totals.sessions")}
          value={fmtInt(tot?.sessions ?? 0)}
        />
        <Tile
          icon={<LogOut className="h-4 w-4" />}
          label={t("admin.behavior.totals.bounce")}
          value={hasData ? fmtPct(tot?.bounce_rate ?? 0) : "—"}
        />
        <Tile
          icon={<Clock className="h-4 w-4" />}
          label={t("admin.behavior.totals.median")}
          value={fmtDwell(tot?.median_dwell_ms ?? 0)}
        />
        <Tile
          icon={<Eye className="h-4 w-4" />}
          label={t("admin.behavior.totals.pageviews")}
          value={fmtInt(tot?.pageviews ?? 0)}
        />
      </div>

      {!hasData ? (
        <GlassPanel title={t("admin.behavior.trendTitle")}>
          <div className="grid h-32 place-items-center text-sm text-muted-foreground">
            {t("admin.behavior.noData")}
          </div>
        </GlassPanel>
      ) : (
        <>
          {/* traffic trend */}
          <GlassPanel title={t("admin.behavior.trendTitle")}>
            <BehaviorTrend data={ov?.daily ?? []} />
          </GlassPanel>

          <div className="grid gap-6 lg:grid-cols-2">
            {/* first action distribution */}
            <GlassPanel title={t("admin.behavior.firstActionTitle")}>
              <p className="mb-3 text-xs text-muted-foreground">
                {t("admin.behavior.firstActionSub")}
              </p>
              <BarList
                items={(ov?.first_actions ?? []).map((b) => ({
                  label: actionLabel(b.key),
                  count: b.count,
                  tone: b.key === "bounce" ? "warn" : b.key === "start" ? "good" : "default",
                  icon:
                    b.key === "bounce" ? (
                      <LogOut className="h-3.5 w-3.5" />
                    ) : b.key === "start" ? (
                      <MousePointerClick className="h-3.5 w-3.5" />
                    ) : undefined,
                }))}
              />
            </GlassPanel>

            {/* dwell histogram */}
            <GlassPanel title={t("admin.behavior.dwellTitle")}>
              <BarList
                items={(ov?.dwell_buckets ?? []).map((b) => ({
                  label: b.key,
                  count: b.count,
                  tone: "accent",
                }))}
              />
            </GlassPanel>
          </div>

          <div className="grid gap-6 lg:grid-cols-2">
            {/* acquisition sources + referrers */}
            <GlassPanel title={t("admin.behavior.sourceTitle")}>
              <BarList
                items={(ov?.sources ?? []).map((b) => ({
                  label: sourceLabel(b.key),
                  count: b.count,
                  tone: "default",
                }))}
              />
              {(ov?.referrers?.length ?? 0) > 0 && (
                <div className="mt-4 border-t border-border/50 pt-3">
                  <div className="mb-2 text-xs font-medium text-muted-foreground">
                    {t("admin.behavior.referrerTitle")}
                  </div>
                  <ul className="space-y-1">
                    {ov?.referrers.map((r) => (
                      <li
                        key={r.key}
                        className="flex items-center justify-between font-mono text-xs"
                      >
                        <span className="truncate text-muted-foreground">{r.key}</span>
                        <span className="tabular-nums">{fmtInt(r.count)}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </GlassPanel>

            {/* top flows */}
            <GlassPanel title={t("admin.behavior.pathTitle")}>
              <p className="mb-3 text-xs text-muted-foreground">{t("admin.behavior.pathSub")}</p>
              {(ov?.paths?.length ?? 0) === 0 ? (
                <div className="grid h-20 place-items-center text-sm text-muted-foreground">
                  {t("admin.behavior.noData")}
                </div>
              ) : (
                <ol className="space-y-2">
                  {ov?.paths.map((p) => (
                    <li key={p.path} className="flex items-center justify-between gap-3 text-sm">
                      <PathChips path={p.path} />
                      <span className="shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
                        ×{fmtInt(p.count)}
                      </span>
                    </li>
                  ))}
                </ol>
              )}
            </GlassPanel>
          </div>
        </>
      )}
    </div>
  );
}

function Tile({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return (
    <div className="glass rounded-xl p-4">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        {icon}
        {label}
      </div>
      <div className="mt-1.5 font-mono text-2xl font-semibold tabular-nums">{value}</div>
    </div>
  );
}

type BarTone = "default" | "good" | "warn" | "accent";

const toneClass: Record<BarTone, string> = {
  default: "bg-primary/60",
  good: "bg-emerald-500/70",
  warn: "bg-amber-500/70",
  accent: "bg-sky-500/60",
};

// BarList draws a horizontal bar per item, widths scaled to the largest count.
// Matches the tab's "no chart dependency" idiom.
function BarList({
  items,
}: {
  items: { label: string; count: number; tone?: BarTone; icon?: ReactNode }[];
}) {
  const { t } = useTranslation();
  const max = Math.max(...items.map((i) => i.count), 1);
  if (items.length === 0 || items.every((i) => i.count === 0)) {
    return (
      <div className="grid h-20 place-items-center text-sm text-muted-foreground">
        {t("admin.behavior.noData")}
      </div>
    );
  }
  return (
    <div className="space-y-2.5">
      {items.map((i) => (
        <div key={i.label} className="space-y-1">
          <div className="flex items-center justify-between text-xs">
            <span className="inline-flex items-center gap-1.5 text-foreground/90">
              {i.icon}
              {i.label}
            </span>
            <span className="font-mono tabular-nums text-muted-foreground">{fmtInt(i.count)}</span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-muted/50">
            <div
              className={cn("h-full rounded-full transition-all", toneClass[i.tone ?? "default"])}
              style={{ width: `${Math.max((i.count / max) * 100, i.count > 0 ? 4 : 0)}%` }}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

// PathChips renders "home → pricing → register" as a row of small page chips.
function PathChips({ path }: { path: string }) {
  const hops = path.split(" → ");
  return (
    <span className="flex flex-wrap items-center gap-1">
      {hops.map((hop, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: hops are positional and may repeat
        <span key={i} className="inline-flex items-center gap-1">
          <span className="rounded bg-muted/70 px-1.5 py-0.5 font-mono text-[11px]">{hop}</span>
          {i < hops.length - 1 && <ArrowRight className="h-3 w-3 shrink-0 text-muted-foreground" />}
        </span>
      ))}
    </span>
  );
}

// BehaviorTrend draws sessions (bars) with pageviews (line) overlaid, mirroring
// the channel TrendChart's hand-rolled-SVG style.
function BehaviorTrend({ data }: { data: BehaviorDaily[] }) {
  const { t } = useTranslation();
  if (data.length === 0 || data.every((d) => d.sessions === 0 && d.pageviews === 0)) {
    return (
      <div className="grid h-32 place-items-center text-sm text-muted-foreground">
        {t("admin.behavior.noData")}
      </div>
    );
  }
  const maxS = Math.max(...data.map((d) => d.sessions), 1);
  const maxP = Math.max(...data.map((d) => d.pageviews), 1);
  const W = 100;
  const H = 60;
  const slot = W / data.length;
  const barW = slot * 0.55;

  const pts = data
    .map((d, i) => {
      const x = i * slot + slot / 2;
      const y = H - (d.pageviews / maxP) * (H - 6);
      return `${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(" ");

  return (
    <div>
      <div className="mb-3 flex gap-4 font-mono text-xs text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-2 w-3 rounded-sm bg-primary/70" />
          {t("admin.behavior.sessions")}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-2 w-3 rounded-sm bg-sky-500" />
          {t("admin.behavior.pageviews")}
        </span>
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className="block h-32 w-full overflow-visible"
        role="img"
        aria-label={t("admin.behavior.trendTitle")}
      >
        <title>{t("admin.behavior.trendTitle")}</title>
        <defs>
          <linearGradient id="behaviorSessionBar" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--primary)" stopOpacity="0.85" />
            <stop offset="100%" stopColor="var(--primary)" stopOpacity="0.25" />
          </linearGradient>
        </defs>
        {data.map((d, i) => {
          const h = (d.sessions / maxS) * (H - 6);
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
              className={cn(d.sessions === 0 && "fill-border")}
              style={d.sessions > 0 ? { fill: "url(#behaviorSessionBar)" } : undefined}
            >
              <title>{`${d.day}: ${d.sessions} ${t("admin.behavior.sessions")}, ${d.pageviews} ${t("admin.behavior.pageviews")}`}</title>
            </rect>
          );
        })}
        <polyline
          points={pts}
          fill="none"
          stroke="rgb(14 165 233)"
          strokeWidth="1.2"
          strokeLinejoin="round"
          strokeLinecap="round"
          vectorEffect="non-scaling-stroke"
        />
      </svg>
      <div className="mt-2 flex justify-between font-mono text-[10px] text-muted-foreground">
        <span>{data[0]?.day.slice(5)}</span>
        <span>{data[Math.floor(data.length / 2)]?.day.slice(5)}</span>
        <span>{data[data.length - 1]?.day.slice(5)}</span>
      </div>
    </div>
  );
}
