// AdminBackdrop — static ambient scenery behind /app/admin. Pure CSS (no
// WebGL: R3F has crashed pages without it, and the panel re-renders on a 15s
// poll, so this must stay free). Composited once, fixed to the viewport,
// pointer-transparent. Content contrast wins every trade-off here: tables and
// small mono text sit directly on top, so every layer keeps peak alpha low
// and the dot grid is masked away from the viewport center. The only motion
// is two ≥48s translate-only orbs (`admin-orbit` in globals.css), frozen
// under prefers-reduced-motion — the entrance animations were deliberately
// calmed, and the backdrop must not reintroduce busyness.
export function AdminBackdrop() {
  return (
    // z-0, not negative: an ancestor (body > div, z-index:1) establishes the
    // stacking context, so a negative-z child would paint *below* the shell's
    // opaque .bg-background wrapper and be fully covered. At z-0 the backdrop
    // sits above that background; admin.tsx lifts the page content to z-10.
    <div aria-hidden className="pointer-events-none fixed inset-0 z-0 overflow-hidden">
      {/* Corner glows — complements the shell's two top glows with a low
          bottom-left anchor so the admin canvas reads framed, not floodlit. */}
      <div
        className="absolute inset-0"
        style={{
          backgroundImage:
            "radial-gradient(42% 38% at 0% 100%, color-mix(in oklch, var(--info) 7%, transparent), transparent 70%)," +
            "radial-gradient(38% 34% at 100% 96%, color-mix(in oklch, var(--primary) 5%, transparent), transparent 72%)",
        }}
      />
      {/* Dot grid, masked to the viewport edges so data tables at center stay
          on clean ground. Dots derive from --foreground so both themes get
          the right polarity without lifting dark-mode blacks. */}
      <div
        className="absolute inset-0"
        style={{
          backgroundImage:
            "radial-gradient(circle, color-mix(in oklch, var(--foreground) 14%, transparent) 1px, transparent 1px)",
          backgroundSize: "26px 26px",
          maskImage: "radial-gradient(ellipse 72% 64% at 50% 42%, transparent 42%, black 96%)",
          WebkitMaskImage:
            "radial-gradient(ellipse 72% 64% at 50% 42%, transparent 42%, black 96%)",
          opacity: 0.5,
        }}
      />
      {/* Drifting orbs — soft-edged via the gradient itself (no filter, so no
          per-frame raster work); translate-only keyframes keep them on the
          compositor. */}
      <div
        className="admin-orb absolute -left-40 top-[16%] size-[34rem] rounded-full"
        style={{
          background:
            "radial-gradient(circle, color-mix(in oklch, var(--primary) 7%, transparent), transparent 68%)",
          animationDuration: "52s",
        }}
      />
      <div
        className="admin-orb absolute -right-48 bottom-[8%] size-[38rem] rounded-full"
        style={{
          background:
            "radial-gradient(circle, color-mix(in oklch, var(--info) 6%, transparent), transparent 68%)",
          animationDuration: "68s",
          animationDelay: "-26s",
        }}
      />
      {/* Top edge glow — a hairline that ties the fixed backdrop to the nav. */}
      <div
        className="absolute inset-x-0 top-0 h-px"
        style={{
          background:
            "linear-gradient(90deg, transparent 8%, color-mix(in oklch, var(--primary) 34%, transparent) 50%, transparent 92%)",
        }}
      />
    </div>
  );
}
