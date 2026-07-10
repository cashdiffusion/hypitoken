import { Activity, ArrowUpRight, Gauge, Gift, KeyRound, Pencil, Wallet } from "lucide-react";
import { AnimatePresence } from "motion/react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { NicknameDialog } from "@/components/app/nickname-dialog";
import { CountUp, GlassPanel, PageHeader, StatTile } from "@/components/app/page-primitives";
import { TermsNotice } from "@/components/app/terms-notice";
import { WelcomeBonus } from "@/components/app/welcome-bonus";
import { SpotlightCard } from "@/components/landing/interactions";
import { Reveal, RevealItem, RevealStagger } from "@/components/landing/reveal";
import { useAuth } from "@/hooks/use-auth";
import { apiGet } from "@/lib/api";
import type { Greeting, UserToken, WalletTx } from "@/lib/types";
import { fmtUSD } from "@/lib/utils";

export default function DashboardPage() {
  const { user } = useAuth();
  const { t } = useTranslation();
  // Pricing card reflects the user's BILLING workspace rate (not the legacy
  // pricing group). Prefer the enterprise space (its discounted rate is the
  // headline for company members); fall back to the personal/standard space.
  const wsList = user?.workspaces ?? [];
  const billingWs =
    wsList.find((w) => w.type === "enterprise") ??
    wsList.find((w) => w.type === "personal") ??
    wsList[0];
  const location = useLocation();
  const navigate = useNavigate();
  const [tx, setTx] = useState<WalletTx[]>([]);
  const [tokens, setTokens] = useState<UserToken[]>([]);
  // Account usage summary — the source of truth for lifetime spend. We pull it
  // from /me/console (wallet-ledger charge total) rather than tallying the
  // transactions list, which by default hides charge rows (so a naive sum was
  // always 0). total.count is the billed-request count.
  const [usage, setUsage] = useState<{ spent_total: number; total: { count: number } } | null>(
    null,
  );
  const [greet, setGreet] = useState<Greeting | null>(null);
  const [nickOpen, setNickOpen] = useState(false);

  // Welcome overlay: register.tsx routes here right after a brand-new signup
  // with either `state.welcomeBonus` (credited USD → celebration) or
  // `state.fraud` (suspected repeat device → "bonus withheld" notice). Capture
  // once, then strip the history state so a refresh/back-nav never replays it.
  const [welcome, setWelcome] = useState<{ bonus: number; fraud: boolean } | null>(() => {
    const st = location.state as { welcomeBonus?: number; fraud?: boolean } | null;
    const bonus = typeof st?.welcomeBonus === "number" && st.welcomeBonus > 0 ? st.welcomeBonus : 0;
    const fraud = st?.fraud === true;
    return bonus > 0 || fraud ? { bonus, fraud } : null;
  });
  useEffect(() => {
    const st = location.state as { welcomeBonus?: number; fraud?: boolean } | null;
    if (st?.welcomeBonus || st?.fraud) {
      navigate(location.pathname, { replace: true, state: null });
    }
  }, [location.pathname, location.state, navigate]);

  useEffect(() => {
    apiGet<{ transactions: WalletTx[] }>("/billing/transactions").then((r) =>
      setTx(r.transactions || []),
    );
    apiGet<{ tokens: UserToken[] }>("/tokens").then((r) => setTokens(r.tokens || []));
    apiGet<{ spent_total: number; total: { count: number } }>("/me/console")
      .then(setUsage)
      .catch(() => setUsage(null));
    apiGet<Greeting>("/me/greeting")
      .then(setGreet)
      .catch(() => setGreet(null));
  }, []);

  const charged = usage?.spent_total ?? 0;
  const chargeCount = usage?.total?.count ?? 0;
  const activeTokens = tokens.filter((t) => !t.disabled).length;

  return (
    <div className="space-y-8">
      <TermsNotice />
      <AnimatePresence>
        {welcome && (
          <WelcomeBonus
            amount={welcome.bonus}
            fraud={welcome.fraud}
            onDismiss={() => setWelcome(null)}
          />
        )}
      </AnimatePresence>
      <PageHeader
        eyebrow={greetLine(t)}
        title={
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
            {t(`dashboard.greeting.${periodKey()}`, {
              name: user?.display_name || t("arena.you"),
            })}
            <button
              type="button"
              onClick={() => setNickOpen(true)}
              title={t("dashboard.greeting.editNick")}
              className="grid h-7 w-7 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-primary/10 hover:text-primary"
            >
              <Pencil className="h-3.5 w-3.5" />
            </button>
          </span>
        }
        sub={greetSub(greet, t)}
      />

      {user?.name_is_default && (
        <Reveal>
          <button
            type="button"
            onClick={() => setNickOpen(true)}
            className="flex w-full items-center gap-2 rounded-xl border border-amber-500/30 bg-amber-500/10 px-4 py-2.5 text-left text-sm text-amber-700 transition-colors hover:bg-amber-500/15 dark:text-amber-300"
          >
            <Pencil className="h-4 w-4 shrink-0" />
            {t("dashboard.greeting.defaultNickNotice")}
          </button>
        </Reveal>
      )}

      <NicknameDialog open={nickOpen} onOpenChange={setNickOpen} />

      <Reveal>
        <Link
          to="/app/invite"
          viewTransition
          className="group flex items-center gap-3 rounded-xl border border-primary/25 bg-primary/5 px-4 py-3 text-sm transition-colors hover:bg-primary/10"
        >
          <Gift className="h-5 w-5 shrink-0 text-primary" />
          <span className="flex-1 text-foreground">{t("dashboard.inviteCta")}</span>
          <ArrowUpRight className="h-4 w-4 shrink-0 text-primary transition-transform group-hover:translate-x-0.5 group-hover:-translate-y-0.5" />
        </Link>
      </Reveal>

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
          description={
            billingWs
              ? `${billingWs.name}${billingWs.type === "enterprise" ? ` · ${t("dashboard.enterpriseRate")}` : ` · ${t("dashboard.standardRate")}`}`
              : t("dashboard.standardRate")
          }
        >
          <div className="grid gap-4 md:grid-cols-2">
            <MultiplierCard
              label="Claude"
              value={billingWs?.claude_multiplier ?? 0.3}
              hint={t("dashboard.multiplierExplanation")}
            />
            <MultiplierCard
              label="Codex"
              value={billingWs?.codex_multiplier ?? 0.05}
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

// periodKey maps the browser-local hour to a greeting bucket (all offline).
function periodKey(): "morning" | "afternoon" | "evening" | "night" {
  const h = new Date().getHours();
  if (h < 5) return "night";
  if (h < 12) return "morning";
  if (h < 18) return "afternoon";
  if (h < 23) return "evening";
  return "night";
}

// greetLine is the small eyebrow above the greeting — just the localized
// dashboard label (kept simple; the personality lives in the title + sub).
function greetLine(t: (k: string) => string): string {
  return t("nav.dashboard");
}

// greetSub builds the location-flavoured sub-line. Uses the browser-native,
// fully-offline Intl.DisplayNames to localize the country code; degrades to a
// generic line when no location signal is available.
function greetSub(greet: { city: string; country_code: string } | null, t: TFn): string {
  const place = placeName(greet);
  if (place) return t("dashboard.greeting.inCity", { city: place });
  return t("dashboard.greeting.generic");
}

type TFn = (k: string, o?: Record<string, unknown>) => string;

function placeName(greet: { city: string; country_code: string } | null): string {
  if (!greet) return "";
  if (greet.city) return greet.city;
  if (greet.country_code && greet.country_code.length === 2) {
    try {
      const lang = (typeof navigator !== "undefined" && navigator.language) || "en";
      const dn = new Intl.DisplayNames([lang], { type: "region" });
      return dn.of(greet.country_code) || "";
    } catch {
      return "";
    }
  }
  return "";
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
