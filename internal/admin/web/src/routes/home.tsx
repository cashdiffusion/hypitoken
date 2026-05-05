import { Link } from "react-router-dom";
import {
  ArrowRight, Sparkles, Wallet, Activity, ShieldCheck, KeyRound, Zap,
  Terminal, Globe2, Check, Code2, Boxes, BookOpen, Github
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { PublicHeader } from "@/components/layout/shell";

export default function HomePage() {
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <PublicHeader />

      {/* Hero — single-column, terminal below as a separate band */}
      <section className="relative overflow-hidden border-b border-border">
        <BackgroundMesh />
        <Grid />
        <div className="relative mx-auto max-w-6xl px-4 py-20 text-center md:px-6 md:py-28">
          <span className="inline-flex items-center gap-2 rounded-full border border-primary/30 bg-primary/10 px-3 py-1 text-xs font-mono uppercase tracking-wider text-primary backdrop-blur">
            <Sparkles className="h-3 w-3" /> Pay-as-you-go LLM gateway
          </span>
          <h1 className="mx-auto mt-6 max-w-4xl text-balance font-display text-[2.25rem] font-semibold leading-[1.08] tracking-tight sm:text-5xl md:text-6xl lg:text-7xl"
              style={{ overflowWrap: "break-word" }}>
            Claude & Codex,{" "}
            <span
              className="bg-clip-text text-transparent"
              style={{ backgroundImage: "linear-gradient(120deg, var(--color-primary), var(--color-info))" }}
            >
              priced like RMB.
            </span>
          </h1>
          <p className="mx-auto mt-6 max-w-2xl text-balance text-lg text-muted-foreground md:text-xl">
            Top up real USD via Alipay. Run any model through one bearer token.
            Codex billed at <strong className="text-foreground">¥0.5/USD</strong>,
            Claude at <strong className="text-foreground">¥2/USD</strong> — pegged below market.
          </p>
          <div className="mt-10 flex flex-wrap items-center justify-center gap-3">
            <Button asChild size="lg" className="gap-2 text-base">
              <Link to="/register">
                Create account <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
            <Button asChild variant="outline" size="lg" className="text-base">
              <Link to="/docs">
                <BookOpen className="mr-2 h-4 w-4" /> Read the docs
              </Link>
            </Button>
          </div>
          <div className="mt-8 flex flex-wrap items-center justify-center gap-x-6 gap-y-2 text-sm text-muted-foreground">
            <span className="inline-flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" /> $1 minimum top-up</span>
            <span className="inline-flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" /> No subscription</span>
            <span className="inline-flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" /> SDK-compatible</span>
          </div>

          {/* Terminal demo — full width below the hero text, framed properly */}
          <div className="relative mx-auto mt-16 max-w-4xl">
            <TerminalDemo />
          </div>
        </div>
      </section>

      {/* Compatible-with strip */}
      <section className="border-b border-border bg-muted/20">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-center gap-x-12 gap-y-4 px-4 py-8 md:px-6">
          <span className="text-xs font-mono uppercase tracking-wider text-muted-foreground">Drop-in compatible with</span>
          <Logo label="Claude Code" />
          <Logo label="Codex CLI" />
          <Logo label="Anthropic SDK" />
          <Logo label="OpenAI SDK" />
          <Logo label="LiteLLM" />
        </div>
      </section>

      {/* Features grid */}
      <section className="mx-auto max-w-7xl px-4 py-24 md:px-6">
        <div className="mb-12 max-w-2xl">
          <span className="text-xs font-mono uppercase tracking-wider text-primary">Engineering</span>
          <h2 className="mt-2 font-display text-4xl font-semibold tracking-tight md:text-5xl">Built for serious load.</h2>
          <p className="mt-3 text-lg text-muted-foreground">
            One reverse proxy in front of dozens of upstream credentials. Health checks, sticky sessions,
            per-token caps — no surprises at scale.
          </p>
        </div>
        <div className="grid gap-px overflow-hidden rounded-xl border border-border bg-border md:grid-cols-2 lg:grid-cols-3">
          {FEATURES.map((f) => (
            <div key={f.title} className="group bg-background p-6 transition-colors hover:bg-card">
              <div className="grid h-10 w-10 place-items-center rounded-lg bg-primary/10 text-primary transition-transform group-hover:scale-110">
                <f.icon className="h-5 w-5" />
              </div>
              <h3 className="mt-5 font-display text-xl font-medium tracking-tight">{f.title}</h3>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{f.body}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Pricing math callout */}
      <section className="relative overflow-hidden border-y border-border bg-card/30">
        <div className="mx-auto max-w-7xl px-4 py-20 md:px-6">
          <div className="grid items-center gap-12 lg:grid-cols-2">
            <div>
              <span className="text-xs font-mono uppercase tracking-wider text-primary">Math</span>
              <h2 className="mt-2 font-display text-4xl font-semibold tracking-tight md:text-5xl">A nicer rate, transparently.</h2>
              <p className="mt-4 max-w-lg text-lg text-muted-foreground">
                Each request is billed at the official cost <em>scaled by your tier's RMB peg</em>.
                No fixed markup, no usage tier surprises — the formula is on every receipt.
              </p>
              <div className="mt-8 space-y-3">
                <FormulaLine token="bill_usd" expr="official × (peg_rmb ÷ live_cny) × multiplier" />
              </div>
              <Button asChild variant="outline" className="mt-8">
                <Link to="/pricing" className="gap-2">See all pricing tiers <ArrowRight className="h-4 w-4" /></Link>
              </Button>
            </div>
            <PricingCards />
          </div>
        </div>
      </section>

      {/* How it works */}
      <section className="mx-auto max-w-7xl px-4 py-24 md:px-6">
        <div className="mb-16 max-w-2xl">
          <span className="text-xs font-mono uppercase tracking-wider text-primary">Workflow</span>
          <h2 className="mt-2 font-display text-4xl font-semibold tracking-tight md:text-5xl">Wire it in, three steps.</h2>
        </div>
        <div className="grid gap-6 md:grid-cols-3">
          {STEPS.map((s, i) => (
            <div key={s.title} className="relative rounded-xl border border-border bg-card p-6">
              <div className="font-mono text-5xl font-semibold leading-none text-primary/30">{(i + 1).toString().padStart(2, "0")}</div>
              <h3 className="mt-4 font-display text-xl font-medium tracking-tight">{s.title}</h3>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{s.body}</p>
            </div>
          ))}
        </div>
      </section>

      {/* CTA */}
      <section className="relative overflow-hidden border-t border-border">
        <BackgroundMesh />
        <div className="relative mx-auto max-w-7xl px-4 py-20 md:px-6">
          <div className="rounded-2xl border border-border-strong bg-card p-10 md:p-14">
            <div className="flex flex-col items-start justify-between gap-8 md:flex-row md:items-center">
              <div>
                <h2 className="font-display text-3xl font-semibold tracking-tight md:text-4xl">Stop paying retail.</h2>
                <p className="mt-2 max-w-md text-muted-foreground">$1 to start. No card, no contract — just Alipay and a bearer token.</p>
              </div>
              <div className="flex gap-3">
                <Button asChild size="lg" className="gap-2 text-base">
                  <Link to="/register">Start free <ArrowRight className="h-4 w-4" /></Link>
                </Button>
                <Button asChild size="lg" variant="outline" className="text-base">
                  <Link to="/docs">Read docs</Link>
                </Button>
              </div>
            </div>
          </div>
        </div>
      </section>

      <footer className="border-t border-border bg-card/20">
        <div className="mx-auto grid max-w-7xl gap-8 px-4 py-12 md:grid-cols-4 md:px-6">
          <div className="md:col-span-2">
            <Link to="/" className="flex items-center gap-2 font-display text-xl font-semibold">
              <span className="grid h-7 w-7 place-items-center rounded-md bg-primary text-primary-foreground">
                <KeyRound className="h-3.5 w-3.5" />
              </span>
              HypiToken
            </Link>
            <p className="mt-3 max-w-xs text-sm text-muted-foreground">A tenant-multiplexed proxy in front of Anthropic and OpenAI. Pay-as-you-go in real USD via Alipay.</p>
          </div>
          <FooterCol title="Product" links={[{ to: "/pricing", label: "Pricing" }, { to: "/status", label: "Status" }, { to: "/docs", label: "Documentation" }]} />
          <FooterCol title="Account" links={[{ to: "/register", label: "Sign up" }, { to: "/login", label: "Sign in" }]} />
        </div>
        <div className="border-t border-border">
          <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-5 text-xs text-muted-foreground md:px-6">
            <span>© {new Date().getFullYear()} HypiToken</span>
            <div className="flex items-center gap-4 font-mono">
              <span>v2 · saas</span>
            </div>
          </div>
        </div>
      </footer>
    </div>
  );
}

function FooterCol({ title, links }: { title: string; links: Array<{ to: string; label: string }> }) {
  return (
    <div>
      <h4 className="font-display text-sm font-medium uppercase tracking-wider text-muted-foreground">{title}</h4>
      <ul className="mt-3 space-y-2 text-sm">
        {links.map((l) => (
          <li key={l.to}><Link to={l.to} className="text-foreground/80 transition-colors hover:text-primary">{l.label}</Link></li>
        ))}
      </ul>
    </div>
  );
}

function Logo({ label }: { label: string }) {
  return <span className="font-display text-lg font-medium tracking-tight text-foreground/70">{label}</span>;
}

function BackgroundMesh() {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-0 opacity-50"
      style={{
        backgroundImage:
          "radial-gradient(ellipse at 15% -10%, color-mix(in oklch, var(--color-primary) 35%, transparent), transparent 55%), radial-gradient(ellipse at 90% 110%, color-mix(in oklch, var(--color-info) 30%, transparent), transparent 55%)",
      }}
    />
  );
}

function Grid() {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-0 opacity-[0.07] dark:opacity-[0.05]"
      style={{
        backgroundImage:
          "linear-gradient(to right, var(--color-foreground) 1px, transparent 1px), linear-gradient(to bottom, var(--color-foreground) 1px, transparent 1px)",
        backgroundSize: "60px 60px",
        maskImage: "radial-gradient(ellipse at center, black 30%, transparent 80%)",
      }}
    />
  );
}

