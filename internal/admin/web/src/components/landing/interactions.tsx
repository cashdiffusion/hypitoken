import {
  motion,
  useMotionTemplate,
  useMotionValue,
  useReducedMotion,
  useSpring,
} from "motion/react";
import { type ReactNode, useRef } from "react";
import { cn } from "@/lib/utils";

/* SpotlightCard — a glass card with a cursor-following primary-tinted glow and
 * an optional micro 3D tilt. The glow reads as premium ambient light, not a
 * cheap neon halo. Tilt is tiny (≤ a few degrees) and disabled for reduced
 * motion. Use in place of a plain glass bento cell. */
export function SpotlightCard({
  children,
  className,
  tiltDeg = 2.5,
  padded = true,
  spotlight = true,
}: {
  children: ReactNode;
  className?: string;
  tiltDeg?: number;
  /** Set false when the content needs to bleed to the card's edges (full-width
   * banners, footers). The caller then owns all internal spacing. */
  padded?: boolean;
  /** Set false in dense grids. The cursor glow reads as premium on a handful
   * of hero cards, but each instance measures its rect on every mousemove — a
   * forced layout — and drives three springs. Two dozen of them on one screen
   * turned pointer movement into steady layout thrash, for an effect nobody
   * notices at that density. The CSS hover shadow and hairline stay. */
  spotlight?: boolean;
}) {
  const reduce = useReducedMotion();
  const ref = useRef<HTMLDivElement>(null);
  const px = useMotionValue(-9999);
  const py = useMotionValue(-9999);
  const glowOpacity = useSpring(0, { stiffness: 200, damping: 28 });
  const rx = useSpring(0, { stiffness: 150, damping: 18 });
  const ry = useSpring(0, { stiffness: 150, damping: 18 });

  const glow = useMotionTemplate`radial-gradient(420px circle at ${px}px ${py}px, color-mix(in oklch, var(--primary) 15%, transparent), transparent 70%)`;

  function onMove(e: React.MouseEvent) {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const x = e.clientX - r.left;
    const y = e.clientY - r.top;
    px.set(x);
    py.set(y);
    if (!reduce && tiltDeg) {
      ry.set((x / r.width - 0.5) * tiltDeg * 2);
      rx.set(-(y / r.height - 0.5) * tiltDeg * 2);
    }
  }

  return (
    <motion.div
      ref={ref}
      onMouseMove={spotlight ? onMove : undefined}
      onMouseEnter={spotlight ? () => glowOpacity.set(1) : undefined}
      onMouseLeave={
        spotlight
          ? () => {
              glowOpacity.set(0);
              rx.set(0);
              ry.set(0);
            }
          : undefined
      }
      style={
        reduce || !spotlight ? undefined : { rotateX: rx, rotateY: ry, transformPerspective: 1000 }
      }
      className={cn(
        "glass group relative overflow-hidden rounded-2xl transition-shadow duration-300 hover:shadow-[0_20px_60px_-24px_color-mix(in_oklch,var(--primary)_45%,transparent)]",
        padded && "p-6",
        className,
      )}
    >
      {spotlight && (
        <motion.div
          aria-hidden
          className="pointer-events-none absolute inset-0"
          style={{ background: glow, opacity: glowOpacity }}
        />
      )}
      {/* hairline top highlight that brightens on hover */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 h-px opacity-60 transition-opacity duration-300 group-hover:opacity-100"
        style={{
          background:
            "linear-gradient(90deg, transparent, color-mix(in oklch, var(--primary) 60%, transparent), transparent)",
        }}
      />
      {/* Unpadded cards own their layout end-to-end, so the wrapper has to
          forward the flex column — otherwise a child's `mt-auto` has nothing to
          push against and short cards leave dead space in a stretched grid. */}
      <div className={cn("relative", !padded && "flex h-full flex-col")}>{children}</div>
    </motion.div>
  );
}

/* Magnetic — wraps an interactive element so it drifts toward the cursor, then
 * springs back on leave. Subtle by default; honours reduced motion. */
export function Magnetic({
  children,
  strength = 0.35,
  className,
}: {
  children: ReactNode;
  strength?: number;
  className?: string;
}) {
  const reduce = useReducedMotion();
  const ref = useRef<HTMLDivElement>(null);
  const x = useSpring(0, { stiffness: 220, damping: 16 });
  const y = useSpring(0, { stiffness: 220, damping: 16 });

  if (reduce) return <div className={className}>{children}</div>;

  function onMove(e: React.MouseEvent) {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    x.set((e.clientX - (r.left + r.width / 2)) * strength);
    y.set((e.clientY - (r.top + r.height / 2)) * strength);
  }
  return (
    <motion.div
      ref={ref}
      onMouseMove={onMove}
      onMouseLeave={() => {
        x.set(0);
        y.set(0);
      }}
      style={{ x, y }}
      className={cn("inline-flex", className)}
    >
      {children}
    </motion.div>
  );
}

/* Marquee — seamless infinite horizontal scroll. Duplicates its children and
 * translates -50%; edges fade via a mask. Pauses on hover. */
export function Marquee({
  children,
  durationSec = 32,
  className,
}: {
  children: ReactNode;
  durationSec?: number;
  className?: string;
}) {
  const reduce = useReducedMotion();
  return (
    <div
      className={cn("group/marquee relative overflow-hidden", className)}
      style={{
        maskImage: "linear-gradient(90deg, transparent, black 12%, black 88%, transparent)",
        WebkitMaskImage: "linear-gradient(90deg, transparent, black 12%, black 88%, transparent)",
      }}
    >
      <div
        className={cn(
          "flex w-max items-center gap-12",
          !reduce && "animate-marquee group-hover/marquee:[animation-play-state:paused]",
        )}
        style={{ ["--marquee-dur" as string]: `${durationSec}s` }}
      >
        <div className="flex shrink-0 items-center gap-12">{children}</div>
        <div className="flex shrink-0 items-center gap-12" aria-hidden>
          {children}
        </div>
      </div>
    </div>
  );
}
