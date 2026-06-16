import {
  Activity,
  ArrowRight,
  BookOpen,
  BrainCircuit,
  Check,
  ChevronDown,
  Eye,
  GitBranch,
  KeyRound,
  Network,
  ShieldCheck,
} from "lucide-react";
import {
  animate,
  motion,
  useInView,
  useMotionValueEvent,
  useReducedMotion,
  useScroll,
} from "motion/react";
import { lazy, type ReactNode, Suspense, useEffect, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { DiscordIcon } from "@/components/icons/discord";
import { Community } from "@/components/landing/community";
import { FeatureShowcase } from "@/components/landing/feature-showcase";
import { HlsVideo } from "@/components/landing/hls-video";
import { Magnetic, Marquee, SpotlightCard } from "@/components/landing/interactions";
import { Reveal, RevealItem, RevealStagger } from "@/components/landing/reveal";
import { RoutingDiagram } from "@/components/landing/routing-diagram";
import { StatusBoard } from "@/components/landing/status-board";
import { useIsMobile, usePrefersReducedMotion } from "@/components/landing/use-media";
import { LanguageToggle } from "@/components/language-toggle";
import { MobileMenu } from "@/components/layout/mobile-menu";
import { SiteNav } from "@/components/layout/shell";
import { ThemeToggle } from "@/components/theme-toggle";
import RadialOrbitalTimeline, { type TimelineItem } from "@/components/ui/radial-orbital-timeline";
import { type Testimonial, TestimonialsColumn } from "@/components/ui/testimonials-columns";
import { useAuth } from "@/hooks/use-auth";
import { DISCORD_INVITE } from "@/lib/social";

const ParticleField = lazy(() => import("@/components/landing/particle-field"));
const FloatingGeometry = lazy(() => import("@/components/landing/floating-geometry"));

// Hosted on the user's Bitiful bucket (salemind) behind the video.wjsphy.top
// CDN — China-friendly + faststart mp4 (the Mux HLS hero was transcoded to mp4).
const HERO_VIDEO = "https://video.wjsphy.top/hero.mp4";
const FOOTER_VIDEO = "https://video.wjsphy.top/footer.mp4";

const EASE = [0.22, 1, 0.36, 1] as const;

export default function HomePage() {
  const { t } = useTranslation();
  const archPoints = (t("home.archPoints", { returnObjects: true }) as unknown as string[]) || [];

  const steps = [
    { titleKey: "home.step.registerT", bodyKey: "home.step.registerB" },
    { titleKey: "home.step.tokenT", bodyKey: "home.step.tokenB" },
    { titleKey: "home.step.pointT", bodyKey: "home.step.pointB" },
  ];

  return (
    <div className="relative text-foreground">
      <FloatingNav />
      {/* page-canvas slides up over the pinned footer for the curtain reveal */}
      <div className="page-canvas relative z-10 mb-[460px] sm:mb-[600px]">
        <Hero t={t} />

        {/* Savings — pricing comparison (hierarchy #2: how much do I save?) */}
        <Savings />

        {/* Compatible-with strip — infinite marquee (hierarchy #3: compatibility) */}
        <section className="border-b border-border bg-muted/20 py-8">
          <div className="mx-auto max-w-7xl px-4 md:px-6">
            <p className="mb-5 text-center text-xs font-mono uppercase tracking-wider text-muted-foreground">
              {t("home.compatStrip")}
            </p>
            <Marquee durationSec={28}>
              {[
                "Claude Code",
                "Codex CLI",
                "Anthropic SDK",
                "OpenAI SDK",
                "LiteLLM",
                "Claude Agent SDK",
              ].map((l) => (
                <span key={l} className="flex items-center gap-3 whitespace-nowrap">
                  <span className="h-1.5 w-1.5 rounded-full bg-primary/50" />
                  <Logo label={l} />
                </span>
              ))}
            </Marquee>
          </div>
        </section>

        {/* How it works — answers "how fast can I start" */}
        <section className="mx-auto max-w-7xl px-4 py-24 md:px-6">
          <Reveal className="mb-16 max-w-2xl">
            <SectionEyebrow>{t("home.workflowEyebrow")}</SectionEyebrow>
            <h2 className="mt-3 font-display text-4xl font-semibold tracking-tight md:text-5xl">
              {t("home.workflowTitle")}
            </h2>
          </Reveal>
          <RevealStagger className="grid gap-4 md:grid-cols-3">
            {steps.map((s, i) => (
              <RevealItem key={s.titleKey}>
                <SpotlightCard className="h-full">
                  <div className="font-mono text-5xl font-semibold leading-none text-primary/30">
                    {(i + 1).toString().padStart(2, "0")}
                  </div>
                  <h3 className="mt-4 font-display text-xl font-medium tracking-tight">
                    {t(s.titleKey)}
                  </h3>
                  <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                    {t(s.bodyKey)}
                  </p>
                </SpotlightCard>
              </RevealItem>
            ))}
          </RevealStagger>
        </section>

        {/* Testimonials — vertical glass marquee (hierarchy #4: trust) */}
        <Testimonials />

        {/* ── Technical detail, below the fold (hierarchy #5: features) ── */}

        {/* Features — Bento grid */}
        <section className="mx-auto max-w-7xl px-4 py-24 md:px-6">
          <Reveal className="mb-12 max-w-2xl">
            <SectionEyebrow>{t("home.featuresEyebrow")}</SectionEyebrow>
            <h2 className="mt-3 font-display text-4xl font-semibold tracking-tight md:text-5xl">
              <Trans
                i18nKey="home.featuresTitle"
                components={{ hl: <span className="text-primary" /> }}
              />
            </h2>
            <p className="mt-3 text-lg text-muted-foreground">{t("home.featuresSub")}</p>
          </Reveal>

          <Reveal>
            <FeatureShowcase
              cards={[
                {
                  icon: Network,
                  label: t("home.feat.adaptiveT"),
                  title: t("home.feat.adaptiveB"),
                  visual: <TerminalDemo />,
                },
                {
                  icon: Eye,
                  label: t("home.feat.healthT"),
                  title: t("home.feat.healthB"),
                  visual: <StatusBoard />,
                },
              ]}
              wideTitle={t("home.featuresWideTitle")}
              wideLabels={
                t("home.featuresWideLabels", { returnObjects: true }) as unknown as string[]
              }
            />
          </Reveal>
        </section>

        {/* Architecture callout */}
        <section className="relative overflow-hidden border-y border-border bg-card/30">
          <BackgroundMesh />
          <div className="relative mx-auto max-w-7xl px-4 py-24 md:px-6">
            <div className="grid items-center gap-12 lg:grid-cols-2">
              <Reveal>
                <SectionEyebrow>{t("home.archEyebrow")}</SectionEyebrow>
                <h2 className="mt-3 font-display text-4xl font-semibold tracking-tight md:text-5xl">
                  {t("home.archTitle")}
                </h2>
                <p className="mt-4 max-w-lg text-lg text-muted-foreground">{t("home.archSub")}</p>
                <ul className="mt-8 space-y-3">
                  {archPoints.map((p) => (
                    <li key={p} className="flex items-start gap-3 text-sm">
                      <Check className="mt-0.5 h-4 w-4 shrink-0 text-success" />
                      <span className="text-foreground/85">{p}</span>
                    </li>
                  ))}
                </ul>
                <GhostLink to="/docs/self-host" className="mt-8 inline-flex">
                  {t("home.archCta")} <ArrowRight className="h-4 w-4" />
                </GhostLink>
              </Reveal>
              <Reveal delay={0.1}>
                <div className="glass relative overflow-hidden rounded-2xl p-6">
                  <div className="mb-2 flex items-center justify-between">
                    <span className="eyebrow text-muted-foreground">
                      {t("home.archGatewayLabel")}
                    </span>
                    <span className="flex items-center gap-1.5 text-xs text-success">
                      <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-success" /> live
                    </span>
                  </div>
                  <RoutingDiagram />
                </div>
              </Reveal>
            </div>
          </div>
        </section>

        {/* Capability constellation — interactive radial orbital timeline */}
        <FeatureOrbit t={t} />

        {/* Community — social platforms (Discord) */}
        <Community />

        {/* CTA */}
        <section className="mx-auto max-w-7xl px-4 pb-24 md:px-6">
          <Reveal>
            <div className="glass relative overflow-hidden rounded-3xl p-10 md:p-14">
              <div
                aria-hidden
                className="pointer-events-none absolute inset-0 opacity-70"
                style={{
                  backgroundImage:
                    "radial-gradient(ellipse 50% 80% at 100% 0%, color-mix(in oklch, var(--primary) 22%, transparent), transparent 60%), radial-gradient(ellipse 60% 80% at 0% 100%, color-mix(in oklch, var(--info) 16%, transparent), transparent 60%)",
                }}
              />
              {/* ambient floating geometry, right side, behind content */}
              <div
                aria-hidden
                className="pointer-events-none absolute -right-10 top-1/2 hidden h-[360px] w-[360px] -translate-y-1/2 opacity-80 md:block"
              >
                <Suspense fallback={null}>
                  <FloatingGeometry color="#34d399" />
                </Suspense>
              </div>
              <div className="relative flex flex-col items-start justify-between gap-8 md:flex-row md:items-center">
                <div>
                  <h2 className="font-display text-3xl font-semibold tracking-tight md:text-4xl">
                    {t("home.ctaTitle")}
                  </h2>
                  <p className="mt-2 max-w-md text-muted-foreground">{t("home.ctaSub")}</p>
                </div>
                <div className="flex flex-wrap gap-3">
                  <Magnetic>
                    <PrimaryLink to="/register">
                      {t("home.ctaCreate")} <ArrowRight className="h-4 w-4" />
                    </PrimaryLink>
                  </Magnetic>
                  <GhostLink to="/docs">{t("home.ctaReadDocs")}</GhostLink>
                </div>
              </div>
            </div>
          </Reveal>
        </section>
      </div>
      <SiteFooter t={t} />
    </div>
  );
}

/* ── Floating glass nav ────────────────────────────────────────────────── */

// Scroll-aware condensed header. Stays hidden over the hero, then slides down
// whenever the user scrolls up anywhere below the fold, and tucks away again on
// scroll-down. The pill itself is the shared <SiteNav/> — identical to every
// other page's header — so the homepage and the rest of the site read as one
// consistent navbar.
function FloatingNav() {
  const reduce = useReducedMotion();
  const { scrollY } = useScroll();
  const [shown, setShown] = useState(false);
  const lastY = useRef(0);

  useMotionValueEvent(scrollY, "change", (y) => {
    const past = y > window.innerHeight * 0.85; // below the hero fold
    const goingUp = y < lastY.current - 2;
    const goingDown = y > lastY.current + 2;
    if (!past) setShown(false);
    else if (goingUp) setShown(true);
    else if (goingDown) setShown(false);
    lastY.current = y;
  });

  return (
    <motion.div
      className="fixed inset-x-0 top-0 z-50 px-4 pt-3 md:px-6"
      style={{ pointerEvents: shown ? "auto" : "none" }}
      initial={{ y: -120, opacity: 0 }}
      animate={shown ? { y: 0, opacity: 1 } : { y: -120, opacity: 0 }}
      transition={reduce ? { duration: 0 } : { duration: 0.38, ease: EASE }}
    >
      <SiteNav />
    </motion.div>
  );
}

/* ── Hero ──────────────────────────────────────────────────────────────── */

function Hero({ t }: { t: (k: string) => string }) {
  const reduce = useReducedMotion();
  const prefersReduced = usePrefersReducedMotion();
  const isMobile = useIsMobile();
  const showParticles = !prefersReduced && !isMobile;

  const heroAnim = (delay: number) =>
    reduce
      ? {}
      : {
          initial: { opacity: 0, y: 22, filter: "blur(10px)" },
          animate: { opacity: 1, y: 0, filter: "blur(0px)" },
          transition: { duration: 0.7, delay, ease: EASE },
        };

  return (
    // `dark` scope: every themed child (toggles, buttons) renders in the dark
    // palette — phosphor-green primary — regardless of the global theme, so the
    // cinematic band stays legible in both light and dark mode.
    <section className="dark relative isolate flex min-h-[100svh] flex-col overflow-hidden bg-[#04110c] text-white">
      <HlsVideo
        src={HERO_VIDEO}
        className="z-0"
        style={{ opacity: 0.46 }}
        fallbackColor="#04110c"
      />

      {/* Scrims: darken top (nav) + bottom (text) and seat the band into the page. */}
      <div
        aria-hidden
        className="absolute inset-0 z-[1]"
        style={{
          background:
            "linear-gradient(to bottom, rgba(4,17,12,0.78) 0%, rgba(4,17,12,0.30) 30%, rgba(4,17,12,0.55) 70%, #04110c 100%)",
        }}
      />
      <div
        aria-hidden
        className="absolute inset-0 z-[1]"
        style={{
          background:
            "radial-gradient(ellipse 70% 55% at 50% 42%, color-mix(in oklch, var(--primary) 18%, transparent), transparent 70%)",
        }}
      />

      {showParticles && (
        <Suspense fallback={null}>
          <div className="absolute inset-0 z-[2] opacity-[0.35]">
            <ParticleField color="#34d399" count={1500} />
          </div>
        </Suspense>
      )}

      <div className="noise pointer-events-none absolute inset-0 z-[3] opacity-40" aria-hidden />

      <HeroNav t={t} />

      <div className="relative z-10 flex flex-1 flex-col items-center justify-center px-4 pb-24 pt-28 text-center">
        <motion.span
          {...heroAnim(0.1)}
          className="glass-dark inline-flex items-center gap-2 rounded-full px-3.5 py-1.5 text-xs font-mono uppercase tracking-wider text-white/85"
        >
          <span className="relative flex h-1.5 w-1.5">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
            <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-emerald-400" />
          </span>
          {t("home.badge")}
        </motion.span>

        <motion.h1
          {...heroAnim(0.2)}
          className="mx-auto mt-6 max-w-4xl text-balance font-display text-[2.5rem] font-semibold leading-[1.05] tracking-tight sm:text-5xl md:text-6xl lg:text-7xl"
          style={{ overflowWrap: "break-word" }}
        >
          {t("home.titleA")}
          <span
            className="bg-clip-text text-transparent"
            style={{ backgroundImage: "linear-gradient(120deg, #6ee7b7, #34d399 55%, #22d3ee)" }}
          >
            {t("home.titleB")}
          </span>
        </motion.h1>

        <motion.p
          {...heroAnim(0.34)}
          className="mx-auto mt-6 max-w-2xl text-balance text-lg text-white/75 md:text-xl"
        >
          <Trans
            i18nKey="home.sub"
            components={{ strong: <strong className="font-medium text-white" /> }}
          />
        </motion.p>

        <motion.div
          {...heroAnim(0.48)}
          className="mt-10 flex flex-wrap items-center justify-center gap-3"
        >
          <Magnetic strength={0.4}>
            <HeroPrimary to="/register" track="start">
              {t("home.ctaPrimary")} <ArrowRight className="h-4 w-4" />
            </HeroPrimary>
          </Magnetic>
          <HeroGhost to="/docs">
            <BookOpen className="h-4 w-4" /> {t("home.ctaDocs")}
          </HeroGhost>
        </motion.div>

        <motion.div
          {...heroAnim(0.62)}
          className="mt-9 flex flex-wrap items-center justify-center gap-2.5"
        >
          {[
            "home.bullets.claudePrice",
            "home.bullets.openaiPrice",
            "home.bullets.noLimits",
            "home.bullets.ccCompat",
            "home.bullets.fastStart",
          ].map((k) => (
            <span
              key={k}
              className="glass-dark inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs text-white/80"
            >
              <Check className="h-3.5 w-3.5 text-emerald-400" /> {t(k)}
            </span>
          ))}
        </motion.div>
      </div>

      {!reduce && (
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          transition={{ delay: 1.1, duration: 0.8 }}
          className="absolute inset-x-0 bottom-6 z-10 flex justify-center"
        >
          <ChevronDown className="h-5 w-5 animate-float text-white/50" />
        </motion.div>
      )}
    </section>
  );
}

function HeroNav({ t }: { t: (k: string) => string }) {
  const { user } = useAuth();
  const links = [
    { to: "/", key: "nav.home" },
    { to: "/pricing", key: "nav.pricing" },
    { to: "/docs", key: "nav.docs" },
    { to: "/status", key: "nav.status" },
  ];
  return (
    <div className="relative z-20 px-4 pt-4 md:px-6">
      <nav className="glass-dark mx-auto flex max-w-6xl items-center justify-between gap-4 rounded-full px-3 py-2 md:px-4">
        <Link
          to="/"
          viewTransition
          className="flex items-center gap-2 pl-1 font-display text-lg font-semibold tracking-tight text-white"
        >
          <span className="grid h-7 w-7 place-items-center rounded-md bg-emerald-400 text-emerald-950">
            <KeyRound className="h-3.5 w-3.5" />
          </span>
          HypiToken
        </Link>
        <div className="hidden items-center gap-1 lg:flex">
          {links.map((l) => (
            <Link
              key={l.to}
              to={l.to}
              viewTransition
              className="rounded-full px-3 py-1.5 text-sm text-white/80 transition-colors hover:bg-white/10 hover:text-white"
            >
              {t(l.key)}
            </Link>
          ))}
        </div>
        <div className="flex items-center gap-2">
          <div className="hidden items-center gap-1.5 lg:flex">
            <LanguageToggle />
            <ThemeToggle />
            {user ? (
              <HeroPrimary to="/app" compact>
                {t("nav.dashboard")} →
              </HeroPrimary>
            ) : (
              <>
                <HeroGhost to="/login" compact>
                  {t("nav.signIn")}
                </HeroGhost>
                <HeroPrimary to="/register" compact>
                  {t("nav.signUp")}
                </HeroPrimary>
              </>
            )}
          </div>
          <MobileMenu variant="public" />
        </div>
      </nav>
    </div>
  );
}

function HeroPrimary({
  to,
  children,
  compact,
  track,
}: {
  to: string;
  children: ReactNode;
  compact?: boolean;
  // Optional analytics label — surfaces this click as an explicit first action
  // (e.g. "start") rather than a generic nav:* in the visitor-behaviour stats.
  track?: string;
}) {
  return (
    <Link
      to={to}
      viewTransition
      data-track={track}
      className={`group inline-flex items-center justify-center gap-2 rounded-full bg-emerald-400 font-medium text-emerald-950 shadow-[0_8px_30px_-8px_rgba(52,211,153,0.6)] transition-all hover:bg-emerald-300 hover:shadow-[0_10px_40px_-8px_rgba(52,211,153,0.8)] ${
        compact ? "px-4 py-1.5 text-sm" : "px-5 py-2.5 text-base"
      }`}
    >
      {children}
    </Link>
  );
}

function HeroGhost({
  to,
  children,
  compact,
}: {
  to: string;
  children: ReactNode;
  compact?: boolean;
}) {
  return (
    <Link
      to={to}
      viewTransition
      className={`glass-dark inline-flex items-center justify-center gap-2 rounded-full font-medium text-white transition-colors hover:bg-white/15 ${
        compact ? "px-4 py-1.5 text-sm" : "px-5 py-2.5 text-base"
      }`}
    >
      {children}
    </Link>
  );
}

/* ── Shared blocks ─────────────────────────────────────────────────────── */

// Section eyebrow with an L-corner bracket — a small structural mark borrowed
// from the reference site, tinted to the brand primary. Pairs with the mono
// `.eyebrow` label for a consistent "engineered" section header across the page.
function SectionEyebrow({ children }: { children: ReactNode }) {
  return (
    <span className="relative inline-flex items-center gap-2.5">
      <span aria-hidden className="block h-3 w-3 shrink-0 border-l border-t border-primary/50" />
      <span className="eyebrow text-primary">{children}</span>
    </span>
  );
}

function PrimaryLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link
      to={to}
      viewTransition
      className="inline-flex items-center justify-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-medium text-primary-foreground shadow-sm transition-all hover:opacity-90 hover:shadow-md"
    >
      {children}
    </Link>
  );
}