function TerminalDemo() {
  // Each entry is a logical line. Rendered with proper newlines so the
  // browser doesn't collapse them into one long scrollbar.
  const lines: Array<{ kind: "cmd" | "out" | "sep"; segs: Array<{ t: string; c?: string }> }> = [
    { kind: "cmd", segs: [{ t: "$ ", c: "muted" }, { t: "export ", c: "success" }, { t: "ANTHROPIC_BASE_URL=" }, { t: "https://hypi.token", c: "info" }] },
    { kind: "cmd", segs: [{ t: "$ ", c: "muted" }, { t: "export ", c: "success" }, { t: "ANTHROPIC_API_KEY=" }, { t: "sk-cpa-•••", c: "info" }] },
    { kind: "cmd", segs: [{ t: "$ ", c: "muted" }, { t: "claude " }, { t: '"refactor this Go file"', c: "warn" }] },
    { kind: "out", segs: [{ t: "─ thinking ─", c: "muted" }] },
    { kind: "out", segs: [{ t: "Here's a cleaner split…", c: "dim" }] },
    { kind: "sep", segs: [] },
    { kind: "out", segs: [{ t: "─ usage ──────────────────────────", c: "muted" }] },
    { kind: "out", segs: [{ t: "in     1,842 tok" }] },
    { kind: "out", segs: [{ t: "out      612 tok" }] },
    { kind: "out", segs: [{ t: "cost   " }, { t: "$0.0094 ", c: "success" }, { t: "(claude-sonnet-4-6)", c: "muted" }] },
    { kind: "out", segs: [{ t: "balance: $24.17", c: "muted" }] },
  ];
  const cls = (c?: string) =>
    c === "muted" ? "text-muted-foreground"
    : c === "success" ? "text-success"
    : c === "info" ? "text-info"
    : c === "warn" ? "text-warning"
    : c === "dim" ? "text-foreground/70"
    : "";
  return (
    <div className="group relative">
      <div className="absolute -inset-2 rounded-2xl bg-gradient-to-br from-primary/30 via-info/25 to-transparent blur-2xl" aria-hidden />
      <div className="relative overflow-hidden rounded-xl border border-border-strong bg-card text-left shadow-2xl">
        <div className="flex items-center gap-2 border-b border-border bg-muted/40 px-4 py-2.5">
          <span className="h-2.5 w-2.5 rounded-full bg-destructive/70" />
          <span className="h-2.5 w-2.5 rounded-full bg-warning/70" />
          <span className="h-2.5 w-2.5 rounded-full bg-success/70" />
          <span className="ml-2 font-mono text-xs text-muted-foreground">claude-cli — wallet $24.18</span>
        </div>
        <div className="overflow-x-auto px-6 py-5 font-mono text-[13px] leading-[1.7] md:text-sm">
          {lines.map((l, i) =>
            l.kind === "sep" ? (
              <div key={i} className="h-2" />
            ) : (
              <div key={i} className="whitespace-pre">
                {l.segs.map((s, j) => (
                  <span key={j} className={cls(s.c)}>{s.t}</span>
                ))}
              </div>
            )
          )}
        </div>
      </div>
    </div>
  );
}

