import confetti from "canvas-confetti";

// Emerald-forward palette so the celebration matches the brand accent.
const COLORS = ["#10b981", "#34d399", "#6ee7b7", "#047857", "#a7f3d0", "#ffffff"];

// celebrate fires a short, layered confetti burst from `origin` (viewport
// fractions, 0–1). Used on a successful verification. canvas-confetti honours
// prefers-reduced-motion via disableForReducedMotion, so callers don't have to.
export function celebrate(origin: { x: number; y: number } = { x: 0.5, y: 0.4 }) {
  const base = {
    origin,
    colors: COLORS,
    disableForReducedMotion: true,
    zIndex: 100,
  } as const;
  // main burst
  confetti({ ...base, particleCount: 70, spread: 72, startVelocity: 42, scalar: 0.95 });
  // wide low-velocity haze
  confetti({
    ...base,
    particleCount: 32,
    spread: 120,
    startVelocity: 26,
    scalar: 0.7,
    decay: 0.92,
  });
  // delayed sparkle pop
  setTimeout(() => {
    confetti({ ...base, particleCount: 24, spread: 90, startVelocity: 34, scalar: 1.1 });
  }, 130);
}
