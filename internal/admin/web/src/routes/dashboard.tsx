import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowRight, Wallet, KeyRound, Activity } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/hooks/use-auth";
import { apiGet } from "@/lib/api";
import { fmtUSD } from "@/lib/utils";
import type { WalletTx, UserToken } from "@/lib/types";

export default function DashboardPage() {
  const { user, group } = useAuth();
  const [tx, setTx] = useState<WalletTx[]>([]);
  const [tokens, setTokens] = useState<UserToken[]>([]);

  useEffect(() => {
    apiGet<{ transactions: WalletTx[] }>("/billing/transactions").then((r) => setTx(r.transactions || []));
    apiGet<{ tokens: UserToken[] }>("/tokens").then((r) => setTokens(r.tokens || []));
  }, []);

  const charged = tx.filter((t) => t.kind === "charge").reduce((s, t) => s + Math.abs(t.amount_usd), 0);
  const topped = tx.filter((t) => t.kind === "topup").reduce((s, t) => s + t.amount_usd, 0);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Welcome back</h1>
        <p className="text-muted-foreground">{user?.email}</p>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <StatCard
          icon={Wallet}
          label="Wallet balance"
          value={fmtUSD(user?.balance_usd)}
          accent
          cta={{ to: "/app/billing", label: "Top up" }}
        />
        <StatCard
          icon={Activity}
          label="Total spent"
          value={fmtUSD(charged)}
          sub={`${tx.filter((t) => t.kind === "charge").length} requests billed`}
        />
        <StatCard
          icon={KeyRound}
          label="API tokens"
          value={String(tokens.length)}
          cta={{ to: "/app/tokens", label: "Manage" }}
          sub={`${tokens.filter((t) => !t.disabled).length} active`}
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Pricing tier</CardTitle>
          <CardDescription>{group?.Name ?? "default"} — {group?.Description}</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-4 md:grid-cols-2">
            <div className="rounded-lg border border-border-strong bg-card p-4">
              <div className="text-xs uppercase tracking-wider text-muted-foreground">Claude</div>
              <div className="mt-2 font-mono text-2xl font-semibold tabular-nums">{group?.ClaudeMultiplier?.toFixed(2)}×</div>
              <div className="mt-1 text-xs text-muted-foreground">final = upstream USD × multiplier</div>
            </div>
            <div className="rounded-lg border border-border-strong bg-card p-4">
              <div className="text-xs uppercase tracking-wider text-muted-foreground">Codex</div>
              <div className="mt-2 font-mono text-2xl font-semibold tabular-nums">{group?.CodexMultiplier?.toFixed(2)}×</div>
              <div className="mt-1 text-xs text-muted-foreground">final = upstream USD × multiplier</div>
            </div>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Recent activity</CardTitle>
          <CardDescription>Last {Math.min(tx.length, 10)} transactions</CardDescription>
        </CardHeader>
        <CardContent>
          {tx.length === 0 ? (
            <div className="rounded-md border border-dashed border-border-strong p-8 text-center text-sm text-muted-foreground">
              No transactions yet. <Link to="/app/billing" className="text-primary underline-offset-4 hover:underline">Top up your wallet</Link> to get started.
            </div>
          ) : (
            <div className="divide-y divide-border">
              {tx.slice(0, 10).map((t) => (
                <div key={t.id} className="flex items-center justify-between py-3 text-sm">
                  <div>
                    <div className="font-medium capitalize">{t.kind}</div>
                    <div className="text-xs text-muted-foreground">{new Date(t.created_at * 1000).toLocaleString()}</div>
                  </div>
                  <div className={`font-mono tabular-nums font-medium ${t.amount_usd >= 0 ? "text-success" : "text-foreground"}`}>
                    {t.amount_usd >= 0 ? "+" : ""}{fmtUSD(t.amount_usd)}
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function StatCard({ icon: Icon, label, value, sub, accent, cta }: any) {
  return (
    <Card className={accent ? "border-primary/40 bg-primary/[0.04]" : ""}>
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardDescription className="text-xs uppercase tracking-wider">{label}</CardDescription>
        <Icon className={`h-4 w-4 ${accent ? "text-primary" : "text-muted-foreground"}`} />
      </CardHeader>
      <CardContent>
        <div className="font-mono text-3xl font-semibold tabular-nums tracking-tight">{value}</div>
        {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
        {cta && (
          <Button asChild variant="ghost" size="sm" className="-ml-2 mt-3 gap-1 text-primary hover:text-primary">
            <Link to={cta.to}>
              {cta.label} <ArrowRight className="h-3 w-3" />
            </Link>
          </Button>
        )}
      </CardContent>
    </Card>
  );
}