function FormulaLine({ token, expr }: { token: string; expr: string }) {
  return (
    <div className="rounded-lg border border-border-strong bg-muted/40 px-5 py-4 font-mono text-sm">
      <span className="text-primary">{token}</span> <span className="text-muted-foreground">=</span> <span className="text-foreground/85">{expr}</span>
    </div>
  );
}

function PricingCards() {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <PriceCard label="Codex" peg="¥0.5" hint="≈ $0.0069 per $0.10 official" accent />
      <PriceCard label="Claude" peg="¥2.0" hint="≈ $0.0278 per $0.10 official" />
    </div>
  );
}

function PriceCard({ label, peg, hint, accent }: any) {
  return (
    <div className={`rounded-xl border p-6 ${accent ? "border-primary/40 bg-primary/[0.06]" : "border-border bg-card"}`}>
      <div className="flex items-center justify-between text-xs font-mono uppercase tracking-wider text-muted-foreground">
        <span>{label}</span>
        <Code2 className="h-3.5 w-3.5" />
      </div>
      <div className="mt-3 font-mono text-4xl font-semibold tabular-nums tracking-tight">{peg}</div>
      <div className="mt-1 text-xs text-muted-foreground">= $1 wallet</div>
      <div className="mt-4 border-t border-border pt-3 text-xs text-muted-foreground">{hint}</div>
    </div>
  );
}

