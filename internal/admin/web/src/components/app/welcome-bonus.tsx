import { Gift, Sparkles } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { CountUp } from "@/components/app/page-primitives";
import { Button } from "@/components/ui/button";
import { celebrate } from "@/lib/confetti";
import { fmtUSD } from "@/lib/utils";

// WelcomeBonus is the full-screen celebration shown ONCE, right after a new
// user finishes registration and lands on the dashboard. It count-ups the
// credited amount, fires a confetti burst, and dismisses on tap. It is never
// shown on login or on a page refresh — the caller passes the granted amount
// from the register response via router state and clears it after first paint.
export function WelcomeBonus({ amount, onDismiss }: { amount: number; onDismiss: () => void }) {
  const { t } = useTranslation();
  const reduce = useReducedMotion();

  // Confetti on mount (layered burst), plus a second pop once the count-up
  // lands so the celebration peaks with the number.
  useEffect(() => {
    celebrate({ x: 0.5, y: 0.42 });
    const id = window.setTimeout(() => celebrate({ x: 0.5, y: 0.42 }), 900);
    return () => window.clearTimeout(id);
  }, []);

  return (
    <AnimatePresence>
      <motion.div
        key="welcome-bonus"
        className="fixed inset-0 z-[120] flex items-center justify-center overflow-hidden bg-background/85 backdrop-blur-xl"
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        transition={{ duration: 0.4 }}
        onClick={onDismiss}
      >
        {/* ambient emerald glow */}
        <div
          aria-hidden
          className="pointer-events-none absolute left-1/2 top-1/2 h-[44rem] w-[44rem] -translate-x-1/2 -translate-y-1/2 rounded-full bg-emerald-500/15 blur-[120px]"
        />

        <motion.div
          className="relative mx-6 flex max-w-md flex-col items-center gap-6 text-center"
          initial={{ scale: reduce ? 1 : 0.9, y: reduce ? 0 : 16, opacity: 0 }}
          animate={{ scale: 1, y: 0, opacity: 1 }}
          transition={{ type: "spring", stiffness: 220, damping: 22, delay: 0.1 }}
          onClick={(e) => e.stopPropagation()}
        >
          {/* gift mark with a soft pulsing ring */}
          <div className="relative">
            <motion.div
              aria-hidden
              className="absolute inset-0 rounded-3xl bg-emerald-400/30 blur-xl"
              animate={reduce ? undefined : { scale: [1, 1.25, 1], opacity: [0.6, 0.3, 0.6] }}
              transition={{ duration: 2.4, repeat: Number.POSITIVE_INFINITY, ease: "easeInOut" }}
            />
            <div className="relative flex h-20 w-20 items-center justify-center rounded-3xl border border-emerald-400/40 bg-gradient-to-br from-emerald-400/20 to-emerald-600/10 text-emerald-400 shadow-[0_0_40px_-8px] shadow-emerald-500/40">
              <Gift className="h-9 w-9" strokeWidth={1.6} />
            </div>
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-center gap-1.5 text-xs font-medium uppercase tracking-[0.2em] text-emerald-400">
              <Sparkles className="h-3.5 w-3.5" />
              {t("dashboard.welcomeBonus.eyebrow")}
            </div>
            <h2 className="font-display text-2xl font-medium tracking-tight text-foreground">
              {t("dashboard.welcomeBonus.title")}
            </h2>
          </div>

          {/* the credited amount, count-up */}
          <div className="font-display text-[64px] font-semibold leading-none tracking-tight text-emerald-400 drop-shadow-[0_0_24px_rgba(16,185,129,0.35)]">
            +<CountUp value={amount} durationMs={1000} format={(n) => fmtUSD(n)} />
          </div>

          <p className="max-w-xs text-sm leading-relaxed text-muted-foreground">
            {t("dashboard.welcomeBonus.sub")}
          </p>

          <Button
            size="lg"
            className="mt-2 bg-emerald-500 px-8 text-white hover:bg-emerald-600"
            onClick={onDismiss}
          >
            {t("dashboard.welcomeBonus.cta")}
          </Button>
        </motion.div>
      </motion.div>
    </AnimatePresence>
  );
}
