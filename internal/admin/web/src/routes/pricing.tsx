import {
  ArrowRight,
  ChevronDown,
  Gauge,
  Infinity as InfinityIcon,
  Minus,
  Plus,
  Receipt,
  Sparkles,
  Wallet,
} from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { lazy, Suspense, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { SpotlightCard } from "@/components/landing/interactions";
import { Reveal, RevealItem, RevealStagger } from "@/components/landing/reveal";
import { useIsMobile, usePrefersReducedMotion } from "@/components/landing/use-media";
import { PublicHeader } from "@/components/layout/shell";
import { api } from "@/lib/api";
import type { PricingGroup } from "@/lib/types";
import { cn } from "@/lib/utils";

const FloatingGeometry = lazy(() => import("@/components/landing/floating-geometry"));

const EASE = [0.22, 1, 0.36, 1] as const;

// Official Anthropic & OpenAI model pricing (USD per 1M tokens, as of May 2025)
const CLAUDE_MODELS = [
  {
    name: "claude-opus-4-8",
    display: "Claude Opus 4.8",
    tier: "flagship",
    input: 5.0,
    output: 25.0,
    cacheWrite: 6.25,
    cacheRead: 0.5,
  },
  {
    name: "claude-opus-4-7",
    display: "Claude Opus 4.7",
    tier: "advanced",
    input: 5.0,
    output: 25.0,
    cacheWrite: 6.25,
    cacheRead: 0.5,
  },
  {
    name: "claude-opus-4-6",
    display: "Claude Opus 4.6",
    tier: "advanced",
    input: 5.0,
    output: 25.0,
    cacheWrite: 6.25,
    cacheRead: 0.5,
  },
  {
    name: "claude-sonnet-4-6",
    display: "Claude Sonnet 4.6",
    tier: "balanced",
    input: 3.0,
    output: 15.0,
    cacheWrite: 3.75,
    cacheRead: 0.3,
  },
  {
    name: "claude-sonnet-4-5",
    display: "Claude Sonnet 4.5",
    tier: "standard",
    input: 3.0,
    output: 15.0,
    cacheWrite: 3.75,
    cacheRead: 0.3,
  },
  {
    name: "claude-haiku-4-5",
    display: "Claude Haiku 4.5",
    tier: "fast",
    input: 1.0,
    output: 5.0,
    cacheWrite: 1.25,
    cacheRead: 0.1,
  },
];

// Codex CLI OAuth models — covered by ChatGPT Plus/Pro/Team subscription.
const CODEX_OAUTH_MODELS = [
  {
    name: "gpt-5.5",
    display: "GPT-5.5",
    tier: "flagship",
    input: 5.0,
    output: 30.0,
    cacheWrite: null,
    cacheRead: 0.5,
  },
  {
    name: "gpt-5.4",
    display: "GPT-5.4",
    tier: "advanced",
    input: 2.5,
    output: 15.0,
    cacheWrite: null,
    cacheRead: 0.25,
  },
  {
    name: "gpt-5.4-mini",
    display: "GPT-5.4 mini",
    tier: "fast",
    input: 0.75,
    output: 4.5,
    cacheWrite: null,
    cacheRead: 0.075,
  },
  {
    name: "gpt-5.3-codex",
    display: "GPT-5.3 Codex",
    tier: "coding",
    input: 1.75,
    output: 14.0,
    cacheWrite: null,
    cacheRead: 0.175,
  },
  {
    name: "gpt-5.2",
    display: "GPT-5.2",
    tier: "standard",
    input: 1.5,
    output: 6.0,
    cacheWrite: null,
    cacheRead: null,
  },
];

type ModelRow = {
  name: string;
  display: string;
  tier: string;
  input: number;
  output: number;
  cacheWrite: number | null;
  cacheRead: number | null;
};

// Format a USD/M-token rate: up to 3 decimals, trailing zeros trimmed ($5, $2.4, $0.075).
function fmtPrice(n: number): string {
  return `$${n.toFixed(3).replace(/\.?0+$/, "")}`;
}

// ─── Hero ─────────────────────────────────────────────────────────────────────

function Hero() {
  const { t } = useTranslation();
  const isMobile = useIsMobile();
  const prefersReduced = usePrefersReducedMotion();
  const showAmbient = !isMobile && !prefersReduced;

  const chips = [
    { icon: InfinityIcon, label: t("pricing.chips.noSub") },
    { icon: Wallet, label: t("pricing.chips.noMin") },
    { icon: Receipt, label: t("pricing.chips.transparent") },
  ];

  return (
    <div className="relative overflow-hidden">
      {/* ambient floating geometry, far back, very subtle */}
      {showAmbient && (
        <div
          aria-hidden
          className="pointer-events-none absolute -right-24 -top-24 h-[420px] w-[420px] opacity-70 md:opacity-100"
        >
          <Suspense fallback={null}>
            <FloatingGeometry color="#34d399" />
          </Suspense>
        </div>
      )}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 -z-10"
        style={{
          background:
            "radial-gradient(60% 50% at 15% 0%, color-mix(in oklch, var(--primary) 12%, transparent), transparent 70%)",
        }}
      />
      <Reveal>
        <span className="eyebrow text-primary">{t("nav.pricing")}</span>
        <h1 className="mt-3 max-w-3xl font-display text-4xl font-semibold leading-[1.05] tracking-tight md:text-6xl">
          {t("pricing.title")}
        </h1>
        <p className="mt-4 max-w-2xl text-base text-muted-foreground md:text-lg">
          <Trans
            i18nKey="pricing.pageSub"
            components={{
              code: <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs" />,
            }}
          />
        </p>
      </Reveal>
      <Reveal delay={0.1} className="mt-7 flex flex-wrap gap-2.5">
        {chips.map((c) => (
          <span
            key={c.label}
            className="glass inline-flex items-center gap-2 rounded-full px-4 py-2 text-sm font-medium"
          >
            <c.icon className="h-4 w-4 text-primary" />
            {c.label}
          </span>
        ))}
      </Reveal>
    </div>
  );
}

// ─── Billing formula visualization — restrained skeuomorphic tiles ────────────

function FormulaTile({
  icon: Icon,
  label,
  hint,
  value,
  accent,
}: {
  icon: typeof Gauge;
  label: string;
  hint: string;
  value: string;
  accent?: boolean;
}) {
  return (
    <div
      className={cn(
        "glass relative flex flex-1 flex-col gap-3 rounded-2xl p-5",
        accent && "ring-1 ring-primary/30",
      )}
    >
      {/* engraved top highlight for a tactile, skeuomorphic edge */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 h-px"
        style={{
          background:
            "linear-gradient(90deg, transparent, color-mix(in oklch, var(--primary) 40%, transparent), transparent)",
        }}
      />
      <div
        className={cn(
          "inline-flex h-9 w-9 items-center justify-center rounded-lg",
          accent ? "bg-primary/15 text-primary" : "bg-muted/60 text-muted-foreground",
        )}
      >
        <Icon className="h-4.5 w-4.5" />
      </div>
      <div>
        <div className="text-xs uppercase tracking-wider text-muted-foreground">{label}</div>
        <div
          className={cn(
            "mt-1 font-display text-2xl font-semibold tracking-tight",
            accent && "text-primary",
          )}
        >
          {value}
        </div>
        <div className="mt-1 text-xs text-muted-foreground">{hint}</div>
      </div>
    </div>
  );
}

function Operator({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex shrink-0 items-center justify-center self-center py-1 md:py-0">
      <div className="flex h-9 w-9 items-center justify-center rounded-full border border-border bg-card/70 text-muted-foreground shadow-sm">
        {children}
      </div>
    </div>
  );
}

function BillingFormula() {
  const { t } = useTranslation();
  return (
    <div>
      <Reveal>
        <span className="eyebrow text-primary">{t("pricing.viz.eyebrow")}</span>
        <h2 className="mt-3 font-display text-2xl font-semibold tracking-tight md:text-3xl">
          {t("pricing.viz.title")}
        </h2>
        <p className="mt-2 max-w-2xl text-sm text-muted-foreground">{t("pricing.viz.sub")}</p>
      </Reveal>

      <RevealStagger className="mt-7 flex flex-col items-stretch gap-3 md:flex-row md:items-center">
        <RevealItem className="flex flex-1">
          <FormulaTile
            icon={Gauge}
            label={t("pricing.viz.official")}
            hint={t("pricing.viz.officialHint")}
            value="$X"
          />
        </RevealItem>
        <RevealItem>
          <Operator>
            <Plus className="h-4 w-4 rotate-45" />
          </Operator>
        </RevealItem>
        <RevealItem className="flex flex-1">
          <FormulaTile
            icon={Sparkles}
            label={t("pricing.viz.multiplier")}
            hint={t("pricing.viz.multiplierHint")}
            value="×N"
          />
        </RevealItem>
        <RevealItem>
          <Operator>
            <Minus className="h-4 w-4" />
            <Minus className="-ml-2 h-4 w-4" />
          </Operator>
        </RevealItem>
        <RevealItem className="flex flex-1">
          <FormulaTile
            icon={Wallet}
            label={t("pricing.viz.charged")}
            hint={t("pricing.viz.chargedHint")}
            value="$Y"
            accent
          />
        </RevealItem>
      </RevealStagger>

      <Reveal delay={0.1}>
        <div className="glass mt-4 flex items-center gap-2 rounded-xl px-4 py-3 font-mono text-xs text-muted-foreground sm:text-sm">
          <Receipt className="h-4 w-4 shrink-0 text-primary" />
          <span className="truncate">{t("pricing.formula")}</span>
        </div>
      </Reveal>
    </div>
  );
}

// ─── Model price bento with provider segmented control ────────────────────────

// One Input | Output price column inside a card. When the default group applies
// a discount (multiplier < 1) the official rate is struck through and the
// effective "ours" rate is emphasized in the brand color — mirroring the
// reference's official-vs-ours framing, but truthfully driven by the real
// default multiplier rather than a hardcoded discount.
function PriceCol({ label, official, mult }: { label: string; official: number; mult: number }) {
  const { t } = useTranslation();
  const discounted = mult < 1;
  return (
    <div className="flex-1 px-4 first:pl-0 last:pr-0">
      <div className="text-xs text-muted-foreground">{label}</div>
      {discounted ? (
        <>
          <div className="mt-1 flex items-center gap-1.5 text-xs text-muted-foreground">
            <span className="opacity-70">{t("pricing.official")}</span>
            <span className="font-mono line-through decoration-muted-foreground/40">
              {fmtPrice(official)}
            </span>
          </div>
          <div className="font-display text-2xl font-semibold tabular-nums text-primary">
            {fmtPrice(official * mult)}
          </div>
        </>
      ) : (
        <div className="mt-1 font-display text-2xl font-semibold tabular-nums">
          {fmtPrice(official)}
        </div>
      )}
      <div className="mt-0.5 text-[10px] uppercase tracking-wider text-muted-foreground">
        {t("pricing.unit")}
      </div>
    </div>
  );
}

function CacheRow({ label, value, mult }: { label: string; value: number | null; mult: number }) {
  return (
    <div className="flex items-center justify-between rounded-lg border border-border bg-background/40 px-3 py-2">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="font-mono text-sm tabular-nums">
        {value == null ? <span className="text-muted-foreground">—</span> : fmtPrice(value * mult)}
      </span>
    </div>
  );
}

function ModelCard({
  m,
  mult,
  featured,
  index,
  reduce,
}: {
  m: ModelRow;
  mult: number;
  featured?: boolean;
  index: number;
  reduce: boolean;
}) {
  const { t } = useTranslation();
  return (
    <motion.div
      initial={reduce ? false : { opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.45, delay: index * 0.05, ease: EASE }}
      className={cn(
        "glass group relative flex flex-col overflow-hidden rounded-2xl p-5 transition-transform duration-300 hover:-translate-y-0.5",
        featured && "sm:col-span-2 lg:row-span-2 lg:col-span-2",
      )}
    >
      {/* engraved top highlight */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 h-px"
        style={{
          background:
            "linear-gradient(90deg, transparent, color-mix(in oklch, var(--primary) 40%, transparent), transparent)",
        }}
      />
      {/* concentric-ripple badge on the featured card */}
      {featured && (
        <div aria-hidden className="pointer-events-none absolute -right-6 -top-6 opacity-60">
          <div className="relative h-28 w-28">
            <span className="absolute inset-0 rounded-full border border-primary/15" />
            <span className="absolute inset-4 rounded-full border border-primary/10" />
            <span className="absolute inset-8 rounded-full border border-primary/10" />
          </div>
        </div>
      )}

      <div className="relative flex items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-display text-sm font-medium text-foreground">
              {t(`pricing.tiers.${m.tier}`)}
            </span>
            <span className="text-xs text-muted-foreground">{t("pricing.modelTag")}</span>
          </div>
          <div
            className={cn(
              "mt-1 font-mono font-semibold text-primary",
              featured ? "text-xl md:text-2xl" : "text-base md:text-lg",
            )}
          >
            {m.name}
          </div>
        </div>
        {featured && (
          <span className="inline-flex shrink-0 items-center gap-1 rounded-full border border-primary/30 bg-primary/10 px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider text-primary">
            <Sparkles className="h-3 w-3" /> {t("pricing.flagship")}
          </span>
        )}
      </div>

      {/* inner pricing sub-card: Input | Output */}
      <div className="mt-4 flex divide-x divide-border rounded-xl border border-border bg-background/40 p-4">
        <PriceCol label={t("pricing.columns.input")} official={m.input} mult={mult} />
        <PriceCol label={t("pricing.columns.output")} official={m.output} mult={mult} />
      </div>

      {featured && (
        <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <CacheRow label={t("pricing.columns.cacheWrite")} value={m.cacheWrite} mult={mult} />
          <CacheRow label={t("pricing.columns.cacheRead")} value={m.cacheRead} mult={mult} />
        </div>
      )}

      {featured && <div className="flex-1" />}
    </motion.div>
  );
}

function ModelBento({ models, mult }: { models: ModelRow[]; mult: number }) {
  const { t } = useTranslation();
  const reduce = useReducedMotion();
  return (
    <div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4 lg:grid-rows-2">
        {models.map((m, i) => (
          <ModelCard
            key={m.name}
            m={m}
            mult={mult}
            featured={i === 0}
            index={i}
            reduce={!!reduce}
          />
        ))}
      </div>
      <p className="mt-4 text-xs text-muted-foreground">{t("pricing.per1m")}</p>
    </div>
  );
}

function ModelTables({ claudeMult, codexMult }: { claudeMult: number; codexMult: number }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState<"claude" | "codex">("claude");
  const tabs = [
    {
      id: "claude" as const,
      label: t("pricing.claudeTitle"),
      sub: t("pricing.claudeSub"),
      models: CLAUDE_MODELS,
      mult: claudeMult,
    },
    {
      id: "codex" as const,
      label: t("pricing.codexTitle"),
      sub: t("pricing.codexSub"),
      models: CODEX_OAUTH_MODELS,
      mult: codexMult,
    },
  ];
  // biome-ignore lint/style/noNonNullAssertion: tab state is always one of the two tab ids defined above
  const active = tabs.find((x) => x.id === tab)!;

  return (
    <div>
      <Reveal>
        <span className="eyebrow text-primary">{t("pricing.tablesEyebrow")}</span>
        <div className="mt-3 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="font-display text-2xl font-semibold tracking-tight md:text-3xl">
              {active.label}
            </h2>
            <p className="mt-1 max-w-xl text-sm text-muted-foreground">{active.sub}</p>
          </div>
          {/* segmented control */}
          <div className="glass inline-flex rounded-full p-1 text-sm">
            {tabs.map((x) => (
              <button
                type="button"
                key={x.id}
                onClick={() => setTab(x.id)}
                className={cn(
                  "relative rounded-full px-4 py-1.5 font-medium transition-colors",
                  tab === x.id
                    ? "text-primary-foreground"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {tab === x.id && (
                  <motion.span
                    layoutId="pricing-tab-pill"
                    className="absolute inset-0 -z-10 rounded-full bg-primary"
                    transition={{ type: "spring", stiffness: 360, damping: 30 }}
                  />
                )}
                {x.id === "claude" ? "Claude" : "Codex"}
              </button>
            ))}
          </div>
        </div>
      </Reveal>

      <Reveal delay={0.05} className="mt-6">
        <AnimatePresence mode="wait">
          <motion.div
            key={tab}
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.2 }}
          >
            <ModelBento models={active.models} mult={active.mult} />
          </motion.div>
        </AnimatePresence>
      </Reveal>
    </div>
  );
}

// ─── Access groups ────────────────────────────────────────────────────────────

function AccessGroups({ groups }: { groups: PricingGroup[] }) {
  const { t } = useTranslation();
  if (groups.length === 0) return null;
  return (
    <div>
      <Reveal>
        <span className="eyebrow text-primary">{t("pricing.accessGroupsTitle")}</span>
        <h2 className="mt-3 font-display text-2xl font-semibold tracking-tight md:text-3xl">
          {t("pricing.accessGroupsTitle")}
        </h2>
        <p className="mt-2 max-w-2xl text-sm text-muted-foreground">
          {t("pricing.accessGroupsSub")}
        </p>
      </Reveal>
      <RevealStagger className="mt-6 grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {groups.map((g) => (
          <RevealItem key={g.ID}>
            <SpotlightCard className={cn("h-full", g.IsDefault && "ring-1 ring-primary/40")}>
              <div className="flex items-center justify-between">
                <h3 className="font-display text-xl tracking-tight">{g.Name}</h3>
                {g.IsDefault && (
                  <span className="inline-flex items-center gap-1 rounded-full border border-primary/30 bg-primary/15 px-2 py-0.5 text-xs font-mono uppercase tracking-wider text-primary">
                    <Sparkles className="h-3 w-3" /> {t("pricing.defaultBadge")}
                  </span>
                )}
              </div>
              {g.Description && (
                <p className="mt-1 text-xs text-muted-foreground">{g.Description}</p>
              )}
              <div className="mt-4 space-y-2">
                <MultRow label="Claude" value={g.ClaudeMultiplier} />
                <MultRow label="Codex" value={g.CodexMultiplier} />
                <p className="pt-1 text-xs text-muted-foreground">{t("pricing.formulaCaption")}</p>
              </div>
            </SpotlightCard>
          </RevealItem>
        ))}
      </RevealStagger>
    </div>
  );
}