const FEATURES = [
  { title: "Sticky sessions", body: "One token gets the same upstream credential — preserves cache hits and conversation continuity at no extra cost.", icon: Activity },
  { title: "Per-token caps", body: "Daily and monthly USD limits per API token. Concurrency and RPM controls. No noisy neighbors.", icon: ShieldCheck },
  { title: "Wallet billing", body: "Real USD wallet, top up via Alipay at the live exchange rate. Charged on every successful response.", icon: Wallet },
  { title: "Subscription pegs", body: "Codex ¥0.5 = $1, Claude ¥2 = $1 — far below the market rate. Pricing groups customise the multiplier per cohort.", icon: Sparkles },
  { title: "Live health", body: "Status-page style UI for every upstream model. Automatic credential rotation on failure, daily reset jobs.", icon: Zap },
  { title: "One bearer token", body: "Drop into Anthropic, OpenAI, or Codex SDKs unchanged. Native passthrough — we never re-shape your request.", icon: KeyRound },
];

const STEPS = [
  { title: "Sign up & top up", body: "$1 minimum via Alipay. The exchange rate is the public live rate, no spread, no fees.", icon: Wallet },
  { title: "Mint a token", body: "Generate sk-cpa-• tokens per app. Set daily & monthly caps separately on each.", icon: KeyRound },
  { title: "Point your CLI here", body: "Set ANTHROPIC_BASE_URL or OPENAI_BASE_URL. Run claude / codex normally — billing happens automatically.", icon: Terminal },
];
