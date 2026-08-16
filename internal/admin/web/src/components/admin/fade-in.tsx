import { motion, useReducedMotion } from "motion/react";
import type { ReactNode } from "react";

/* FadeIn — the single entrance the admin panel uses: one short opacity-only
 * fade on a tab's root. Tab switches remount content and replay entrances, so
 * anything heavier (blur, rise, stagger) reads as churn rather than polish.
 * Collapses to a plain div when the user prefers reduced motion. */
export function FadeIn({ children, className }: { children: ReactNode; className?: string }) {
  const reduce = useReducedMotion();
  if (reduce) return <div className={className}>{children}</div>;
  return (
    <motion.div
      className={className}
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.2, ease: "easeOut" }}
    >
      {children}
    </motion.div>
  );
}