function MultRow({ label, value }: { label: string; value: number }) {
  const discount = value < 1;
  return (
    <div className="flex items-center justify-between rounded-lg border border-border bg-muted/30 p-3">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className="flex items-center gap-2">
        {discount && (
          <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider text-primary">
            −{Math.round((1 - value) * 100)}%
          </span>
        )}
        <span className="font-mono text-sm font-semibold tabular-nums">{value.toFixed(2)}×</span>
      </span>
    </div>
  );
}

// ─── FAQ accordion ────────────────────────────────────────────────────────────

function FaqItem({ q, a, defaultOpen }: { q: string; a: React.ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(!!defaultOpen);
  return (
    <div className="glass overflow-hidden rounded-2xl">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between gap-4 px-5 py-4 text-left"
      >
        <span className="font-medium">{q}</span>
        <ChevronDown
          className={cn(
            "h-4 w-4 shrink-0 text-muted-foreground transition-transform duration-300",
            open && "rotate-180 text-primary",
          )}
        />
      </button>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.3, ease: EASE }}
            className="overflow-hidden"
          >
            <div className="px-5 pb-5 text-sm leading-relaxed text-muted-foreground">{a}</div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

function Faq() {
  const { t } = useTranslation();
  const logsLink = (
    // biome-ignore lint/a11y/useAnchorContent: Trans component fills in the link text from the translation string
    <a className="text-primary underline-offset-2 hover:underline" href="/app/logs" />
  );
  return (
    <div>
      <Reveal>
        <span className="eyebrow text-primary">{t("pricing.faqEyebrow")}</span>
        <h2 className="mt-3 font-display text-2xl font-semibold tracking-tight md:text-3xl">
          {t("pricing.faqTitle")}
        </h2>
      </Reveal>
      <RevealStagger className="mt-6 space-y-3">
        <RevealItem>
          <FaqItem
            defaultOpen
            q={t("pricing.faq.q1")}
            a={<Trans i18nKey="pricing.faq.a1" components={{ logs: logsLink }} />}
          />
        </RevealItem>
        <RevealItem>
          <FaqItem q={t("pricing.faq.q2")} a={t("pricing.faq.a2")} />
        </RevealItem>
        <RevealItem>
          <FaqItem q={t("pricing.faq.q3")} a={t("pricing.faq.a3")} />
        </RevealItem>
      </RevealStagger>
    </div>
  );
}

// ─── CTA ──────────────────────────────────────────────────────────────────────

function PricingCta() {
  const { t } = useTranslation();
  return (
    <Reveal>
      <div className="glass relative overflow-hidden rounded-3xl px-6 py-10 text-center md:px-12 md:py-14">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 -z-10"
          style={{
            background:
              "radial-gradient(70% 120% at 50% 0%, color-mix(in oklch, var(--primary) 14%, transparent), transparent 70%)",
          }}
        />
        <h2 className="mx-auto max-w-2xl font-display text-2xl font-semibold tracking-tight md:text-3xl">
          {t("pricing.viz.title")}
        </h2>
        <p className="mx-auto mt-3 max-w-xl text-sm text-muted-foreground">{t("pricing.sub")}</p>
        <a
          href="/app/register"
          className="mt-6 inline-flex items-center gap-2 rounded-full bg-primary px-6 py-3 font-medium text-primary-foreground shadow-lg shadow-primary/20 transition-transform hover:scale-[1.03]"
        >
          {t("nav.signUp")}
          <ArrowRight className="h-4 w-4" />
        </a>
      </div>
    </Reveal>
  );
}

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function PricingPage({ embedded }: { embedded?: boolean }) {
  const [groups, setGroups] = useState<PricingGroup[]>([]);

  useEffect(() => {
    api<{ groups: PricingGroup[] }>("/groups").then((r) => setGroups(r.groups || []));
  }, []);

  // The default group's multipliers drive the "official vs ours" framing on the
  // model bento. Fall back to 1× (no discount) until groups load.
  const defaultGroup = groups.find((g) => g.IsDefault) ?? groups[0];
  const claudeMult = defaultGroup?.ClaudeMultiplier ?? 1;
  const codexMult = defaultGroup?.CodexMultiplier ?? 1;

  const content = (
    <div className="space-y-20 md:space-y-28">
      <Hero />
      <BillingFormula />
      <ModelTables claudeMult={claudeMult} codexMult={codexMult} />
      <AccessGroups groups={groups} />
      <Faq />
      {!embedded && <PricingCta />}
    </div>
  );

  if (embedded) return content;
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <PublicHeader />
      <div className="mx-auto max-w-6xl px-4 py-12 md:px-6 md:py-20">{content}</div>
    </div>
  );
}
