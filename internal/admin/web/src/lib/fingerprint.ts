// Browser fingerprint (client side). Computes a stable device hash via
// ThumbmarkJS (MIT, fully client-side — no paid API) for two purposes:
//
//   1. signup anti-abuse — the backend (internal/saas/growth/fraud.go) withholds
//      the welcome bonus from a device that already registered;
//   2. behavioural attribution — the hash rides along with /track visit pings so
//      a visitor is recognisable across sessions even after localStorage clears.
//
// Everything here is best-effort and defensive: a fingerprint failure or a slow
// device must NEVER block registration. On any error/timeout we resolve to "" —
// the backend then degrades to IP-only signals.

const TIMEOUT_MS = 2500;
const CACHE_KEY = "hypi.fp"; // sessionStorage: stable within a tab session

// In-memory promise so concurrent callers share one computation per page load.
let pending: Promise<string> | null = null;

// getFingerprint returns the device fingerprint hash, or "" if it can't be
// computed in time. The result is memoised in-memory and in sessionStorage so
// repeated calls (visit ping + register) are cheap and consistent.
export function getFingerprint(): Promise<string> {
  try {
    const cached = sessionStorage.getItem(CACHE_KEY);
    if (cached) return Promise.resolve(cached);
  } catch {
    // sessionStorage unavailable (private mode / blocked) — fall through.
  }
  if (pending) return pending;

  pending = compute()
    .then((fp) => {
      if (fp) {
        try {
          sessionStorage.setItem(CACHE_KEY, fp);
        } catch {
          // ignore storage write failures
        }
      }
      return fp;
    })
    .catch(() => "");
  return pending;
}

async function compute(): Promise<string> {
  // Lazy-load so the ~20kB library (canvas/audio/webgl probes) is only pulled
  // when a visitor actually reaches an attribution/registration surface.
  const mod = await import("@thumbmarkjs/thumbmarkjs");
  try {
    mod.setOption("timeout", TIMEOUT_MS);
    mod.setOption("logging", false);
  } catch {
    // older/newer API shape — ignore, the race below still bounds it.
  }
  // Race the library against our own ceiling so a hung probe can't stall the
  // signup flow even if the library's internal timeout misbehaves.
  const fp = await Promise.race<string>([
    mod.getFingerprint(),
    new Promise<string>((resolve) => {
      window.setTimeout(() => resolve(""), TIMEOUT_MS + 300);
    }),
  ]);
  return typeof fp === "string" ? fp : "";
}
