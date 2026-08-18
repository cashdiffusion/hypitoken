// Site-wide visitor-behaviour tracking (client side). Captures EVERY visitor: a
// pageview on first load and on each SPA route change, explicit CTA clicks (via
// a delegated data-track listener), and total dwell time beaconed on page hide.
//
// Pairs with the backend internal/saas/analytics module. Everything here is
// best-effort and wrapped so a tracking failure can never break the page. It
// still reuses the legacy channel-attribution visitor id when a browser already
// carries one, so a returning visitor isn't counted twice.

const AN_VID_KEY = "hypi.an_vid"; // our own visitor id (the default)
const REF_VID_KEY = "hypi.ref_vid"; // legacy attribution visitor id — reused when already stored
const SID_KEY = "hypi.an_sid"; // per-tab session id (sessionStorage: new tab/session = new id)

const TRACK_EVENT = "/api/v2/track/event";
const TRACK_DWELL = "/api/v2/track/dwell";

const HEARTBEAT_MS = 15_000; // dwell ping cadence while the tab is visible

let sessionID = "";
let visitorID = "";
let dwellStarted = false;
// document.referrer is only meaningful for the landing event (it becomes the
// session's source); captured once at init and cleared so later pageviews don't
// re-send a stale value.
let landingReferrer = "";
let landingPath = "";

function randID(prefix: string): string {
  try {
    if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
      return crypto.randomUUID();
    }
  } catch {
    // fall through to the math fallback
  }
  return `${prefix}${Math.random().toString(36).slice(2)}${Date.now().toString(36)}`;
}

// visitor id is stable across sessions (localStorage). Prefer a legacy
// attribution id when one is already stored so a returning browser keeps its
// identity instead of being counted as a brand-new visitor.
function getVisitorID(): string {
  try {
    const ref = localStorage.getItem(REF_VID_KEY);
    if (ref) return ref;
    let v = localStorage.getItem(AN_VID_KEY);
    if (!v) {
      v = randID("a_");
      localStorage.setItem(AN_VID_KEY, v);
    }
    return v;
  } catch {
    // storage unavailable (private mode) — fall back to an ephemeral id
    return randID("a_");
  }
}

// session id is per tab session (sessionStorage). Reused across route changes
// within the tab; a fresh tab or a closed-and-reopened one starts a new session.
function getSessionID(): string {
  try {
    let s = sessionStorage.getItem(SID_KEY);
    if (!s) {
      s = randID("s_");
      sessionStorage.setItem(SID_KEY, s);
    }
    return s;
  } catch {
    return randID("s_");
  }
}

// pathToPage maps a router pathname to a short, low-cardinality page label so
// the flow/path analytics group cleanly (e.g. /docs/self-host → "docs"). Unknown
// app paths collapse to their first segment.
export function pathToPage(pathname: string): string {
  const p = (pathname || "/").split("?")[0].split("#")[0];
  if (p === "/" || p === "") return "home";
  const seg = p.replace(/^\/+/, "").split("/");
  switch (seg[0]) {
    case "login":
    case "register":
    case "forgot-password":
    case "pricing":
    case "status":
      return seg[0];
    case "docs":
      return "docs";
    case "app":
      return seg[1] ? `app:${seg[1]}` : "app";
    default:
      return seg[0].slice(0, 32);
  }
}

// post sends a small JSON beacon. keepalive lets it survive a navigation.
function post(url: string, body: Record<string, unknown>): void {
  try {
    void fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      keepalive: true,
    }).catch(() => {});
  } catch {
    // ignore — tracking is best-effort
  }
}

// trackPageview records a pageview for the given page label. The first call of a
// session also carries the landing path + referrer, which the server uses to
// stamp the session's landing page and acquisition source (later calls ignore
// them). Call on every SPA route change.
export function trackPageview(page: string): void {
  if (!sessionID) return; // init not run yet
  // Skip the authenticated /app/* area: this is the acquisition funnel, so a
  // logged-in user (or operator) clicking around the console shouldn't register
  // as a homepage visitor / first action. pathToPage maps /app/* → "app[:…]".
  if (page === "app" || page.startsWith("app:")) return;
  post(TRACK_EVENT, {
    sid: sessionID,
    vid: visitorID,
    kind: "pageview",
    name: page,
    path: landingPath,
    referrer: landingReferrer,
  });
}

// trackAction records an explicit interaction (a CTA click). The first action of
// a session becomes its first_action server-side; later ones just enrich the
// flow log. Fired by the delegated data-track listener and callable directly.
export function trackAction(name: string): void {
  if (!sessionID || !name) return;
  post(TRACK_EVENT, { sid: sessionID, vid: visitorID, kind: "action", name });
}

// initWebAnalytics runs once at app startup (alongside initAttribution). It
// resolves the visitor/session ids, records the landing pageview, starts dwell
// tracking, and installs the delegated data-track click listener. Idempotent.
export function initWebAnalytics(): void {
  if (dwellStarted) return;
  dwellStarted = true;
  try {
    visitorID = getVisitorID();
    sessionID = getSessionID();
    landingReferrer = document.referrer || "";
    landingPath = window.location.pathname || "/";

    trackPageview(pathToPage(landingPath));
    startDwellTracking();
    installActionListener();
  } catch {
    // ignore — never let tracking break the page
  }
}

// installActionListener autocaptures clicks on any element carrying a
// data-track="<name>" attribute (or nested inside one). This is the standard
// lightweight pattern — mark a CTA with the attribute and it reports itself, no
// per-component prop threading.
function installActionListener(): void {
  document.addEventListener(
    "click",
    (e) => {
      try {
        const start = e.target as Element | null;
        const el = start?.closest?.("[data-track]");
        const name = el?.getAttribute("data-track");
        if (name) trackAction(name);
      } catch {
        // ignore
      }
    },
    { capture: true },
  );
}

// startDwellTracking pings accumulated on-page time periodically and once more
// on page-hide — visibilitychange→hidden and pagehide are the reliable
// "session end" signals on mobile and desktop.
function startDwellTracking(): void {
  const start = Date.now();
  const elapsed = () => Date.now() - start;

  const beat = window.setInterval(() => {
    if (document.visibilityState === "visible") {
      post(TRACK_DWELL, { sid: sessionID, vid: visitorID, ms: elapsed() });
    }
  }, HEARTBEAT_MS);

  const flush = () => {
    const body = JSON.stringify({ sid: sessionID, vid: visitorID, ms: elapsed() });
    try {
      if (navigator.sendBeacon) {
        navigator.sendBeacon(TRACK_DWELL, new Blob([body], { type: "application/json" }));
        return;
      }
    } catch {
      // fall through to fetch
    }
    post(TRACK_DWELL, { sid: sessionID, vid: visitorID, ms: elapsed() });
  };

  window.addEventListener("pagehide", flush);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") flush();
  });
  window.addEventListener("pagehide", () => window.clearInterval(beat));
}