function GhostLink({
  to,
  children,
  className = "",
}: {
  to: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Link
      to={to}
      viewTransition
      className={`inline-flex items-center justify-center gap-2 rounded-full border border-border-strong bg-card px-5 py-2.5 text-sm font-medium text-foreground transition-colors hover:bg-accent ${className}`}
    >
      {children}
    </Link>
  );
}

function Logo({ label }: { label: string }) {
  return (
    <span className="font-display text-lg font-medium tracking-tight text-foreground/70">
      {label}
    </span>
  );
}

function BackgroundMesh() {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-0 opacity-50"
      style={{
        backgroundImage:
          "radial-gradient(ellipse at 15% -10%, color-mix(in oklch, var(--color-primary) 30%, transparent), transparent 55%), radial-gradient(ellipse at 90% 110%, color-mix(in oklch, var(--color-info) 25%, transparent), transparent 55%)",
      }}
    />
  );
}

/* ── Capability constellation (radial orbital timeline) ────────────────── */

function FeatureOrbit({ t }: { t: (k: string) => string }) {
  const reduce = useReducedMotion();
  const tier = (status: TimelineItem["status"]) =>
    status === "in-progress" ? t("home.orbit.tierBeta") : t("home.orbit.tierCore");

  const nodes: Array<Omit<TimelineItem, "date">> = [
    {
      id: 1,
      title: t("home.feat.adaptiveT"),
      content: t("home.feat.adaptiveB"),
      category: t("home.orbit.cat.routing"),
      icon: Network,
      relatedIds: [2, 5],
      status: "completed",
      energy: 96,
    },
    {
      id: 2,
      title: t("home.feat.stickyT"),
      content: t("home.feat.stickyB"),
      category: t("home.orbit.cat.sessions"),
      icon: Activity,
      relatedIds: [1, 3],
      status: "completed",
      energy: 92,
    },
    {
      id: 3,
      title: t("home.feat.ccT"),
      content: t("home.feat.ccB"),
      category: t("home.orbit.cat.cc"),
      icon: BrainCircuit,
      relatedIds: [2, 6],
      status: "completed",
      energy: 98,
    },
    {
      id: 4,
      title: t("home.feat.perTokenT"),
      content: t("home.feat.perTokenB"),
      category: t("home.orbit.cat.access"),
      icon: ShieldCheck,
      relatedIds: [5, 1],
      status: "completed",
      energy: 88,
    },
    {
      id: 5,
      title: t("home.feat.healthT"),
      content: t("home.feat.healthB"),
      category: t("home.orbit.cat.health"),
      icon: Eye,
      relatedIds: [1, 4],
      status: "completed",
      energy: 90,
    },
    {
      id: 6,
      title: t("home.feat.dualT"),
      content: t("home.feat.dualB"),
      category: t("home.orbit.cat.provider"),
      icon: GitBranch,
      relatedIds: [3, 1],
      status: "in-progress",
      energy: 84,
    },
  ];
  const data: TimelineItem[] = nodes.map((n) => ({ ...n, date: tier(n.status) }));

  return (
    <section className="relative overflow-hidden border-y border-border bg-card/30">
      <BackgroundMesh />
      <div className="relative mx-auto max-w-7xl px-4 py-24 md:px-6">
        <Reveal className="mx-auto max-w-2xl text-center">
          <SectionEyebrow>{t("home.orbit.eyebrow")}</SectionEyebrow>
          <h2 className="mt-3 font-display text-4xl font-semibold tracking-tight md:text-5xl">
            <Trans
              i18nKey="home.orbit.title"
              components={{ hl: <span className="text-primary" /> }}
            />
          </h2>
          <p className="mt-3 text-lg text-muted-foreground">{t("home.orbit.sub")}</p>
        </Reveal>
        <div className="relative mt-8 h-[560px] sm:h-[640px]">
          <RadialOrbitalTimeline
            className="h-full"
            timelineData={data}
            autoRotate={!reduce}
            labels={{ metric: t("home.orbit.metric"), related: t("home.orbit.related") }}
          />
        </div>
        <p className="mt-2 text-center text-xs font-mono uppercase tracking-wider text-muted-foreground">
          {t("home.orbit.hint")}
        </p>
      </div>
    </section>
  );
}

