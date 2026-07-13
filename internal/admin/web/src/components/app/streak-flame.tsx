import { Flame } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { CountUp } from "@/components/app/page-primitives";
import { celebrate } from "@/lib/confetti";
import { cn } from "@/lib/utils";

/* StreakFlame — consecutive-days-of-use, as a thing you don't want to lose.
 *
 * The design brief was "like a game, but not cheap". Cheapness in UI motion comes
 * from fast, high-contrast, high-frequency effects (blinking, jitter, confetti on
 * every render). So the flame does the opposite:
 *
 *  - The only thing that scales with the streak is the flame's SIZE. A 3-day
 *    streak is a spark; a 30-day one is a bonfire. One continuous variable
 *    carries the whole progression — no badges, no tiers, no extra chrome.
 *  - The idle motion is a slow 2.2s breath and a slow gradient drift. It reads as
 *    alive rather than as an animation demanding attention.
 *  - Losing the streak greys the flame out. The stake is the point of a streak;
 *    if breaking it costs nothing visually, keeping it means nothing.
 *  - Confetti fires ONCE per milestone, ever — gated on localStorage. A page that
 *    throws a party every time you open it is a page you learn to dread.
 */

const MILESTONES = [3, 7, 14, 30, 60, 100] as const;
const CELEBRATED_KEY = "hypi.streak.celebrated";

/** Highest milestone reached at `days` (0 if none). */
function milestoneFor(days: number): number {
  let hit = 0;
  for (const m of MILESTONES) if (days >= m) hit = m;
  return hit;
}

/** Next milestone above `days`, or null once they've passed the last one. */
function nextMilestone(days: number): number | null {
  return MILESTONES.find((m) => m > days) ?? null;
}

export function StreakFlame({ current, longest }: { current: number; longest: number }) {
  const { t } = useTranslation();
  const reduce = useReducedMotion();
  const flameRef = useRef<HTMLDivElement>(null);
  const [party, setParty] = useState<number | null>(null);

  const alive = current > 0;
  // Grows to ~1.35× by day 30, then holds — an unbounded scale would eventually
  // blow out the layout.
  const scale = 1 + Math.min(current / 30, 1) * 0.35;
  const next = nextMilestone(current);

  useEffect(() => {
    const reached = milestoneFor(current);
    if (!reached) return;
    const seen = Number(localStorage.getItem(CELEBRATED_KEY) || 0);
    if (reached <= seen) return;
    localStorage.setItem(CELEBRATED_KEY, String(reached));
    setParty(reached);
    const box = flameRef.current?.getBoundingClientRect();
    if (box) {
      celebrate({
        x: (box.left + box.width / 2) / window.innerWidth,
        y: (box.top + box.height / 2) / window.innerHeight,
      });
    }
    const timer = setTimeout(() => setParty(null), 3200);
    return () => clearTimeout(timer);
  }, [current]);

  return (
    <div className="glass relative flex items-center gap-5 overflow-hidden rounded-2xl px-5 py-4 md:px-6">
      {/* flame */}
      <div ref={flameRef} className="relative grid h-16 w-16 shrink-0 place-items-center">
        <motion.div
          className={cn("relative", !alive && "opacity-40 grayscale")}
          style={{ scale }}
          animate={
            reduce || !alive
              ? undefined
              : { scale: [scale, scale * 1.04, scale], opacity: [0.92, 1, 0.92] }
          }
          transition={{ duration: 2.2, repeat: Number.POSITIVE_INFINITY, ease: "easeInOut" }}
        >
          <Flame
            className="h-10 w-10"
            style={{
              // A single icon, filled with a warm→primary gradient rather than a
              // flat accent colour, so it reads as fire and not as an icon that
              // happens to be orange.
              color: alive ? "var(--warning)" : "var(--muted-foreground)",
              fill: alive
                ? "color-mix(in oklch, var(--warning) 45%, var(--primary))"
                : "transparent",
            }}
          />
        </motion.div>

        {/* embers — 8 DOM nodes, no canvas needed */}
        {alive && !reduce && (
          <div className="pointer-events-none absolute inset-0">
            {Array.from({ length: 8 }, (_, i) => (
              <motion.span
                // biome-ignore lint/suspicious/noArrayIndexKey: fixed-size decorative particle set
                key={i}
                className="absolute left-1/2 top-1/2 h-1 w-1 rounded-full"
                style={{ backgroundColor: "var(--warning)" }}
                animate={{
                  y: [-4, -30],
                  x: [0, (i % 2 === 0 ? 1 : -1) * (2 + (i % 4) * 3)],
                  opacity: [0, 0.9, 0],
                  scale: [0.6, 1, 0.3],
                }}
                transition={{
                  duration: 1.6 + i * 0.12,
                  repeat: Number.POSITIVE_INFINITY,
                  delay: i * 0.2,
                  ease: "easeOut",
                }}
              />
            ))}
          </div>
        )}
      </div>

      {/* numbers */}
      <div className="min-w-0">
        <div className="text-xs uppercase tracking-wider text-muted-foreground">
          {t("usage.streak.title")}
        </div>
        <div className="mt-0.5 flex items-baseline gap-2">
          <span
            className={cn(
              "font-mono text-4xl font-semibold tabular-nums",
              alive ? "text-warning" : "text-muted-foreground",
            )}
          >
            <CountUp value={current} />
          </span>
          <span className="text-sm text-muted-foreground">{t("usage.streak.days")}</span>
        </div>
        <div className="mt-1 text-xs text-muted-foreground">
          {alive
            ? next
              ? t("usage.streak.next", { n: next - current })
              : t("usage.streak.maxed")
            : t("usage.streak.broken")}
          {longest > 0 && <> · {t("usage.streak.longest", { n: longest })}</>}
        </div>
      </div>

      {/* milestone banner */}
      <AnimatePresence>
        {party && (
          <motion.div
            initial={{ opacity: 0, y: 8, scale: 0.9 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, scale: 0.95 }}
            transition={{ type: "spring", stiffness: 400, damping: 22 }}
            className="ml-auto rounded-full bg-warning/15 px-3 py-1.5 text-sm font-semibold text-warning"
          >
            🔥 {t("usage.streak.milestone", { n: party })}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
