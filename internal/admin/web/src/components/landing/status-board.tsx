import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

// Live status-page visual for the "health dashboard" feature card. Reads like a
// real status page: each upstream credential shows its health, latest model
// probe latency, and per-credential usage. A timer simulates a hard failure on
// the active credential and rotates traffic to the next healthy one — the exact
// behavior the copy describes. Distinct medium (DOM rows, not the SVG routing
// diagram) so it doesn't echo the architecture section. Static under
// reduced-motion: the resting snapshot still reads as a status page.

interface Row {
  id: string;
  kind: "oauth" | "apikey";
  model: string;
  base: number; // baseline probe latency (ms)
  usage: number; // 0..1
}

const ROWS: Row[] = [
  { id: "oauth · max-7f3a", kind: "oauth", model: "haiku", base: 142, usage: 0.62 },
  { id: "oauth · pro-1b9c", kind: "oauth", model: "haiku", base: 168, usage: 0.41 },
  { id: "apikey · sk-•2c", kind: "apikey", model: "gpt-5.5", base: 96, usage: 0.78 },
  { id: "oauth · team-d4", kind: "oauth", model: "haiku", base: 211, usage: 0.27 },
];

const CYCLE_MS = 2600;

export function StatusBoard() {
  const { t } = useTranslation();
  const reduce = useReducedMotion();

  // active = credential currently serving the sticky session.
  const [active, setActive] = useState(0);
  // down = credential that just hard-failed (recovers next cycle).
  const [down, setDown] = useState<number | null>(null);
  // jitter applied to probe latencies so the numbers feel live.
  const [tick, setTick] = useState(0);
  const activeRef = useRef(active);
  activeRef.current = active;

  useEffect(() => {
    if (reduce) return;
    const probe = setInterval(() => setTick((n) => n + 1), 1300);
    const rotate = setInterval(() => {
      // hard-fail the active credential, rotate to the next healthy one.
      const failing = activeRef.current;
      let next = (failing + 1) % ROWS.length;
      if (next === failing) next = (next + 1) % ROWS.length;
      setDown(failing);
      setActive(next);
      // recover the failed credential shortly after it rotates out.
      setTimeout(() => setDown((d) => (d === failing ? null : d)), CYCLE_MS - 700);
    }, CYCLE_MS);
    return () => {
      clearInterval(probe);
      clearInterval(rotate);
    };
  }, [reduce]);

  const latency = (r: Row, i: number) => {
    if (down === i) return null;
    const wobble = reduce
      ? 0
      : Math.round(Math.sin(tick * 1.7 + i) * 9 + (tick % 2 === 0 ? 4 : -3));
    return r.base + wobble;
  };

  return (
    <div className="relative overflow-hidden rounded-xl border border-border-strong bg-card/80 text-left shadow-xl">
      {/* header — status-page chrome */}
      <div className="flex items-center justify-between border-b border-border bg-muted/40 px-4 py-2.5">
        <span className="font-mono text-xs text-muted-foreground">{t("home.status.title")}</span>
        <span className="flex items-center gap-1.5 font-mono text-xs text-success">
          <span className="relative flex h-1.5 w-1.5">
            {!reduce && (
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success/70" />
            )}
            <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-success" />
          </span>
          {t("home.status.live")}
        </span>
      </div>

      {/* credential rows — horizontally scrollable on narrow phones so the
          rightmost status tag is never clipped; fills the card on desktop. */}
      <div className="overflow-x-auto">
        <div className="min-w-[420px] divide-y divide-border/70">
          {ROWS.map((r, i) => {
            const isDown = down === i;
            const isActive = active === i && !isDown;
            const ms = latency(r, i);
            return (
              <div
                key={r.id}
                className="relative flex items-center gap-3 px-4 py-3 font-mono text-[12.5px] transition-colors"
                style={
                  isActive
                    ? { background: "color-mix(in oklch, var(--color-primary) 7%, transparent)" }
                    : undefined
                }
              >
                {/* active rail */}
                <span
                  aria-hidden
                  className="absolute inset-y-0 left-0 w-0.5 transition-opacity"
                  style={{ background: "var(--color-primary)", opacity: isActive ? 1 : 0 }}
                />

                {/* health dot */}
                <span className="relative flex h-2 w-2 shrink-0">
                  {isActive && !reduce && (
                    <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary/60" />
                  )}
                  <span
                    className="relative inline-flex h-2 w-2 rounded-full"
                    style={{
                      background: isDown
                        ? "var(--color-destructive)"
                        : isActive
                          ? "var(--color-primary)"
                          : "var(--color-success)",
                    }}
                  />
                </span>

                {/* credential id */}
                <span className="w-[120px] shrink-0 truncate text-foreground/90">{r.id}</span>

                {/* model probe */}
                <span className="flex w-[112px] shrink-0 items-center gap-1.5">
                  <span className="text-muted-foreground">{r.model}</span>
                  <AnimatePresence mode="wait" initial={false}>
                    <motion.span
                      key={isDown ? "down" : ms}
                      initial={reduce ? false : { opacity: 0, y: -4 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={reduce ? undefined : { opacity: 0, y: 4 }}
                      transition={{ duration: 0.25 }}
                      className={isDown ? "text-destructive" : "text-info"}
                    >
                      {isDown ? t("home.status.fail") : `${ms}ms`}
                    </motion.span>
                  </AnimatePresence>
                </span>

                {/* per-credential usage */}
                <div className="flex flex-1 items-center gap-2">
                  <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                    <motion.div
                      className="h-full rounded-full"
                      style={{
                        background: r.usage > 0.7 ? "var(--color-warning)" : "var(--color-primary)",
                      }}
                      initial={reduce ? false : { width: 0 }}
                      animate={{ width: `${Math.round(r.usage * 100)}%` }}
                      transition={{ duration: 0.9, delay: i * 0.08, ease: "easeOut" }}
                    />
                  </div>
                  <span className="w-9 shrink-0 text-right text-muted-foreground">
                    {Math.round(r.usage * 100)}%
                  </span>
                </div>

                {/* status tag — keyed remount (no exit) so the pill swaps atomically
                  with the health dot, never lagging a frame behind a state flip */}
                <span className="w-[64px] shrink-0 text-right">
                  {isDown ? (
                    <motion.span
                      key="rot"
                      initial={reduce ? false : { opacity: 0, scale: 0.9 }}
                      animate={{ opacity: 1, scale: 1 }}
                      className="inline-flex items-center rounded-full bg-destructive/12 px-1.5 py-0.5 text-[10px] text-destructive"
                    >
                      {t("home.status.rotated")}
                    </motion.span>
                  ) : isActive ? (
                    <motion.span
                      key="act"
                      initial={reduce ? false : { opacity: 0 }}
                      animate={{ opacity: 1 }}
                      className="inline-flex items-center rounded-full bg-primary/12 px-1.5 py-0.5 text-[10px] text-primary"
                    >
                      {t("home.status.active")}
                    </motion.span>
                  ) : null}
                </span>
              </div>
            );
          })}
        </div>
      </div>

      {/* footer note */}
      <div className="border-t border-border bg-muted/20 px-4 py-2.5 font-mono text-[11px] text-muted-foreground">
        {t("home.status.foot")}
      </div>
    </div>
  );
}
