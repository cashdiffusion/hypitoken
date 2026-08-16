// Admin business dashboard — surfaces revenue, customer growth, and
// recent activity in a single fast-loading view backed by
// /api/v2/admin/dashboard. Distinct from the operator console (which
// focuses on credential / fleet health) and from the user-facing
// /app dashboard (per-user usage). Designed as the admin's "money
// view" so the operator can see at a glance what the platform is doing.

import {
  ArrowUpRight,
  DollarSign,
  RefreshCw,
  ShoppingCart,
  TrendingUp,
  Trophy,
  UserPlus,
  Users,
  Wallet,
} from "lucide-react";
import { lazy, type ReactNode, Suspense, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { FadeIn } from "@/components/admin/fade-in";
import { SpotlightCard } from "@/components/landing/interactions";
import { Button } from "@/components/ui/button";
import { apiGet } from "@/lib/api";
import { cn, errMsg, fmtUSD } from "@/lib/utils";

// Ambient 3D particle shell — atmosphere only, never a focal element. Lazy so
// the three.js chunk never blocks the dashboard's first paint.
const ParticleField = lazy(() => import("@/components/landing/particle-field"));

interface UsersBlock {
  total: number;
  verified: number;
  new_30d: number;
  disabled: number;
}
interface RevenueBlock {
  topups_lifetime: number;
  topups_30d: number;
  topups_7d: number;
  charges_lifetime: number;
  charges_30d: number;
  charges_7d: number;
}
interface BalanceBlock {
  outstanding: number;
  orders_pending: number;
  orders_paid_lifetime: number;
}
interface Daily {
  day: string;
  amount: number;
}
interface Spender {
  user_id: number;
  email: string;
  spent: number;
}
interface RecentTopup {
  out_trade_no: string;
  user_id: number;
  user_email: string;
  cny_amount: number;
  usd_credit: number;
  paid_at: number;
}
interface Signup {
  id: number;
  email: string;
  role: string;
  verified: boolean;
  disabled: boolean;
  created_at: number;
}
interface DashboardResp {
  users: UsersBlock;
  revenue: RevenueBlock;
  balance: BalanceBlock;
  daily_revenue: Daily[];
  top_spenders: Spender[];
  recent_topups: RecentTopup[];
  recent_signups: Signup[];
}

export function AdminDashboard() {
  const { t } = useTranslation();
  const [data, setData] = useState<DashboardResp | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const reload = async () => {
    setBusy(true);
    setErr("");
    try {
      const r = await apiGet<DashboardResp>("/admin/dashboard");
      setData(r);
    } catch (e) {
      setErr(errMsg(e, "load failed"));
    } finally {
      setBusy(false);
    }
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: mount-only — reload is stable; interval should not restart on every render
  useEffect(() => {
    reload();
    const id = setInterval(reload, 30_000);
    return () => clearInterval(id);
  }, []);

  if (!data && !err) {
    return (
      <div className="glass grid h-48 place-items-center rounded-2xl text-sm text-muted-foreground">
        <span className="flex items-center gap-2">
          <RefreshCw className="h-4 w-4 animate-spin" /> {t("admin.dashboard.loading")}
        </span>
      </div>
    );
  }
  if (err) {
    return (
      <div className="rounded-xl border border-destructive/40 bg-destructive/10 p-4 font-mono text-sm text-destructive">
        {err}
      </div>
    );
  }
  if (!data) return null;

  const margin =
    data.revenue.topups_lifetime > 0
      ? ((data.revenue.topups_lifetime - data.balance.outstanding) / data.revenue.topups_lifetime) *
        100
      : 0;
  const realized = data.revenue.topups_lifetime - data.balance.outstanding; // wallet drained = revenue earned

  return (
    <FadeIn className="space-y-6">
      {/* Hero — glass plane with an ambient particle shell + realized revenue */}
      <div className="glass relative overflow-hidden rounded-2xl p-6 lg:p-8">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 opacity-[0.45] [mask-image:radial-gradient(120%_120%_at_85%_-10%,black,transparent_70%)]"
        >
          <Suspense fallback={null}>
            <ParticleField color="#34d399" count={1100} />
          </Suspense>
        </div>
        <div
          aria-hidden
          className="pointer-events-none absolute -right-24 -top-24 h-64 w-64 rounded-full bg-primary/10 blur-3xl"
        />
        <div className="relative flex flex-wrap items-end justify-between gap-6">
          <div className="min-w-0">
            <div className="eyebrow mb-2.5 flex items-center gap-2 text-primary">
              <span className="relative inline-flex h-2 w-2">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary opacity-75" />
                <span className="relative inline-flex h-2 w-2 rounded-full bg-primary" />
              </span>
              {t("admin.dashboard.eyebrow")}
            </div>
            <h1 className="font-display text-4xl font-semibold tracking-tight lg:text-5xl">
              {t("admin.dashboard.titlePart1")}{" "}
              <span className="text-muted-foreground/70">{t("admin.dashboard.titlePart2")}</span>
            </h1>
            <p className="mt-2 max-w-xl text-sm text-muted-foreground lg:text-base">
              {t("admin.dashboard.sub")}
            </p>
          </div>
          <div className="flex items-baseline gap-2">
            <span className="font-mono text-5xl font-semibold tracking-tight tabular-nums text-primary lg:text-6xl">
              {fmtUSD(realized)}
            </span>
            <span className="ml-1 whitespace-pre-line font-mono text-xs uppercase tracking-wider text-muted-foreground">
              {t("admin.dashboard.realizedRevenue")}
            </span>
          </div>
        </div>
      </div>

      {/* KPI bento — glass spotlight tiles */}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div className="flex">
          <KpiTile
            icon={<DollarSign className="h-4 w-4" />}
            label={t("admin.dashboard.kpi.topupsLifetime")}
            value={fmtUSD(data.revenue.topups_lifetime)}
            sub={t("admin.dashboard.kpi.topupsSub", {
              m30: fmtUSD(data.revenue.topups_30d),
              m7: fmtUSD(data.revenue.topups_7d),
            })}
            accent="primary"
          />
        </div>
        <div className="flex">
          <KpiTile
            icon={<TrendingUp className="h-4 w-4" />}
            label={t("admin.dashboard.kpi.chargesLifetime")}
            value={fmtUSD(data.revenue.charges_lifetime)}
            sub={t("admin.dashboard.kpi.chargesSub", {
              m30: fmtUSD(data.revenue.charges_30d),
              m7: fmtUSD(data.revenue.charges_7d),
            })}
          />
        </div>
        <div className="flex">
          <KpiTile
            icon={<Wallet className="h-4 w-4" />}
            label={t("admin.dashboard.kpi.outstanding")}
            value={fmtUSD(data.balance.outstanding)}
            sub={t("admin.dashboard.kpi.outstandingSub", { margin: margin.toFixed(1) })}
            accent="warning"
          />
        </div>
        <div className="flex">
          <KpiTile
            icon={<Users className="h-4 w-4" />}
            label={t("admin.dashboard.kpi.users")}
            value={data.users.total.toString()}
            sub={t("admin.dashboard.kpi.usersSub", {
              verified: data.users.verified,
              new30: data.users.new_30d,
              disabled: data.users.disabled,
            })}
          />
        </div>
      </div>

      {/* Revenue + leaderboard bento */}
      <div className="grid gap-4 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <Panel
            eyebrow={t("admin.dashboard.revenueChartEyebrow")}
            title={t("admin.dashboard.revenueChartTitle")}
            action={
              <Button
                variant="ghost"
                size="sm"
                onClick={reload}
                disabled={busy}
                className="gap-1.5"
              >
                <RefreshCw className={cn("h-3.5 w-3.5", busy && "animate-spin")} />
                <span className="text-xs">{t("common.refresh")}</span>
              </Button>
            }
          >
            <DailyRevenueChart data={data.daily_revenue} />
          </Panel>
        </div>

        <div>
          <Panel
            icon={<Trophy className="h-3.5 w-3.5" />}
            eyebrow={t("admin.dashboard.topSpendersEyebrow")}
            title={t("admin.dashboard.topSpendersTitle")}
          >
            {data.top_spenders.length === 0 ? (
              <Empty>{t("admin.dashboard.noCharges")}</Empty>
            ) : (
              <ul className="space-y-1">
                {data.top_spenders.map((u, i) => (
                  <li
                    key={u.user_id}
                    className="flex items-center gap-3 rounded-lg px-2 py-1.5 transition-colors hover:bg-primary/[0.04]"
                  >
                    <span
                      className={cn(
                        "grid h-5 w-5 shrink-0 place-items-center rounded-md font-mono text-[11px] font-semibold tabular-nums",
                        i === 0
                          ? "bg-primary/20 text-primary"
                          : "bg-muted/60 text-muted-foreground",
                      )}
                    >
                      {i + 1}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium">{u.email}</div>
                      <div className="font-mono text-[11px] text-muted-foreground">
                        id={u.user_id}
                      </div>
                    </div>
                    <span className="font-mono text-sm font-semibold tabular-nums">
                      {fmtUSD(u.spent)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
        </div>
      </div>

      {/* Recent activity bento */}
      <div className="grid gap-4 lg:grid-cols-2">
        <div>
          <Panel
            icon={<ShoppingCart className="h-3.5 w-3.5" />}
            eyebrow={t("admin.dashboard.recentTopupsEyebrow")}
            title={t("admin.dashboard.recentTopupsTitle", { n: data.recent_topups.length })}
            action={
              <span className="font-mono text-xs text-muted-foreground">
                {t("admin.dashboard.ordersAllTimePending", {
                  paid: data.balance.orders_paid_lifetime,
                  pending: data.balance.orders_pending,
                })}
              </span>
            }
          >
            {data.recent_topups.length === 0 ? (
              <Empty>{t("admin.dashboard.noPaid")}</Empty>
            ) : (
              <ul className="divide-y divide-border/60">
                {data.recent_topups.map((o) => (
                  <li
                    key={o.out_trade_no}
                    className="flex items-center justify-between gap-3 py-2.5 text-sm"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-medium">
                        {o.user_email || `user#${o.user_id}`}
                      </div>
                      <div className="font-mono text-[11px] text-muted-foreground">
                        {o.out_trade_no.slice(0, 18)}… · {fmtTime(o.paid_at)}
                      </div>
                    </div>
                    <div className="shrink-0 font-mono text-sm font-semibold tabular-nums text-success">
                      +{fmtUSD(o.usd_credit)}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
        </div>

        <div>
          <Panel
            icon={<UserPlus className="h-3.5 w-3.5" />}
            eyebrow={t("admin.dashboard.recentSignupsEyebrow")}
            title={t("admin.dashboard.recentSignupsTitle")}
            action={
              <Link
                to="/app/admin/users"
                className="inline-flex items-center gap-0.5 text-xs text-primary transition-colors hover:text-primary/80"
              >
                {t("admin.dashboard.manageLink")} <ArrowUpRight className="h-3 w-3" />
              </Link>
            }
          >
            {data.recent_signups.length === 0 ? (
              <Empty>{t("admin.dashboard.noUsers")}</Empty>
            ) : (
              <ul className="divide-y divide-border/60">
                {data.recent_signups.map((u) => (
                  <li key={u.id} className="flex items-center justify-between gap-3 py-2.5 text-sm">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="truncate font-medium">{u.email}</span>
                        {u.role === "admin" && (
                          <Tag tone="primary">{t("admin.dashboard.badges.admin")}</Tag>
                        )}
                        {!u.verified && (
                          <Tag tone="warning">{t("admin.dashboard.badges.unverified")}</Tag>
                        )}
                        {u.disabled && (
                          <Tag tone="danger">{t("admin.dashboard.badges.disabled")}</Tag>
                        )}
                      </div>
                      <div className="font-mono text-[11px] text-muted-foreground">
                        id={u.id} · {fmtTime(u.created_at)}
                      </div>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
        </div>
      </div>
    </FadeIn>
  );
}

// Panel — a frosted glass section with an eyebrow + title header and an
// optional right-aligned action. The admin equivalent of the app's GlassPanel,
// kept local so the dashboard's denser header layout stays self-contained.
function Panel({
  icon,
  eyebrow,
  title,
  action,
  children,
}: {
  icon?: ReactNode;
  eyebrow: string;
  title: ReactNode;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="glass h-full rounded-2xl p-5">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="eyebrow flex items-center gap-1.5">
            {icon}
            {eyebrow}
          </div>
          <div className="mt-1 font-display text-xl font-semibold tracking-tight">{title}</div>
        </div>
        {action && <div className="shrink-0">{action}</div>}
      </div>
      {children}
    </div>
  );
}

function KpiTile({
  icon,
  label,
  value,
  sub,
  accent,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  sub?: string;
  accent?: "primary" | "warning";
}) {
  return (
    <SpotlightCard
      tiltDeg={0}
      className={cn(
        "h-full w-full p-4",
        accent === "primary" && "ring-1 ring-primary/30",
        accent === "warning" && "ring-1 ring-warning/30",
      )}
    >
      <div className="flex items-center justify-between">
        <span className="text-[11px] uppercase tracking-wider text-muted-foreground">{label}</span>
        <span
          className={cn(
            "grid h-7 w-7 place-items-center rounded-lg",
            accent === "primary"
              ? "bg-primary/15 text-primary"
              : accent === "warning"
                ? "bg-warning/15 text-warning"
                : "bg-muted/60 text-muted-foreground",
          )}
        >
          {icon}
        </span>
      </div>
      <div
        className={cn(
          "mt-2.5 font-mono text-2xl font-semibold tracking-tight tabular-nums md:text-3xl",
          accent === "primary" && "text-primary",
          accent === "warning" && "text-warning",
        )}
      >
        {value}
      </div>
      {sub && (
        <div className="mt-1.5 font-mono text-[11px] leading-tight text-muted-foreground">
          {sub}
        </div>
      )}
    </SpotlightCard>
  );
}

function Tag({ tone, children }: { tone: "primary" | "warning" | "danger"; children: ReactNode }) {
  return (
    <span
      className={cn(
        "rounded border px-1.5 py-0.5 font-mono text-[10px] uppercase",
        tone === "primary" && "border-primary/30 bg-primary/10 text-primary",
        tone === "warning" && "border-warning/30 bg-warning/10 text-warning",
        tone === "danger" && "border-destructive/30 bg-destructive/10 text-destructive",
      )}
    >
      {children}
    </span>
  );
}

function Empty({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-xl border border-dashed border-border-strong p-6 text-center text-sm italic text-muted-foreground">
      {children}
    </div>
  );
}

function DailyRevenueChart({ data }: { data: Daily[] }) {
  const { t } = useTranslation();
  if (data.length === 0) {
    return (
      <div className="grid h-32 place-items-center text-sm text-muted-foreground">
        {t("admin.dashboard.noData")}
      </div>
    );
  }
  const max = Math.max(...data.map((d) => d.amount), 1);
  const total = data.reduce((s, d) => s + d.amount, 0);
  const W = 100;
  const H = 60;
  const slot = W / data.length;
  const barW = slot * 0.62;

  return (
    <div>
      <div className="mb-3 font-mono text-xs text-muted-foreground">
        {t("admin.dashboard.revenueSum", {
          total: fmtUSD(total),
          days: data.length,
          peak: fmtUSD(max),
        })}
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className="block h-32 w-full overflow-visible"
        aria-label={t("admin.dashboard.revenueChartTitle")}
        role="img"
      >
        <title>{t("admin.dashboard.revenueChartTitle")}</title>
        <defs>
          <linearGradient id="adminRevBar" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--primary)" stopOpacity="0.95" />
            <stop offset="100%" stopColor="var(--primary)" stopOpacity="0.35" />
          </linearGradient>
        </defs>
        {data.map((d, i) => {
          const h = max > 0 ? (d.amount / max) * (H - 4) : 0;
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
              className={cn(d.amount === 0 && "fill-border")}
              style={d.amount > 0 ? { fill: "url(#adminRevBar)" } : undefined}
            >
              <title>{`${d.day}: ${fmtUSD(d.amount)}`}</title>
            </rect>
          );
        })}
      </svg>
      <div className="mt-2 flex justify-between font-mono text-[10px] text-muted-foreground">
        <span>{data[0]?.day.slice(5)}</span>
        <span>{data[Math.floor(data.length / 2)]?.day.slice(5)}</span>
        <span>{data[data.length - 1]?.day.slice(5)}</span>
      </div>
    </div>
  );
}

function fmtTime(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString();
}
