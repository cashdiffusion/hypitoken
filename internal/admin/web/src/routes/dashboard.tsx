import { Activity, ArrowUpRight, Gauge, KeyRound, Wallet } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation } from "react-router-dom";
import { CountUp, GlassPanel, PageHeader, StatTile } from "@/components/app/page-primitives";
import { WelcomeBonus } from "@/components/app/welcome-bonus";
import { SpotlightCard } from "@/components/landing/interactions";
import { Reveal, RevealItem, RevealStagger } from "@/components/landing/reveal";
import { useAuth } from "@/hooks/use-auth";
import { apiGet } from "@/lib/api";
import type { UserToken, WalletTx } from "@/lib/types";
import { fmtUSD } from "@/lib/utils";

export default function DashboardPage() {
  const { user, group } = useAuth();
  const { t } = useTranslation();
  const location = useLocation();
  const [tx, setTx] = useState<WalletTx[]>([]);
  const [tokens, setTokens] = useState<UserToken[]>([]);

  // Welcome bonus: register.tsx routes here with `state.welcomeBonus` (the
  // credited USD) right after a brand-new signup. Capture it once, then strip
  // the history state so a refresh or back-nav never replays the celebration.
  const [welcomeBonus, setWelcomeBonus] = useState<number>(() => {
    const b = (location.state as { welcomeBonus?: number } | null)?.welcomeBonus;
    return typeof b === "number" && b > 0 ? b : 0;
  });
  useEffect(() => {
    if ((location.state as { welcomeBonus?: number } | null)?.welcomeBonus) {
      window.history.replaceState({}, "");
    }
  }, [location.state]);

  useEffect(() => {
    apiGet<{ transactions: WalletTx[] }>("/billing/transactions").then((r) =>
      setTx(r.transactions || []),
    );
    apiGet<{ tokens: UserToken[] }>("/tokens").then((r) => setTokens(r.tokens || []));
  }, []);

  const charged = tx
    .filter((t) => t.kind === "charge")
    .reduce((s, t) => s + Math.abs(t.amount_usd), 0);
  const chargeCount = tx.filter((t) => t.kind === "charge").length;
  const activeTokens = tokens.filter((t) => !t.disabled).length;

  return (
    <div className="space-y-8">
      {welcomeBonus > 0 && (
        <WelcomeBonus amount={welcomeBonus} onDismiss={() => setWelcomeBonus(0)} />
      )}
      <PageHeader eyebrow={t("nav.dashboard")} title={t("dashboard.welcome")} sub={user?.email} />

      <RevealStagger className="grid gap-4 md:grid-cols-3">
        <RevealItem className="flex">
          <StatTile
            icon={Wallet}
            label={t("dashboard.walletBalance")}
            value={<CountUp value={user?.balance_usd ?? 0} format={(n) => fmtUSD(n)} />}
            accent
            cta={{ to: "/app/billing", label: t("dashboard.topUp") }}
          />
        </RevealItem>
        <RevealItem className="flex">
          <StatTile
            icon={Activity}
            label={t("dashboard.totalSpent")}
            value={<CountUp value={charged} format={(n) => fmtUSD(n)} />}
            sub={t("dashboard.requestsBilled", { n: chargeCount })}
          />
        </RevealItem>
        <RevealItem className="flex">
          <StatTile
            icon={KeyRound}
            label={t("dashboard.apiTokens")}
            value={<CountUp value={tokens.length} format={(n) => String(Math.round(n))} />}
            cta={{ to: "/app/tokens", label: t("dashboard.manage") }}
            sub={t("dashboard.nActive", { n: activeTokens })}
          />
        </RevealItem>
      </RevealStagger>

      {/* Pricing tier */}
      <Reveal>
        <GlassPanel
          title={
            <span className="flex items-center gap-2">
              <Gauge className="h-4.5 w-4.5 text-primary" />
              {t("dashboard.pricingTier")}
            </span>
          }
          description={`${group?.Name ?? "default"}${group?.Description ? ` — ${group.Description}` : ""}`}
        >
          <div className="grid gap-4 md:grid-cols-2">
            <MultiplierCard
              label="Claude"
              value={group?.ClaudeMultiplier}
              hint={t("dashboard.multiplierExplanation")}
            />
            <MultiplierCard
              label="Codex"
              value={group?.CodexMultiplier}
              hint={t("dashboard.multiplierExplanation")}
            />
          </div>
        </GlassPanel>
      </Reveal>

      {/* Recent activity */}
      <Reveal>
        <GlassPanel
          title={t("dashboard.recentActivity")}
          description={t("dashboard.lastNTransactions", { n: Math.min(tx.length, 10) })}
          bodyClassName="p-0"
        >
          {tx.length === 0 ? (
            <div className="m-5 rounded-xl border border-dashed border-border-strong p-8 text-center text-sm text-muted-foreground md:m-6">
              {t("dashboard.noTxYet")}{" "}
              <Link to="/app/billing" className="text-primary underline-offset-4 hover:underline">
                {t("dashboard.topUpYourWallet")}
              </Link>
              {t("dashboard.toGetStarted")}
            </div>
          ) : (
            <div className="divide-y divide-border/60">
              {tx.slice(0, 10).map((row) => (
                <div
                  key={row.id}
                  className="flex items-center justify-between px-5 py-3 text-sm transition-colors hover:bg-primary/[0.03] md:px-6"
                >
                  <div>
                    <div className="font-medium capitalize">{row.kind}</div>
                    <div className="text-xs text-muted-foreground">
                      {new Date(row.created_at * 1000).toLocaleString()}
                    </div>
                  </div>
                  <div
                    className={`font-mono font-medium tabular-nums ${row.amount_usd >= 0 ? "text-success" : "text-foreground"}`}
                  >
                    {row.amount_usd >= 0 ? "+" : ""}
                    {fmtUSD(row.amount_usd)}
                  </div>
                </div>
              ))}
            </div>
          )}
        </GlassPanel>
      </Reveal>
    </div>
  );
}

function MultiplierCard({ label, value, hint }: { label: string; value?: number; hint: string }) {
  const discount = value != null && value < 1;
  return (
    <SpotlightCard tiltDeg={0} className="rounded-xl p-4">
      <div className="flex items-center justify-between">
        <span className="text-xs uppercase tracking-wider text-muted-foreground">{label}</span>
        {discount && (
          <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider text-primary">
            −{Math.round((1 - (value ?? 1)) * 100)}%
          </span>
        )}
      </div>
      <div className="mt-2 font-mono text-2xl font-semibold tabular-nums">{value?.toFixed(2)}×</div>
      <div className="mt-1 flex items-center gap-1 text-xs text-muted-foreground">
        {hint}
        <ArrowUpRight className="h-3 w-3 opacity-0" />
      </div>
    </SpotlightCard>
  );
}
