import { ArrowUpRight, Bell, LifeBuoy, MessagesSquare } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import type { ElementType, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { DiscordIcon } from "@/components/icons/discord";
import { SpotlightCard } from "@/components/landing/interactions";
import { Reveal, RevealItem, RevealStagger } from "@/components/landing/reveal";
import { DISCORD_INVITE } from "@/lib/social";

// Discord brand blurple — used *only* on Discord-owned elements (glyph, CTA,
// focal-card glow). The rest of the section stays in the site's primary
// language so the page reads as one product, not a Discord skin.
const BLURPLE = "#5865f2";

// Section eyebrow — same L-bracket + mono label mark as home.tsx, kept local so
// this section is self-contained.
function SectionEyebrow({ children }: { children: ReactNode }) {
  return (
    <span className="relative inline-flex items-center gap-2.5">
      <span aria-hidden className="block h-3 w-3 shrink-0 border-l border-t border-primary/50" />
      <span className="eyebrow text-primary">{children}</span>
    </span>
  );
}

export function Community() {
  const { t } = useTranslation();

  const reasons: Array<{ icon: ElementType; title: string; body: string }> = [
    {
      icon: LifeBuoy,
      title: t("home.community.supportT"),
      body: t("home.community.supportB"),
    },
    {
      icon: Bell,
      title: t("home.community.updatesT"),
      body: t("home.community.updatesB"),
    },
    {
      icon: MessagesSquare,
      title: t("home.community.talkT"),
      body: t("home.community.talkB"),
    },
  ];

  return (
    <section className="mx-auto max-w-7xl px-4 py-24 md:px-6">
      <Reveal className="mx-auto max-w-2xl text-center">
        <SectionEyebrow>{t("home.community.eyebrow")}</SectionEyebrow>
        <h2 className="mt-3 font-display text-4xl font-semibold tracking-tight md:text-5xl">
          {t("home.community.title")}
        </h2>
        <p className="mt-3 text-lg text-muted-foreground">{t("home.community.sub")}</p>
      </Reveal>

      {/* Bento: a 2/3 focal Discord card + a 1/3 column of real reasons to join.
          The size hierarchy (the card spanning two of three columns) is what
          makes it a bento rather than an even grid. The reasons live in a real
          flex box (not display:contents) so the stagger's whileInView fires. */}
      <div className="mt-12 grid gap-4 lg:grid-cols-3">
        <Reveal className="lg:col-span-2">
          <DiscordCard />
        </Reveal>

        <RevealStagger className="flex flex-col gap-4 lg:col-start-3">
          {reasons.map((r) => (
            <RevealItem key={r.title} className="flex-1">
              <SpotlightCard className="h-full p-5">
                <div className="flex items-start gap-3.5">
                  <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary ring-1 ring-primary/15">
                    <r.icon className="size-[18px]" />
                  </span>
                  <div>
                    <h3 className="font-display text-base font-medium tracking-tight">{r.title}</h3>
                    <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{r.body}</p>
                  </div>
                </div>
              </SpotlightCard>
            </RevealItem>
          ))}
        </RevealStagger>
      </div>
    </section>
  );
}

// DiscordCard — the focal glass cell. Blurple appears here (glyph chip, glow,
// CTA) because the whole card *is* the Discord surface; everything around it
// stays neutral.
function DiscordCard() {
  const { t } = useTranslation();
  return (
    <a
      href={DISCORD_INVITE}
      target="_blank"
      rel="noopener noreferrer"
      className="group glass relative flex h-full min-h-[320px] flex-col justify-between overflow-hidden rounded-2xl p-8 transition-shadow duration-300 md:p-10"
      style={{
        // soft blurple seat — kept to the corner so the card doesn't drown in it
        backgroundImage:
          "radial-gradient(ellipse 70% 90% at 100% 0%, color-mix(in oklch, #5865f2 16%, transparent), transparent 60%)",
      }}
    >
      {/* hairline top highlight, blurple-tinted, brightens on hover */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 h-px opacity-60 transition-opacity duration-300 group-hover:opacity-100"
        style={{
          background: `linear-gradient(90deg, transparent, color-mix(in oklch, ${BLURPLE} 70%, transparent), transparent)`,
        }}
      />
      <ConstellationMotif />

      <div className="relative flex items-center justify-between">
        <div className="flex items-center gap-3">
          <span
            className="grid size-12 place-items-center rounded-xl text-white shadow-lg ring-1 ring-white/10"
            style={{ backgroundColor: BLURPLE, boxShadow: `0 10px 30px -10px ${BLURPLE}` }}
          >
            <DiscordIcon className="size-7" />
          </span>
          <div>
            <p className="font-display text-lg font-semibold tracking-tight">Discord</p>
            <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <span className="relative flex size-1.5">
                <span className="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-75" />
                <span className="relative inline-flex size-1.5 rounded-full bg-success" />
              </span>
              {t("home.community.live")}
            </span>
          </div>
        </div>
        <ArrowUpRight className="size-5 text-muted-foreground transition-all duration-300 group-hover:-translate-y-0.5 group-hover:translate-x-0.5 group-hover:text-foreground" />
      </div>

      <div className="relative mt-8">
        <h3 className="max-w-md font-display text-2xl font-semibold tracking-tight md:text-3xl">
          {t("home.community.cardTitle")}
        </h3>
        <p className="mt-3 max-w-md text-muted-foreground">{t("home.community.cardSub")}</p>
        <span
          className="mt-7 inline-flex items-center gap-2 rounded-full px-5 py-2.5 text-sm font-medium text-white transition-transform duration-300 group-hover:scale-[1.02]"
          style={{ backgroundColor: BLURPLE, boxShadow: `0 8px 30px -8px ${BLURPLE}` }}
        >
          <DiscordIcon className="size-4" />
          {t("home.community.cta")}
        </span>
      </div>
    </a>
  );
}

// ConstellationMotif — ambient community texture for the focal card: a handful
// of softly drifting member dots in the top-right, a couple pinging. Replaces
// the CTA section's 3D geometry so the two adjacent sections don't repeat the
// same motif. Decorative, reduced-motion aware.
function ConstellationMotif() {
  const reduce = useReducedMotion();
  // positions as % within the card, biased to the upper-right negative space
  const dots = [
    { x: 78, y: 22, s: 6, d: 0 },
    { x: 90, y: 38, s: 4, d: 0.6 },
    { x: 68, y: 14, s: 4, d: 1.2 },
    { x: 84, y: 58, s: 5, d: 0.3 },
    { x: 95, y: 18, s: 3, d: 0.9 },
    { x: 72, y: 40, s: 3, d: 1.5 },
  ];
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0">
      {dots.map((p) => (
        <motion.span
          key={`${p.x}-${p.y}`}
          className="absolute rounded-full"
          style={{
            left: `${p.x}%`,
            top: `${p.y}%`,
            width: p.s,
            height: p.s,
            backgroundColor: BLURPLE,
            opacity: 0.45,
          }}
          animate={reduce ? undefined : { y: [0, -7, 0], opacity: [0.3, 0.6, 0.3] }}
          transition={
            reduce
              ? undefined
              : { duration: 4 + p.d, repeat: Infinity, ease: "easeInOut", delay: p.d }
          }
        />
      ))}
    </div>
  );
}
