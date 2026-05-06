// Admin business dashboard — surfaces revenue, customer growth, and
// recent activity in a single fast-loading view backed by
// /api/v2/admin/dashboard. Distinct from the operator console (which
// focuses on credential / fleet health) and from the user-facing
// /app dashboard (per-user usage). Designed as the admin's "money
// view" so the operator can see at a glance what the platform is doing.

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import {
  Users,
  DollarSign,
  Wallet,
  TrendingUp,
  ArrowUpRight,
  Trophy,
  ShoppingCart,
  UserPlus,
  RefreshCw,
} from "lucide-react";
import { apiGet } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { fmtUSD } from "@/lib/utils";
import { cn } from "@/lib/utils";

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
  const [data, setData] = useState<DashboardResp | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const reload = async () => {
    setBusy(true);
    setErr("");
    try {
      const r = await apiGet<DashboardResp>("/admin/dashboard");
      setData(r);
    } catch (e: any) {
      setErr(e.message || "load failed");
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    reload();
    const id = setInterval(reload, 30_000);
    return () => clearInterval(id);
  }, []);

  if (!data && !err) {
    return (
      <div className="rounded-xl border border-border p-8 text-center text-muted-foreground text-sm">
        Loading admin snapshot…
      </div>
    );
  }
  if (err) {
    return (
      <div className="rounded-md border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive font-mono">
        {err}
      </div>
    );
  }
  if (!data) return null;

  const margin = data.revenue.topups_lifetime > 0
    ? ((data.revenue.topups_lifetime - data.balance.outstanding) / data.revenue.topups_lifetime) * 100
    : 0;
  const realized = data.revenue.topups_lifetime - data.balance.outstanding; // wallet drained = revenue earned

  return (
    <div className="space-y-6">
      {/* Hero header */}
      <header className="relative overflow-hidden rounded-xl border border-primary/30 bg-gradient-to-br from-primary/[0.08] via-primary/[0.04] to-transparent p-6 lg:p-8">
        <div className="absolute -right-24 -top-24 h-64 w-64 rounded-full bg-primary/10 blur-3xl pointer-events-none" />
        <div className="relative flex flex-wrap items-end justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 text-xs font-mono uppercase tracking-wider text-primary mb-2">
              <span className="relative inline-flex h-2 w-2">
                <span className="absolute inline-flex h-full w-full rounded-full bg-primary opacity-75 animate-ping" />
                <span className="relative inline-flex h-2 w-2 rounded-full bg-primary" />
              </span>
              Admin command center
            </div>
            <h1 className="font-display text-4xl lg:text-5xl font-semibold tracking-tight">
              Money <span className="text-muted-foreground/70">moves</span>
            </h1>
            <p className="mt-2 text-muted-foreground max-w-xl text-sm lg:text-base">
              Revenue, growth and outstanding balances at a glance — the operator's
              ledger view, refreshed every 30 seconds.
            </p>
          </div>
          <div className="flex items-baseline gap-1">
            <span className="font-mono tabular-nums text-5xl lg:text-6xl font-semibold tracking-tight text-primary">
              {fmtUSD(realized)}
            </span>
            <span className="text-xs font-mono uppercase tracking-wider text-muted-foreground ml-2">
              realized<br />revenue
            </span>
          </div>
        </div>
      </header>

      {/* KPI grid */}
      <div className="grid gap-3 grid-cols-2 lg:grid-cols-4">
        <KpiTile
          icon={<DollarSign className="h-4 w-4" />}
          label="Topups lifetime"
          value={fmtUSD(data.revenue.topups_lifetime)}
          sub={`+${fmtUSD(data.revenue.topups_30d)} last 30d · ${fmtUSD(data.revenue.topups_7d)} last 7d`}
          accent="primary"
        />
        <KpiTile
          icon={<TrendingUp className="h-4 w-4" />}
          label="Charges lifetime"
          value={fmtUSD(data.revenue.charges_lifetime)}
          sub={`${fmtUSD(data.revenue.charges_30d)} last 30d · ${fmtUSD(data.revenue.charges_7d)} last 7d`}
        />
        <KpiTile
          icon={<Wallet className="h-4 w-4" />}
          label="Outstanding wallet"
          value={fmtUSD(data.balance.outstanding)}
          sub={`liability · margin ${margin.toFixed(1)}%`}
          accent="warning"
        />
        <KpiTile
          icon={<Users className="h-4 w-4" />}
          label="Users"
          value={data.users.total.toString()}
          sub={`${data.users.verified} verified · +${data.users.new_30d} (30d) · ${data.users.disabled} disabled`}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        {/* Daily revenue chart */}
        <Card className="lg:col-span-2">
          <CardContent className="p-5">
            <div className="flex items-baseline justify-between mb-4">
              <div>
                <div className="text-xs font-mono uppercase tracking-wider text-muted-foreground">
                  Daily topups · last 14d
                </div>
                <div className="font-display text-xl font-semibold mt-1">Revenue rhythm</div>
              </div>
              <Button variant="ghost" size="sm" onClick={reload} disabled={busy} className="gap-1.5">
                <RefreshCw className={cn("h-3.5 w-3.5", busy && "animate-spin")} />
                <span className="text-xs">Refresh</span>
              </Button>
            </div>
            <DailyRevenueChart data={data.daily_revenue} />
          </CardContent>
        </Card>

        {/* Top spenders */}
        <Card>
          <CardContent className="p-5">
            <div className="text-xs font-mono uppercase tracking-wider text-muted-foreground mb-1">
              <Trophy className="inline-block h-3.5 w-3.5 align-[-2px] mr-1" />
              Top spenders
            </div>
            <div className="font-display text-xl font-semibold mb-4">Revenue leaders</div>
            {data.top_spenders.length === 0 ? (
              <div className="text-sm text-muted-foreground italic">No charges yet.</div>
            ) : (
              <ul className="space-y-2">
                {data.top_spenders.map((u, i) => (
                  <li key={u.user_id} className="flex items-center gap-3">
                    <span className="font-mono text-xs text-muted-foreground w-5 text-right">
                      {i + 1}
                    </span>
                    <div className="flex-1 min-w-0">
                      <div className="text-sm truncate font-medium">{u.email}</div>
                      <div className="text-[11px] text-muted-foreground font-mono">id={u.user_id}</div>
                    </div>
                    <span className="font-mono tabular-nums text-sm font-semibold">{fmtUSD(u.spent)}</span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {/* Recent topups */}
        <Card>
          <CardContent className="p-5">
            <div className="flex items-baseline justify-between mb-4">
              <div>
                <div className="text-xs font-mono uppercase tracking-wider text-muted-foreground">
                  <ShoppingCart className="inline-block h-3.5 w-3.5 align-[-2px] mr-1" />
                  Recent topups
                </div>
                <div className="font-display text-xl font-semibold mt-1">
                  Last {data.recent_topups.length} payments
                </div>
              </div>
              <span className="text-xs text-muted-foreground font-mono">
                {data.balance.orders_paid_lifetime} all-time · {data.balance.orders_pending} pending
              </span>
            </div>
            {data.recent_topups.length === 0 ? (
              <div className="text-sm text-muted-foreground italic">No paid orders yet.</div>
            ) : (
              <ul className="divide-y divide-border">
                {data.recent_topups.map((o) => (
                  <li key={o.out_trade_no} className="py-2.5 flex items-center justify-between gap-3 text-sm">
                    <div className="min-w-0 flex-1">
                      <div className="font-medium truncate">{o.user_email || `user#${o.user_id}`}</div>
                      <div className="text-[11px] text-muted-foreground font-mono">
                        {o.out_trade_no.slice(0, 18)}… · {fmtTime(o.paid_at)}
                      </div>
                    </div>
                    <div className="text-right shrink-0">
                      <div className="font-mono tabular-nums font-semibold">+{fmtUSD(o.usd_credit)}</div>
                      <div className="text-[11px] text-muted-foreground font-mono">¥{o.cny_amount.toFixed(2)}</div>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        {/* Recent signups */}
        <Card>
          <CardContent className="p-5">
            <div className="flex items-baseline justify-between mb-4">
              <div>
                <div className="text-xs font-mono uppercase tracking-wider text-muted-foreground">
                  <UserPlus className="inline-block h-3.5 w-3.5 align-[-2px] mr-1" />
                  Recent signups
                </div>
                <div className="font-display text-xl font-semibold mt-1">New customers</div>
              </div>
              <Link
                to="/app/admin/users"
                className="text-xs text-primary hover:underline inline-flex items-center gap-0.5"
              >
                manage <ArrowUpRight className="h-3 w-3" />
              </Link>
            </div>
            {data.recent_signups.length === 0 ? (
              <div className="text-sm text-muted-foreground italic">No users yet.</div>
            ) : (
              <ul className="divide-y divide-border">
                {data.recent_signups.map((u) => (
                  <li key={u.id} className="py-2.5 flex items-center justify-between gap-3 text-sm">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="font-medium truncate">{u.email}</span>
                        {u.role === "admin" && (
                          <span className="rounded border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-[10px] font-mono uppercase text-primary">
                            admin
                          </span>
                        )}
                        {!u.verified && (
                          <span className="rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-mono uppercase text-amber-500">
                            unverified
                          </span>
                        )}
                        {u.disabled && (
                          <span className="rounded border border-destructive/30 bg-destructive/10 px-1.5 py-0.5 text-[10px] font-mono uppercase text-destructive">
                            disabled
                          </span>
                        )}
                      </div>
                      <div className="text-[11px] text-muted-foreground font-mono">
                        id={u.id} · {fmtTime(u.created_at)}
                      </div>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
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
  icon: React.ReactNode;
  label: string;
  value: string;
  sub?: string;
  accent?: "primary" | "warning";
}) {
  return (
    <div
      className={cn(
        "rounded-lg border p-4 transition-colors",
        accent === "primary"
          ? "border-primary/30 bg-primary/[0.05]"
          : accent === "warning"
            ? "border-amber-500/30 bg-amber-500/[0.05]"
            : "border-border bg-card",
      )}
    >
      <div
        className={cn(
          "flex items-center gap-1.5 text-[11px] font-mono uppercase tracking-wider",
          accent === "primary" ? "text-primary" : accent === "warning" ? "text-amber-500" : "text-muted-foreground",
        )}
      >
        {icon}
        {label}
      </div>
      <div className="mt-2 font-mono text-2xl md:text-3xl font-semibold tabular-nums tracking-tight">
        {value}
      </div>
      {sub && (
        <div className="mt-1.5 text-[11px] text-muted-foreground font-mono leading-tight">{sub}</div>
      )}
    </div>
  );
}

// DailyRevenueChart renders 14 vertical bars with hover tooltips. Plain
// SVG — no charting library dependency.
function DailyRevenueChart({ data }: { data: Daily[] }) {
  if (data.length === 0) {
    return <div className="h-32 grid place-items-center text-sm text-muted-foreground">No data</div>;
  }
  const max = Math.max(...data.map((d) => d.amount), 1);
  const total = data.reduce((s, d) => s + d.amount, 0);
  const W = 100;
  const H = 60;
  const slot = W / data.length;
  const barW = slot * 0.7;

  return (
    <div>
      <div className="text-xs text-muted-foreground font-mono mb-2">
        Σ <span className="text-foreground font-semibold">{fmtUSD(total)}</span> over {data.length} days
        · peak <span className="text-foreground">{fmtUSD(max)}</span>
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className="block h-32 w-full"
      >
        {data.map((d, i) => {
          const h = max > 0 ? (d.amount / max) * (H - 4) : 0;
          const x = i * slot + (slot - barW) / 2;
          const y = H - h;
          return (
            <g key={d.day}>
              <rect
                x={x}
                y={y}
                width={barW}
                height={h || 0.5}
                rx="0.5"
                className={cn(
                  d.amount > 0 ? "fill-primary" : "fill-border",
                )}
              >
                <title>{`${d.day}: ${fmtUSD(d.amount)}`}</title>
              </rect>
            </g>
          );
        })}
      </svg>
      <div className="mt-2 flex justify-between text-[10px] text-muted-foreground font-mono">
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
