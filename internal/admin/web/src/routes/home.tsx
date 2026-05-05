import { Link } from "react-router-dom";
import {
  ArrowRight, Activity, ShieldCheck, Zap,
  Terminal, Check, KeyRound, BookOpen, GitBranch,
  Cpu, Network, Eye, BrainCircuit,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { PublicHeader } from "@/components/layout/shell";

export default function HomePage() {
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <PublicHeader />

      {/* Hero */}
      <section className="relative overflow-hidden border-b border-border">
        <BackgroundMesh />
        <Grid />
        <div className="relative mx-auto max-w-6xl px-4 py-20 text-center md:px-6 md:py-28">
          <span className="inline-flex items-center gap-2 rounded-full border border-primary/30 bg-primary/10 px-3 py-1 text-xs font-mono uppercase tracking-wider text-primary backdrop-blur">
            <Cpu className="h-3 w-3" /> Self-developed LLM gateway middleware
          </span>
          <h1
            className="mx-auto mt-6 max-w-4xl text-balance font-display text-[2.25rem] font-semibold leading-[1.08] tracking-tight sm:text-5xl md:text-6xl lg:text-7xl"
            style={{ overflowWrap: "break-word" }}
          >
            Intelligent routing for{" "}
            <span
              className="bg-clip-text text-transparent"
              style={{ backgroundImage: "linear-gradient(120deg, var(--color-primary), var(--color-info))" }}
            >
              Claude & Codex.
            </span>
          </h1>
          <p className="mx-auto mt-6 max-w-2xl text-balance text-lg text-muted-foreground md:text-xl">
            A purpose-built reverse proxy with credential pool management, sticky sessions,
            and full Claude Code compatibility — including{" "}
            <strong className="text-foreground">advisor, extended thinking, and sub-agents</strong>.
          </p>
          <div className="mt-10 flex flex-wrap items-center justify-center gap-3">
            <Button asChild size="lg" className="gap-2 text-base">
              <Link to="/register">
                Get started <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
            <Button asChild variant="outline" size="lg" className="text-base">
              <Link to="/docs">
                <BookOpen className="mr-2 h-4 w-4" /> Read the docs
              </Link>
            </Button>
          </div>
          <div className="mt-8 flex flex-wrap items-center justify-center gap-x-6 gap-y-2 text-sm text-muted-foreground">
            <span className="inline-flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" /> Advisor & sub-agent support</span>
            <span className="inline-flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" /> Drop-in SDK compatible</span>
            <span className="inline-flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" /> Per-token access control</span>
          </div>

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
          <h2 className="mt-2 font-display text-4xl font-semibold tracking-tight md:text-5xl">Built on a self-developed routing core.</h2>
          <p className="mt-3 text-lg text-muted-foreground">
            One gateway in front of a credential pool. Adaptive routing, health monitoring,
            and session continuity — engineered to be invisible to your clients.
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

      {/* Architecture callout */}
      <section className="relative overflow-hidden border-y border-border bg-card/30">
        <div className="mx-auto max-w-7xl px-4 py-20 md:px-6">
          <div className="grid items-center gap-12 lg:grid-cols-2">
            <div>
              <span className="text-xs font-mono uppercase tracking-wider text-primary">Architecture</span>
              <h2 className="mt-2 font-display text-4xl font-semibold tracking-tight md:text-5xl">Routing that mirrors real CC behaviour.</h2>
              <p className="mt-4 max-w-lg text-lg text-muted-foreground">
                The gateway maintains a full Claude Code session identity per account — same
                device fingerprint, consistent session IDs across turns, and all the
                auxiliary traffic that keeps Claude subscriptions healthy.
              </p>
              <ul className="mt-8 space-y-3">
                {ARCH_POINTS.map((p) => (
                  <li key={p} className="flex items-start gap-3 text-sm">
                    <Check className="mt-0.5 h-4 w-4 shrink-0 text-success" />
                    <span className="text-foreground/85">{p}</span>
                  </li>
                ))}
              </ul>
              <Button asChild variant="outline" className="mt-8">
                <Link to="/docs/self-host" className="gap-2">See architecture docs <ArrowRight className="h-4 w-4" /></Link>
              </Button>
            </div>
            <ArchDiagram />
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
                <h2 className="font-display text-3xl font-semibold tracking-tight md:text-4xl">Start routing today.</h2>
                <p className="mt-2 max-w-md text-muted-foreground">Create an account, issue a token, and your entire Claude Code workflow is proxied through the gateway.</p>
              </div>
              <div className="flex gap-3">
                <Button asChild size="lg" className="gap-2 text-base">
                  <Link to="/register">Create account <ArrowRight className="h-4 w-4" /></Link>
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
            <p className="mt-3 max-w-xs text-sm text-muted-foreground">Self-developed LLM gateway middleware for Claude and Codex. Intelligent routing, credential pool management, full Claude Code compatibility.</p>
          </div>
          <FooterCol title="Product" links={[{ to: "/status", label: "Status" }, { to: "/docs", label: "Documentation" }]} />
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
  const lines: Array<{ kind: "cmd" | "out" | "sep"; segs: Array<{ t: string; c?: string }> }> = [
    { kind: "cmd", segs: [{ t: "$ ", c: "muted" }, { t: "export ", c: "success" }, { t: "ANTHROPIC_BASE_URL=" }, { t: "https://api.novadiffusion.com", c: "info" }] },
    { kind: "cmd", segs: [{ t: "$ ", c: "muted" }, { t: "export ", c: "success" }, { t: "ANTHROPIC_AUTH_TOKEN=" }, { t: "sk-cpa-•••", c: "info" }] },
    { kind: "cmd", segs: [{ t: "$ ", c: "muted" }, { t: "claude " }, { t: '"review this PR"', c: "warn" }] },
    { kind: "out", segs: [{ t: "─ advisor ─────────────────────────", c: "muted" }] },
    { kind: "out", segs: [{ t: "Routing to  ", c: "muted" }, { t: "claude-sonnet-4-6", c: "info" }, { t: "  [pool: 3 active]", c: "muted" }] },
    { kind: "out", segs: [{ t: "Session     ", c: "muted" }, { t: "sticky → credential #2", c: "dim" }] },
    { kind: "sep", segs: [] },
    { kind: "out", segs: [{ t: "─ response ─────────────────────────", c: "muted" }] },
    { kind: "out", segs: [{ t: "Here's my analysis of the changes…", c: "dim" }] },
    { kind: "sep", segs: [] },
    { kind: "out", segs: [{ t: "in     2,841 tok · out    914 tok", c: "muted" }] },
    { kind: "out", segs: [{ t: "duration  ", c: "muted" }, { t: "1.24s", c: "success" }, { t: "  (claude-sonnet-4-6)", c: "muted" }] },
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
          <span className="ml-2 font-mono text-xs text-muted-foreground">claude-code — gateway routing</span>
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

function ArchDiagram() {
  const nodes = [
    { label: "Claude Code", sub: "advisor / thinking / sub-agents", color: "text-primary" },
    { label: "Codex CLI", sub: "openai-compatible", color: "text-info" },
    { label: "Any SDK", sub: "anthropic / openai / litellm", color: "text-muted-foreground" },
  ];
  const creds = ["OAuth credential 1", "OAuth credential 2", "API key fallback"];
  return (
    <div className="rounded-xl border border-border bg-card p-6 font-mono text-sm">
      <div className="space-y-2">
        {nodes.map((n) => (
          <div key={n.label} className="flex items-center gap-3 rounded-lg border border-border bg-muted/30 px-4 py-2.5">
            <span className={`font-medium ${n.color}`}>{n.label}</span>
            <span className="text-xs text-muted-foreground">{n.sub}</span>
          </div>
        ))}
      </div>
      <div className="my-4 flex items-center justify-center gap-2 text-muted-foreground">
        <div className="h-px flex-1 border-t border-dashed border-border-strong" />
        <span className="text-xs uppercase tracking-wider">gateway routing</span>
        <div className="h-px flex-1 border-t border-dashed border-border-strong" />
      </div>
      <div className="space-y-2">
        {creds.map((c, i) => (
          <div key={c} className="flex items-center gap-3 rounded-lg border border-border bg-background px-4 py-2.5">
            <span className="text-xs font-mono text-success">●</span>
            <span className="text-foreground/80">{c}</span>
            {i === 0 && <span className="ml-auto text-xs text-muted-foreground">sticky</span>}
          </div>
        ))}
      </div>
    </div>
  );
}

const ARCH_POINTS = [
  "Per-account device fingerprint anchored to the OAuth credential, not the client token",
  "Session IDs derived from (account, client, first message) — consistent across turns",
  "Auxiliary bootstrap traffic emitted on first touch per account to maintain subscription health",
  "Hard-failure sticky, daily reset job, automatic stealth-ban detection",
];

const FEATURES = [
  {
    title: "Adaptive routing",
    body: "Credential pool with fewest-active-sessions scheduling. Automatic failover, cooldown management, and daily reset jobs keep the pool healthy.",
    icon: Network,
  },
  {
    title: "Sticky sessions",
    body: "Each client token gets a consistent upstream credential within its active window — preserving cache hits and conversation continuity.",
    icon: Activity,
  },
  {
    title: "Claude Code native",
    body: "Full CC fingerprint: advisor mode, extended thinking, sub-agents, MCP tools — every feature works exactly as the official CLI intended.",
    icon: BrainCircuit,
  },
  {
    title: "Per-token access control",
    body: "Daily and monthly spend caps, concurrency limits, and RPM controls on each issued token. No noisy-neighbour effects.",
    icon: ShieldCheck,
  },
  {
    title: "Live health dashboard",
    body: "Status-page style UI for every upstream credential and model. Automatic rotation on hard failures; per-credential usage gauges.",
    icon: Eye,
  },
  {
    title: "Dual provider",
    body: "Claude (Anthropic) and Codex (OpenAI) behind one gateway domain. Route by path — same bearer token, same host.",
    icon: GitBranch,
  },
];

const STEPS = [
  {
    title: "Create an account",
    body: "Register, then add your Anthropic or OpenAI credentials in the admin panel. The pool manager takes it from there.",
    icon: KeyRound,
  },
  {
    title: "Issue a bearer token",
    body: "Mint sk-cpa-• tokens per app or team. Set per-token caps: daily spend, concurrency, RPM.",
    icon: Zap,
  },
  {
    title: "Point your CLI",
    body: "Set ANTHROPIC_BASE_URL or OPENAI_BASE_URL. Run claude or codex normally — advisor, thinking, sub-agents all work.",
    icon: Terminal,
  },
];