/* ── Savings (pricing comparison) ──────────────────────────────────────── */

// CountUp — eases a number from 0 to `to` the first time it scrolls into view.
// Honours reduced motion (renders the final value immediately).
function CountUp({ to }: { to: number }) {
  const reduce = useReducedMotion();
  const ref = useRef<HTMLSpanElement>(null);
  const inView = useInView(ref, { once: true, margin: "-15%" });
  const [n, setN] = useState(reduce ? to : 0);

  useEffect(() => {
    if (reduce || !inView) return;
    const controls = animate(0, to, {
      duration: 1.2,
      ease: EASE,
      onUpdate: (v) => setN(Math.round(v)),
    });
    return () => controls.stop();
  }, [inView, to, reduce]);

  return <span ref={ref}>{n}</span>;
}

// SavingsGauge — a radial dial. The arc sweeps to `savePct` (the headline win,
// nearly-full for OpenAI) while the centre counts up to `payPct` (what you
// actually pay). The arc draws itself on scroll-in via strokeDashoffset.
function SavingsGauge({
  payPct,
  savePct,
  gradId,
}: {
  payPct: number;
  savePct: number;
  gradId: string;
}) {
  const reduce = useReducedMotion();
  const R = 58;
  const C = 2 * Math.PI * R;
  const offset = C * (1 - savePct / 100);

  return (
    <div className="relative mx-auto aspect-square w-40">
      <svg viewBox="0 0 140 140" className="h-full w-full -rotate-90" role="img">
        <title>{`${savePct}% saved`}</title>
        <defs>
          <linearGradient id={gradId} x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" stopColor="#6ee7b7" />
            <stop offset="55%" stopColor="#34d399" />
            <stop offset="100%" stopColor="#22d3ee" />
          </linearGradient>
        </defs>
        <circle cx="70" cy="70" r={R} fill="none" strokeWidth="9" className="stroke-muted" />
        <motion.circle
          cx="70"
          cy="70"
          r={R}
          fill="none"
          strokeWidth="9"
          strokeLinecap="round"
          stroke={`url(#${gradId})`}
          strokeDasharray={C}
          initial={{ strokeDashoffset: reduce ? offset : C }}
          whileInView={{ strokeDashoffset: offset }}
          viewport={{ once: true, margin: "-15%" }}
          transition={{ duration: 1.3, ease: EASE }}
        />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span
          className="bg-clip-text font-display text-5xl font-semibold tracking-tight text-transparent"
          style={{ backgroundImage: "linear-gradient(120deg, #6ee7b7, #34d399 55%, #22d3ee)" }}
        >
          <CountUp to={payPct} />%
        </span>
      </div>
    </div>
  );
}

