import { Gift, ShieldAlert, Sparkles } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { CountUp } from "@/components/app/page-primitives";
import { Button } from "@/components/ui/button";
import { celebrate } from "@/lib/confetti";
import { fmtUSD } from "@/lib/utils";

// WelcomeBonus is the full-screen overlay shown ONCE, right after a new user
// finishes registration and lands on the dashboard. Two modes:
//
//   - bonus  (fraud=false, amount>0): an emerald celebration that count-ups the
//     credited trial/channel bonus and fires confetti.
//   - fraud  (fraud=true): an amber "bonus withheld" notice explaining the
//     signup looked like a repeat device/network, no confetti.
//
// It is never shown on login or on a page refresh — the caller passes the state
// from the register response via router state and clears it after first paint.
export function WelcomeBonus({
  amount,
  fraud = false,
  onDismiss,
}: {
  amount: number;
  fraud?: boolean;
  onDismiss: () => void;
}) {
  const { t } = useTranslation();
  const reduce = useReducedMotion();

  // Confetti only in the celebratory (non-fraud) mode.
  useEffect(() => {
    if (fraud) return;
    celebrate({ x: 0.5, y: 0.42 });
    const id = window.setTimeout(() => celebrate({ x: 0.5, y: 0.42 }), 900);
    return () => window.clearTimeout(id);
  }, [fraud]);

  // Icon differs per mode; the colour classes are applied inline below.
  const Icon = fraud ? ShieldAlert : Gift;

  return (
    <motion.div
      key="welcome-bonus"
      className="fixed inset-0 z-[120] flex items-center justify-center overflow-hidden bg-background/85 backdrop-blur-lg"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: reduce ? 0 : 0.28, ease: [0.2, 0, 0, 1] }}
      onClick={onDismiss}
    >
      {/* ambient glow */}
      <div
        aria-hidden
        className={`pointer-events-none absolute left-1/2 top-1/2 h-[44rem] w-[44rem] -translate-x-1/2 -translate-y-1/2 rounded-full blur-[120px] ${
          fraud ? "bg-amber-500/15" : "bg-emerald-500/15"
        }`}
      />

      <motion.div
        className="relative mx-6 flex max-w-md flex-col items-center gap-6 text-center"
        initial={{ scale: reduce ? 1 : 0.9, y: reduce ? 0 : 16, opacity: 0 }}
        animate={{ scale: 1, y: 0, opacity: 1 }}
        transition={{ type: "spring", stiffness: 220, damping: 22, delay: 0.1 }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* icon mark with a soft pulsing ring */}
        <div className="relative">
          <motion.div
            aria-hidden
            className={`absolute inset-0 rounded-3xl blur-xl ${
              fraud ? "bg-amber-400/30" : "bg-emerald-400/30"
            }`}
            animate={reduce ? undefined : { scale: [1, 1.25, 1], opacity: [0.6, 0.3, 0.6] }}
            transition={{ duration: 2.4, repeat: Number.POSITIVE_INFINITY, ease: "easeInOut" }}
          />
          <div
            className={`relative flex h-20 w-20 items-center justify-center rounded-3xl border bg-gradient-to-br shadow-[0_0_40px_-8px] ${
              fraud
                ? "border-amber-400/40 from-amber-400/20 to-amber-600/10 text-amber-400 shadow-amber-500/40"
                : "border-emerald-400/40 from-emerald-400/20 to-emerald-600/10 text-emerald-400 shadow-emerald-500/40"
            }`}
          >
            <Icon className="h-9 w-9" strokeWidth={1.6} />
          </div>
        </div>

        <div className="space-y-2">
          <div
            className={`flex items-center justify-center gap-1.5 text-xs font-medium uppercase tracking-[0.2em] ${
              fraud ? "text-amber-400" : "text-emerald-400"
            }`}
          >
            {!fraud && <Sparkles className="h-3.5 w-3.5" />}
            {t(fraud ? "dashboard.welcomeBonus.fraud.eyebrow" : "dashboard.welcomeBonus.eyebrow")}
          </div>
          <h2 className="font-display text-2xl font-medium tracking-tight text-foreground">
            {t(fraud ? "dashboard.welcomeBonus.fraud.title" : "dashboard.welcomeBonus.title")}
          </h2>
        </div>

        {/* the credited amount — only in the celebratory mode */}
        {!fraud && (
          <div className="font-display text-[64px] font-semibold leading-none tracking-tight text-emerald-400 drop-shadow-[0_0_24px_rgba(16,185,129,0.35)]">
            +<CountUp value={amount} durationMs={1000} format={(n) => fmtUSD(n)} />
          </div>
        )}

        <p className="max-w-xs text-sm leading-relaxed text-muted-foreground">
          {t(fraud ? "dashboard.welcomeBonus.fraud.sub" : "dashboard.welcomeBonus.sub")}
        </p>

        <Button
          size="lg"
          className={`mt-2 px-8 text-white ${
            fraud ? "bg-amber-500 hover:bg-amber-600" : "bg-emerald-500 hover:bg-emerald-600"
          }`}
          onClick={onDismiss}
        >
          {t(fraud ? "dashboard.welcomeBonus.fraud.cta" : "dashboard.welcomeBonus.cta")}
        </Button>
      </motion.div>
    </motion.div>
  );
}
