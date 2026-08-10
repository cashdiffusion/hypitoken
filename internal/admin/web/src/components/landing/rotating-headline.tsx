import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { type CSSProperties, useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";

/*
 * Rotating provider headline.
 *
 * Codex overtook Claude as the bulk of real traffic, so the hero can no longer
 * lead with one brand. Rather than cross-fading two unrelated sentences, the
 * sentence frame ("… API, at … of list price.") stays put and only the two
 * variable slots — brand name and percentage — swap. It reads as one offer
 * quoted for a second provider, which is what the product actually is.
 *
 * Motion personality: Premium. Exit accelerates out (300ms), a 60ms breath,
 * then entry decelerates in (420ms). The percentage is the hero of the line, so
 * it is staged 90ms behind the brand name and carries a settle-only scale — no
 * overshoot, which would read as playful and fight the cinematic hero band.
 *
 * The number never travels more than 14px and only one element is the primary
 * mover at a time, so the 1/3 rules hold. Under prefers-reduced-motion the
 * rotation survives as a plain cross-fade: suppressing it entirely would hide
 * the Claude half of the offer from exactly the users least able to hunt for it.
 */

export type HeadlineSlide = {
  /** Provider brand, the first variable slot. */
  name: string;
  /** Effective price as a percentage of list, the second (hero) slot. */
  pct: string;
  /** Gradient for the percentage — the secondary motion layer. */
  gradient: string;
  /** Accent used by the progress indicator and by the ambient hero glow. */
  accent: string;
};

/** Dwell before the swap begins. */
const DWELL_MS = 4400;
/** Exit + hero stagger + entry — how long the headline is actually in motion. */
const SWAP_S = 0.3 + 0.09 + 0.42;

const EXIT = { duration: 0.3, ease: [0.4, 0, 1, 1] as const };
const ENTER = { duration: 0.42, ease: [0.22, 1, 0.36, 1] as const };
/** Staging gap that makes the percentage land after the brand name. */
const HERO_STAGGER = 0.09;

function Slot({
  value,
  stagger = 0,
  reduce,
  className,
  style,
  hero = false,
}: {
  value: string;
  stagger?: number;
  reduce: boolean;
  className?: string;
  style?: CSSProperties;
  hero?: boolean;
}) {
  const enter = reduce ? { opacity: 1 } : { opacity: 1, y: 0, filter: "blur(0px)", scale: 1 };
  const from = reduce
    ? { opacity: 0 }
    : { opacity: 0, y: 14, filter: "blur(8px)", scale: hero ? 1.06 : 1 };
  // The exit curve rides on the target rather than the shared `transition`,
  // because entry and exit pull in opposite directions: entry decelerates in,
  // exit accelerates away. One curve for both would flatten the swap.
  const exit = reduce
    ? { opacity: 0, transition: { duration: 0.25 } }
    : {
        opacity: 0,
        y: -14,
        filter: "blur(6px)",
        scale: 1,
        transition: { ...EXIT, delay: stagger },
      };

  return (
    // The slot animates its own width instead of being pre-sized to the widest
    // variant. Reserving the maximum was the first attempt and it left a dead
    // gap between "5%" and the period that read as a rendering bug; letting the
    // sentence breathe by ~30px is both tighter to read and better motion.
    <motion.span
      layout={!reduce}
      transition={{ duration: 0.42, ease: [0.22, 1, 0.36, 1] }}
      className="relative inline-grid align-baseline"
    >
      <AnimatePresence mode="wait" initial={false}>
        <motion.span
          key={value}
          className={cn("col-start-1 row-start-1 whitespace-nowrap", className)}
          style={style}
          initial={from}
          animate={enter}
          exit={exit}
          transition={reduce ? { duration: 0.25 } : { ...ENTER, delay: stagger }}
        >
          {value}
        </motion.span>
      </AnimatePresence>
    </motion.span>
  );
}

export function RotatingHeadline({ slides, index }: { slides: HeadlineSlide[]; index: number }) {
  const { t } = useTranslation();
  const reduce = !!useReducedMotion();
  const active = slides[index];

  return (
    <>
      <Slot value={active.name} reduce={reduce} />
      {t("home.rotator.mid")}
      <Slot
        value={active.pct}
        stagger={HERO_STAGGER}
        reduce={reduce}
        hero
        className="bg-clip-text text-transparent"
        style={{ backgroundImage: active.gradient }}
      />
      {t("home.rotator.suffix")}
      <span className="sr-only" aria-live="polite">
        {active.name} {active.pct}
      </span>
    </>
  );
}

/*
 * Two bars, not decorative dots: the active one fills over the dwell so a
 * first-time visitor can tell the headline is about to change rather than
 * suspecting a render glitch. Linear is deliberate here — it is the one place
 * the quality rules allow it, because a progress bar that eases lies about
 * elapsed time. Clicking a bar pins that provider.
 */
export function HeadlineProgress({
  slides,
  index,
  onSelect,
}: {
  slides: HeadlineSlide[];
  index: number;
  onSelect: (i: number) => void;
}) {
  const { t } = useTranslation();
  const reduce = !!useReducedMotion();
  return (
    <div className="flex items-center justify-center gap-2">
      {slides.map((s, i) => (
        <button
          key={s.name}
          type="button"
          onClick={() => onSelect(i)}
          aria-label={t("home.rotator.show", { name: s.name })}
          aria-current={i === index}
          className="group h-4 w-10 shrink-0 px-0"
        >
          <span className="block h-[3px] w-full overflow-hidden rounded-full bg-white/20 transition-colors group-hover:bg-white/35">
            {i === index && (
              <motion.span
                key={`${s.name}-${reduce}`}
                className="block h-full rounded-full"
                style={{ background: s.accent }}
                initial={{ width: reduce ? "100%" : "0%" }}
                animate={{ width: "100%" }}
                // The index flips when the swap *starts*, so the fill waits out
                // the swap. Otherwise the bar advertises the new provider while
                // the headline is still showing the old one on its way out.
                transition={{
                  duration: reduce ? 0 : DWELL_MS / 1000,
                  delay: reduce ? 0 : SWAP_S,
                  ease: "linear",
                }}
              />
            )}
          </span>
        </button>
      ))}
    </div>
  );
}

/**
 * Drives the rotation. Pauses while the pointer is over the hero and while the
 * tab is hidden — a headline that silently cycles in a background tab burns
 * frames and lands the visitor on a random provider when they come back.
 */
export function useHeadlineRotation(count: number, paused: boolean) {
  const [index, setIndex] = useState(0);
  const [pinned, setPinned] = useState(false);
  const timer = useRef<number | undefined>(undefined);

  const select = useCallback((i: number) => {
    setIndex(i);
    setPinned(true);
  }, []);

  useEffect(() => {
    if (paused || pinned || count < 2) return;
    const tick = () => {
      if (document.hidden) return;
      setIndex((i) => (i + 1) % count);
    };
    // Dwell plus the swap, so the indicator finishes filling exactly as the
    // next swap begins.
    timer.current = window.setInterval(tick, DWELL_MS + SWAP_S * 1000);
    return () => window.clearInterval(timer.current);
  }, [paused, pinned, count]);

  return { index, select };
}