// The "how much do I save" beat — hierarchy #2. The 30% / 5% figures are the
// prod default access-group multipliers (claude 0.3, codex 0.05), so this
// stays consistent with the effective price a visitor sees one click later on
// /pricing. Kept to a light compare — full per-model rates live there.
function Savings() {
  const { t } = useTranslation();
  const cards = [
    {
      name: t("home.savings.claudeName"),
      pay: 30,
      save: 70,
      saveLabel: t("home.savings.claudeSave"),
      gradId: "gauge-claude",
    },
    {
      name: t("home.savings.openaiName"),
      pay: 5,
      save: 95,
      saveLabel: t("home.savings.openaiSave"),
      gradId: "gauge-openai",
    },
  ];
  const chips = [
    t("home.savings.chipNoSub"),
    t("home.savings.chipPerToken"),
    t("home.savings.chipItemized"),
  ];

  return (
    <section className="relative overflow-hidden border-b border-border">
      <BackgroundMesh />
      <div className="relative mx-auto max-w-7xl px-4 py-24 md:px-6">
        <Reveal className="mx-auto mb-12 max-w-2xl text-center">
          <SectionEyebrow>{t("home.savingsEyebrow")}</SectionEyebrow>
          <h2 className="mt-3 font-display text-4xl font-semibold tracking-tight md:text-5xl">
            {t("home.savingsTitle")}
          </h2>
          <p className="mt-3 text-lg text-muted-foreground">{t("home.savingsSub")}</p>
        </Reveal>

        {/* Bento: two gauge dials over a full-width highlight bar */}
        <RevealStagger className="grid gap-4 md:grid-cols-6">
          {cards.map((c) => (
            <RevealItem key={c.name} className="md:col-span-3">
              <SpotlightCard className="h-full">
                <div className="flex items-center justify-between">
                  <span className="font-display text-xl font-medium tracking-tight">{c.name}</span>
                  <span className="inline-flex items-center rounded-full bg-success/15 px-2.5 py-1 text-xs font-medium text-success">
                    {c.saveLabel}
                  </span>
                </div>

                <div className="mt-6">
                  <SavingsGauge payPct={c.pay} savePct={c.save} gradId={c.gradId} />
                </div>

                <p className="mt-5 text-center text-sm text-muted-foreground">
                  {t("home.savings.ofOfficial")} ·{" "}
                  <span className="text-foreground/85">
                    {c.save}% {t("home.savings.saved")}
                  </span>
                </p>
              </SpotlightCard>
            </RevealItem>
          ))}

          <RevealItem className="md:col-span-6">
            <SpotlightCard className="h-full" tiltDeg={0}>
              <div className="flex flex-col items-start gap-6 md:flex-row md:items-center md:justify-between">
                <div className="max-w-xl">
                  <p className="font-display text-2xl font-semibold tracking-tight">
                    {t("home.savings.noLimitsTitle")}
                  </p>
                  <p className="mt-1.5 text-sm text-muted-foreground">
                    {t("home.savings.noLimitsSub")}
                  </p>
                  <div className="mt-4 flex flex-wrap gap-2">
                    {chips.map((chip) => (
                      <span
                        key={chip}
                        className="inline-flex items-center gap-1.5 rounded-full border border-border bg-card/50 px-3 py-1 text-xs text-foreground/80 backdrop-blur"
                      >
                        <Check className="h-3.5 w-3.5 text-primary" /> {chip}
                      </span>
                    ))}
                  </div>
                </div>
                <Magnetic strength={0.3} className="shrink-0">
                  <PrimaryLink to="/pricing">
                    {t("home.savings.cta")} <ArrowRight className="h-4 w-4" />
                  </PrimaryLink>
                </Magnetic>
              </div>
            </SpotlightCard>
          </RevealItem>
        </RevealStagger>
      </div>
    </section>
  );
}

