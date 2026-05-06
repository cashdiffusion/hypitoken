import { Link } from "react-router-dom";
import {
  ArrowRight, Activity, ShieldCheck, Zap,
  Terminal, Check, KeyRound, BookOpen, GitBranch,
  Cpu, Network, Eye, BrainCircuit,
} from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { PublicHeader } from "@/components/layout/shell";

export default function HomePage() {
  const { t } = useTranslation();
  const archPoints = (t("home.archPoints", { returnObjects: true }) as unknown as string[]) || [];
  const archCreds = (t("home.archCreds", { returnObjects: true }) as unknown as string[]) || [];
  const features = [
    { titleKey: "home.feat.adaptiveT", bodyKey: "home.feat.adaptiveB", icon: Network },
    { titleKey: "home.feat.stickyT", bodyKey: "home.feat.stickyB", icon: Activity },
    { titleKey: "home.feat.ccT", bodyKey: "home.feat.ccB", icon: BrainCircuit },
    { titleKey: "home.feat.perTokenT", bodyKey: "home.feat.perTokenB", icon: ShieldCheck },
    { titleKey: "home.feat.healthT", bodyKey: "home.feat.healthB", icon: Eye },
    { titleKey: "home.feat.dualT", bodyKey: "home.feat.dualB", icon: GitBranch },
  ];
  const steps = [
    { titleKey: "home.step.registerT", bodyKey: "home.step.registerB" },
    { titleKey: "home.step.tokenT", bodyKey: "home.step.tokenB" },
    { titleKey: "home.step.pointT", bodyKey: "home.step.pointB" },
  ];

  return (
    <div className="min-h-dvh bg-background text-foreground">
      <PublicHeader />

      {/* Hero */}
      <section className="relative overflow-hidden border-b border-border">
        <BackgroundMesh />
        <Grid />
        <div className="relative mx-auto max-w-6xl px-4 py-20 text-center md:px-6 md:py-28">
          <span className="inline-flex items-center gap-2 rounded-full border border-primary/30 bg-primary/10 px-3 py-1 text-xs font-mono uppercase tracking-wider text-primary backdrop-blur">
            <Cpu className="h-3 w-3" /> {t("home.badge")}
          </span>
          <h1
            className="mx-auto mt-6 max-w-4xl text-balance font-display text-[2.25rem] font-semibold leading-[1.08] tracking-tight sm:text-5xl md:text-6xl lg:text-7xl"
            style={{ overflowWrap: "break-word" }}
          >
            {t("home.titleA")}
            <span
              className="bg-clip-text text-transparent"
              style={{ backgroundImage: "linear-gradient(120deg, var(--color-primary), var(--color-info))" }}
            >
              {t("home.titleB")}
            </span>
          </h1>
          <p className="mx-auto mt-6 max-w-2xl text-balance text-lg text-muted-foreground md:text-xl">
            <Trans i18nKey="home.sub" components={{ strong: <strong className="text-foreground" /> }} />
          </p>
          <div className="mt-10 flex flex-wrap items-center justify-center gap-3">
            <Button asChild size="lg" className="gap-2 text-base">
              <Link to="/register">
                {t("home.ctaPrimary")} <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
            <Button asChild variant="outline" size="lg" className="text-base">
              <Link to="/docs">
                <BookOpen className="mr-2 h-4 w-4" /> {t("home.ctaDocs")}
              </Link>
            </Button>
          </div>
          <div className="mt-8 flex flex-wrap items-center justify-center gap-x-6 gap-y-2 text-sm text-muted-foreground">
            <span className="inline-flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" /> {t("home.bullets.advisor")}</span>
            <span className="inline-flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" /> {t("home.bullets.compat")}</span>
            <span className="inline-flex items-center gap-1.5"><Check className="h-3.5 w-3.5 text-success" /> {t("home.bullets.perToken")}</span>
          </div>

          <div className="relative mx-auto mt-16 max-w-4xl">
            <TerminalDemo />
          </div>
        </div>
      </section>

      {/* Compatible-with strip */}
      <section className="border-b border-border bg-muted/20">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-center gap-x-12 gap-y-4 px-4 py-8 md:px-6">
          <span className="text-xs font-mono uppercase tracking-wider text-muted-foreground">{t("home.compatStrip")}</span>
          <Logo label="Claude Code" />
          <Logo label="Codex CLI" />
        </div>
      </section>

      {/* Features grid */}
      <section className="mx-auto max-w-7xl px-4 py-24 md:px-6">
        <div className="mb-12 max-w-2xl">
          <span className="text-xs font-mono uppercase tracking-wider text-primary">{t("home.featuresEyebrow")}</span>
          <h2 className="mt-2 font-display text-4xl font-semibold tracking-tight md:text-5xl">{t("home.featuresTitle")}</h2>
          <p className="mt-3 text-lg text-muted-foreground">{t("home.featuresSub")}</p>
        </div>
        <div className="grid gap-px overflow-hidden rounded-xl border border-border bg-border md:grid-cols-2 lg:grid-cols-3">
          {features.map((f) => (
            <div key={f.titleKey} className="group bg-background p-6 transition-colors hover:bg-card">
              <div className="grid h-10 w-10 place-items-center rounded-lg bg-primary/10 text-primary transition-transform group-hover:scale-110">
                <f.icon className="h-5 w-5" />
              </div>
              <h3 className="mt-5 font-display text-xl font-medium tracking-tight">{t(f.titleKey)}</h3>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{t(f.bodyKey)}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Architecture callout */}
      <section className="relative overflow-hidden border-y border-border bg-card/30">
        <div className="mx-auto max-w-7xl px-4 py-20 md:px-6">
          <div className="grid items-center gap-12 lg:grid-cols-2">
            <div>
              <span className="text-xs font-mono uppercase tracking-wider text-primary">{t("home.archEyebrow")}</span>
              <h2 className="mt-2 font-display text-4xl font-semibold tracking-tight md:text-5xl">{t("home.archTitle")}</h2>
              <p className="mt-4 max-w-lg text-lg text-muted-foreground">{t("home.archSub")}</p>
              <ul className="mt-8 space-y-3">
                {archPoints.map((p) => (
                  <li key={p} className="flex items-start gap-3 text-sm">
                    <Check className="mt-0.5 h-4 w-4 shrink-0 text-success" />
                    <span className="text-foreground/85">{p}</span>
                  </li>
                ))}
              </ul>
              <Button asChild variant="outline" className="mt-8">
                <Link to="/docs/self-host" className="gap-2">{t("home.archCta")} <ArrowRight className="h-4 w-4" /></Link>
              </Button>
            </div>
            <ArchDiagram archCreds={archCreds} />
          </div>
        </div>
      </section>

      {/* How it works */}
      <section className="mx-auto max-w-7xl px-4 py-24 md:px-6">
        <div className="mb-16 max-w-2xl">
          <span className="text-xs font-mono uppercase tracking-wider text-primary">{t("home.workflowEyebrow")}</span>
          <h2 className="mt-2 font-display text-4xl font-semibold tracking-tight md:text-5xl">{t("home.workflowTitle")}</h2>
        </div>
        <div className="grid gap-6 md:grid-cols-3">
          {steps.map((s, i) => (
            <div key={s.titleKey} className="relative rounded-xl border border-border bg-card p-6">
              <div className="font-mono text-5xl font-semibold leading-none text-primary/30">{(i + 1).toString().padStart(2, "0")}</div>
              <h3 className="mt-4 font-display text-xl font-medium tracking-tight">{t(s.titleKey)}</h3>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{t(s.bodyKey)}</p>
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
                <h2 className="font-display text-3xl font-semibold tracking-tight md:text-4xl">{t("home.ctaTitle")}</h2>
                <p className="mt-2 max-w-md text-muted-foreground">{t("home.ctaSub")}</p>
              </div>
              <div className="flex gap-3">
                <Button asChild size="lg" className="gap-2 text-base">
                  <Link to="/register">{t("home.ctaCreate")} <ArrowRight className="h-4 w-4" /></Link>
                </Button>
                <Button asChild size="lg" variant="outline" className="text-base">
                  <Link to="/docs">{t("home.ctaReadDocs")}</Link>
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
            <p className="mt-3 max-w-xs text-sm text-muted-foreground">{t("home.footerBlurb")}</p>
          </div>
          <FooterCol title={t("home.footerProduct")} links={[
            { to: "/status", label: t("nav.status") },
            { to: "/docs", label: t("nav.documentation") },
          ]} />
          <FooterCol title={t("home.footerAccount")} links={[
            { to: "/register", label: t("home.footerSignUp") },
            { to: "/login", label: t("nav.signIn") },
          ]} />
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
  // The terminal demo is a code snippet — code stays in English. Only the
  // status-bar caption is translatable.
  const { t } = useTranslation();
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
          <span className="ml-2 font-mono text-xs text-muted-foreground">claude-code — {t("home.archGatewayLabel")}</span>
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

function ArchDiagram({ archCreds }: { archCreds: string[] }) {
  const { t } = useTranslation();
  const nodes = [
    { label: t("home.archNodes.ccLabel"), sub: t("home.archNodes.ccSub"), color: "text-primary" },
    { label: t("home.archNodes.codexLabel"), sub: t("home.archNodes.codexSub"), color: "text-info" },
    { label: t("home.archNodes.sdkLabel"), sub: t("home.archNodes.sdkSub"), color: "text-muted-foreground" },
  ];
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
        <span className="text-xs uppercase tracking-wider">{t("home.archGatewayLabel")}</span>
        <div className="h-px flex-1 border-t border-dashed border-border-strong" />
      </div>
      <div className="space-y-2">
        {archCreds.map((c, i) => (
          <div key={c} className="flex items-center gap-3 rounded-lg border border-border bg-background px-4 py-2.5">
            <span className="text-xs font-mono text-success">●</span>
            <span className="text-foreground/80">{c}</span>
            {i === 0 && <span className="ml-auto text-xs text-muted-foreground">{t("home.archStickyTag")}</span>}
          </div>
        ))}
      </div>
    </div>
  );
}
