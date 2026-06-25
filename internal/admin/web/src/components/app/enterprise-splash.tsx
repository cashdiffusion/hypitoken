import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "@/hooks/use-auth";

// EnterpriseSplash — a once-per-session, personalized "company name" intro that
// plays when a member of an ENTERPRISE workspace lands on the dashboard. Pure
// CSS/motion (no R3F) so it's instant. Auto-dismisses; click/anykey to skip.
export function EnterpriseSplash() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const reduce = useReducedMotion();

  // First enterprise workspace the user belongs to (admin or member).
  const space = useMemo(
    () => (user?.workspaces || []).find((w) => w.type === "enterprise"),
    [user],
  );
  const seenKey = space ? `hypi.splash.${space.id}` : "";
  const [show, setShow] = useState(false);

  useEffect(() => {
    if (!space) return;
    if (sessionStorage.getItem(seenKey)) return;
    sessionStorage.setItem(seenKey, "1");
    setShow(true);
    const id = setTimeout(() => setShow(false), 2600);
    return () => clearTimeout(id);
  }, [space, seenKey]);

  if (!space) return null;

  const name = space.name;

  return (
    <AnimatePresence>
      {show && (
        <motion.div
          onClick={() => setShow(false)}
          className="fixed inset-0 z-[120] flex cursor-pointer flex-col items-center justify-center overflow-hidden bg-background"
          initial={{ opacity: 1 }}
          exit={{ opacity: 0, transition: { duration: 0.6, ease: "easeInOut" } }}
        >
          {/* layered aurora background */}
          <div
            aria-hidden
            className="pointer-events-none absolute inset-0"
            style={{
              background:
                "radial-gradient(60% 50% at 50% 35%, color-mix(in oklch, var(--primary) 24%, transparent), transparent 70%)," +
                "radial-gradient(50% 50% at 15% 100%, color-mix(in oklch, var(--info, var(--primary)) 16%, transparent), transparent 72%)," +
                "radial-gradient(45% 45% at 100% 0%, color-mix(in oklch, var(--primary) 14%, transparent), transparent 70%)",
            }}
          />
          {!reduce && (
            <motion.div
              aria-hidden
              className="pointer-events-none absolute left-1/2 top-1/2 h-[60vmax] w-[60vmax] -translate-x-1/2 -translate-y-1/2 rounded-full opacity-30 blur-3xl"
              style={{
                background:
                  "conic-gradient(from 0deg, color-mix(in oklch, var(--primary) 40%, transparent), transparent 40%, color-mix(in oklch, var(--primary) 24%, transparent) 75%, transparent)",
              }}
              animate={{ rotate: 360 }}
              transition={{ duration: 28, ease: "linear", repeat: Number.POSITIVE_INFINITY }}
            />
          )}

          <div className="relative z-10 px-6 text-center">
            <motion.div
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: 0.1, duration: 0.5 }}
              className="mb-4 font-mono text-xs uppercase tracking-[0.5em] text-primary/80"
            >
              {t("splash.eyebrow")}
            </motion.div>

            <motion.h1
              initial={{ opacity: 0, y: 26, filter: "blur(12px)" }}
              animate={{ opacity: 1, y: 0, filter: "blur(0px)" }}
              transition={{ delay: 0.25, duration: 0.7, ease: "easeOut" }}
              className="bg-gradient-to-br from-foreground via-foreground to-primary bg-clip-text font-display text-5xl font-bold tracking-tight text-transparent sm:text-7xl"
            >
              {name}
            </motion.h1>

            <motion.div
              initial={{ scaleX: 0 }}
              animate={{ scaleX: 1 }}
              transition={{ delay: 0.7, duration: 0.6, ease: "easeOut" }}
              className="mx-auto mt-5 h-px w-40 origin-center bg-gradient-to-r from-transparent via-primary to-transparent"
            />

            <motion.p
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              transition={{ delay: 0.9, duration: 0.6 }}
              className="mt-5 text-sm text-muted-foreground sm:text-base"
            >
              {t("splash.welcome")}
            </motion.p>
          </div>

          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ delay: 1.4, duration: 0.6 }}
            className="absolute bottom-8 text-[11px] uppercase tracking-widest text-muted-foreground/60"
          >
            {t("splash.skip")}
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