/* ── Testimonials (vertical glass marquee) ─────────────────────────────── */

function Testimonials() {
  const { t } = useTranslation();
  const reduce = useReducedMotion();
  const items = (t("home.testimonials", { returnObjects: true }) as unknown as Testimonial[]) || [];
  const col1 = items.slice(0, 3);
  const col2 = items.slice(3, 6);
  const col3 = items.slice(6, 9);

  return (
    <section className="mx-auto max-w-7xl px-4 py-24 md:px-6">
      <Reveal className="mx-auto max-w-2xl text-center">
        <SectionEyebrow>{t("home.testimonialsEyebrow")}</SectionEyebrow>
        <h2 className="mt-3 font-display text-4xl font-semibold tracking-tight md:text-5xl">
          <Trans
            i18nKey="home.testimonialsTitle"
            components={{ hl: <span className="text-primary" /> }}
          />
        </h2>
        <p className="mt-3 text-lg text-muted-foreground">{t("home.testimonialsSub")}</p>
      </Reveal>

      <div className="mt-12 flex max-h-[680px] justify-center gap-6 overflow-hidden [mask-image:linear-gradient(to_bottom,transparent,black_18%,black_82%,transparent)]">
        <TestimonialsColumn testimonials={col1} duration={26} reduce={reduce ?? undefined} />
        <TestimonialsColumn
          testimonials={col2}
          duration={32}
          reduce={reduce ?? undefined}
          className="hidden md:block"
        />
        <TestimonialsColumn
          testimonials={col3}
          duration={29}
          reduce={reduce ?? undefined}
          className="hidden lg:block"
        />
      </div>
    </section>
  );
}

/* ── Footer with video background ──────────────────────────────────────── */

function SiteFooter({ t }: { t: (k: string) => string }) {
  return (
    // Curtain-reveal footer: pinned to the bottom of the viewport (fixed) and
    // sitting *behind* the page-canvas (z-0). As the canvas scrolls up off the
    // bottom, this cinematic "stargazer" scene (deep-blue night sky, a figure
    // looking up, framed by stacks of books) is uncovered beneath it — a true
    // parallax depth reveal. Base colour is the teal-navy page bg so it blends
    // into the blue; scrims darken only the top (columns) and bottom (legal bar).
    <footer
      className="dark fixed inset-x-0 bottom-0 z-0 flex h-[460px] flex-col overflow-hidden text-white sm:h-[600px]"
      style={{ backgroundColor: "var(--background)" }}
    >
      <div aria-hidden className="absolute inset-0 z-0 overflow-hidden">
        <HlsVideo
          src={FOOTER_VIDEO}
          className="kenburns object-[50%_56%]"
          style={{ opacity: 0.95 }}
          fallbackColor="#0a1828"
        />
      </div>
      {/* vertical scrim — opaque at top & bottom, clear through the middle */}
      <div
        aria-hidden
        className="absolute inset-0 z-[1]"
        style={{
          background:
            "linear-gradient(to bottom, var(--background) 0%, color-mix(in oklch, var(--background) 55%, transparent) 18%, transparent 44%, transparent 66%, color-mix(in oklch, var(--background) 80%, transparent) 88%, var(--background) 100%)",
        }}
      />
      {/* gentle side vignette to seat the books into frame */}
      <div
        aria-hidden
        className="absolute inset-0 z-[1]"
        style={{
          background:
            "radial-gradient(ellipse 78% 70% at 50% 55%, transparent 45%, color-mix(in oklch, var(--background) 45%, transparent) 100%)",
        }}
      />
      <div
        className="noise pointer-events-none absolute inset-0 z-[2] opacity-[0.18]"
        aria-hidden
      />

      <div className="relative z-10 flex flex-1 flex-col">
        <div className="mx-auto w-full max-w-7xl px-4 pt-14 md:px-6">
          <span className="eyebrow text-emerald-300/80">{t("home.badge")}</span>
          <div className="mt-5 grid gap-8 md:grid-cols-4">
            <div className="md:col-span-2">
              <Link
                to="/"
                className="flex items-center gap-2 font-display text-xl font-semibold text-white"
              >
                <span className="grid h-7 w-7 place-items-center rounded-md bg-emerald-400 text-emerald-950 shadow-[0_4px_20px_-4px_rgba(52,211,153,0.7)]">
                  <KeyRound className="h-3.5 w-3.5" />
                </span>
                HypiToken
              </Link>
              <p className="mt-3 max-w-xs text-sm text-white/70">{t("home.footerBlurb")}</p>
              <a
                href={DISCORD_INVITE}
                target="_blank"
                rel="noopener noreferrer"
                aria-label={t("common.joinDiscord")}
                className="mt-5 inline-flex h-9 w-9 items-center justify-center rounded-full bg-white/8 text-white/80 ring-1 ring-white/10 transition-colors hover:bg-[#5865f2] hover:text-white hover:ring-[#5865f2]"
              >
                <DiscordIcon className="h-4 w-4" />
              </a>
            </div>
            <FooterCol
              title={t("home.footerProduct")}
              links={[
                { to: "/status", label: t("nav.status") },
                { to: "/docs", label: t("nav.documentation") },
              ]}
            />
            <FooterCol
              title={t("home.footerAccount")}
              links={[
                { to: "/register", label: t("home.footerSignUp") },
                { to: "/login", label: t("nav.signIn") },
              ]}
            />
          </div>
        </div>

        {/* breathing room — the stargazer + sky show through here */}
        <div className="flex-1" />

        <div className="border-t border-white/10 backdrop-blur-[2px]">
          <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-5 text-xs text-white/55 md:px-6">
            <span>© {new Date().getFullYear()} HypiToken</span>
            <div className="flex items-center gap-4">
              <Link to="/terms" className="transition-colors hover:text-emerald-400">
                {t("nav.terms")}
              </Link>
              <Link to="/privacy" className="transition-colors hover:text-emerald-400">
                {t("nav.privacy")}
              </Link>
              <span className="font-mono">v2 · saas</span>
            </div>
          </div>
        </div>
      </div>
    </footer>
  );
}

function FooterCol({
  title,
  links,
}: {
  title: string;
  links: Array<{ to: string; label: string }>;
}) {
  return (
    <div>
      <h4 className="font-display text-sm font-medium uppercase tracking-wider text-white/60">
        {title}
      </h4>
      <ul className="mt-3 space-y-2 text-sm">
        {links.map((l) => (
          <li key={l.to}>
            <Link to={l.to} className="text-white/80 transition-colors hover:text-emerald-400">
              {l.label}
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

/* ── Terminal demo + architecture diagram (reglassed) ──────────────────── */

function TerminalDemo() {
  const { t } = useTranslation();
  const lines: Array<{ kind: "cmd" | "out" | "sep"; segs: Array<{ t: string; c?: string }> }> = [
    {
      kind: "cmd",
      segs: [
        { t: "$ ", c: "muted" },
        { t: "export ", c: "success" },
        { t: "ANTHROPIC_BASE_URL=" },
        { t: "https://api.novadiffusion.com", c: "info" },
      ],
    },
    {
      kind: "cmd",
      segs: [
        { t: "$ ", c: "muted" },
        { t: "export ", c: "success" },
        { t: "ANTHROPIC_AUTH_TOKEN=" },
        { t: "sk-cpa-•••", c: "info" },
      ],
    },
    {
      kind: "cmd",
      segs: [{ t: "$ ", c: "muted" }, { t: "claude " }, { t: '"review this PR"', c: "warn" }],
    },
    { kind: "out", segs: [{ t: "─ advisor ─────────────────────────", c: "muted" }] },
    {
      kind: "out",
      segs: [
        { t: "Routing to  ", c: "muted" },
        { t: "claude-sonnet-4-6", c: "info" },
        { t: "  [pool: 3 active]", c: "muted" },
      ],
    },
    {
      kind: "out",
      segs: [
        { t: "Session     ", c: "muted" },
        { t: "sticky → credential #2", c: "dim" },
      ],
    },
    { kind: "sep", segs: [] },
    { kind: "out", segs: [{ t: "─ response ─────────────────────────", c: "muted" }] },
    { kind: "out", segs: [{ t: "Here's my analysis of the changes…", c: "dim" }] },
    { kind: "sep", segs: [] },
    { kind: "out", segs: [{ t: "in     2,841 tok · out    914 tok", c: "muted" }] },
    {
      kind: "out",
      segs: [
        { t: "duration  ", c: "muted" },
        { t: "1.24s", c: "success" },
        { t: "  (claude-sonnet-4-6)", c: "muted" },
      ],
    },
  ];
  const cls = (c?: string) =>
    c === "muted"
      ? "text-muted-foreground"
      : c === "success"
        ? "text-success"
        : c === "info"
          ? "text-info"
          : c === "warn"
            ? "text-warning"
            : c === "dim"
              ? "text-foreground/70"
              : "";
  return (
    <div className="relative overflow-hidden rounded-xl border border-border-strong bg-card/80 text-left shadow-xl">
      <div className="flex items-center gap-2 border-b border-border bg-muted/40 px-4 py-2.5">
        <span className="h-2.5 w-2.5 rounded-full bg-destructive/70" />
        <span className="h-2.5 w-2.5 rounded-full bg-warning/70" />
        <span className="h-2.5 w-2.5 rounded-full bg-success/70" />
        <span className="ml-2 font-mono text-xs text-muted-foreground">
          claude-code — {t("home.archGatewayLabel")}
        </span>
      </div>
      <div className="overflow-x-auto px-6 py-5 font-mono text-[13px] leading-[1.7] md:text-sm">
        {lines.map((l, i) =>
          l.kind === "sep" ? (
            // biome-ignore lint/suspicious/noArrayIndexKey: static terminal display; index is the stable position
            <div key={i} className="h-2" />
          ) : (
            // biome-ignore lint/suspicious/noArrayIndexKey: static terminal display; index is the stable position
            <div key={i} className="whitespace-pre">
              {l.segs.map((s, j) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: static terminal display; segment index is stable position within line
                <span key={j} className={cls(s.c)}>
                  {s.t}
                </span>
              ))}
            </div>
          ),
        )}
      </div>
    </div>
  );
}
