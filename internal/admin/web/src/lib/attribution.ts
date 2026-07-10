// Marketing-channel attribution (client side). Self-contained: capture the
// ?ref=<channel> a visitor lands with, remember it first-touch in localStorage,
// beacon anonymous visit + dwell-time pings to the public /track endpoints, and
// expose the stored referral so the register form can attach it.
//
// Pairs with the backend internal/saas/growth module. Everything here is
// best-effort and wrapped so a tracking failure can never break the page.

import { getFingerprint } from "./fingerprint";

const REF_KEY = "hypi.ref"; // channel slug (first-touch)
const VID_KEY = "hypi.ref_vid"; // anonymous visitor id
const TS_KEY = "hypi.ref_ts"; // first-touch epoch ms

const TRACK_VISIT = "/api/v2/track/visit";
const TRACK_PING = "/api/v2/track/ping";

// Must mirror the server's slug rule (growth.NormalizeSlug): lowercase,
// alnum/-/_ , 1–31 chars. Anything else is ignored rather than stored.
const SLUG_RE = /^[a-z0-9][a-z0-9_-]{0,30}$/;

const HEARTBEAT_MS = 15_000; // dwell ping cadence while the tab is visible

export interface Referral {
  ref: string;
  vid: string;
}

function normalizeSlug(raw: string | null): string {
  if (!raw) return "";
  const s = raw.trim().toLowerCase();
  return SLUG_RE.test(s) ? s : "";
}

function newVisitorID(): string {
  try {
    if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
      return crypto.randomUUID();
    }
  } catch {
    // fall through to the math fallback
  }
  return `v_${Math.random().toString(36).slice(2)}${Date.now().toString(36)}`;
}

// getReferral returns the stored first-touch referral, or null if the visitor
// never arrived through a channel link.
export function getReferral(): Referral | null {
  try {
    const ref = normalizeSlug(localStorage.getItem(REF_KEY));
    const vid = localStorage.getItem(VID_KEY) || "";
    if (!ref || !vid) return null;
    return { ref, vid };
  } catch {
    return null;
  }
}

// clearReferral wipes the stored attribution. Called after a successful signup
// so the credit is spent exactly once and later visits start fresh.
export function clearReferral(): void {
  try {
    localStorage.removeItem(REF_KEY);
    localStorage.removeItem(VID_KEY);
    localStorage.removeItem(TS_KEY);
  } catch {
    // ignore — storage may be unavailable (private mode); nothing to clean up
  }
}

// post sends a small JSON beacon. keepalive lets it survive a navigation; on
// page-hide we prefer sendBeacon (see scheduleDwellFlush).
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

let dwellStarted = false;

// initAttribution runs once at app startup. It captures a fresh ?ref= (first
// touch wins), records the visit, and — whenever a referral is active — starts
// dwell-time tracking. Safe to call on every mount; guards make it idempotent.
export function initAttribution(): void {
  try {
    captureFromURL();
  } catch {
    // ignore
  }
  const r = getReferral();
  if (!r) return;
  // Attach the device fingerprint (best-effort, async) so the visit carries a
  // cross-session stable id; resolves to "" on failure without blocking.
  void getFingerprint().then((fp) => post(TRACK_VISIT, { ref: r.ref, vid: r.vid, fp }));
  startDwellTracking(r);
}

// captureFromURL reads ?ref= from the current location. First-touch semantics:
// if a referral is already stored we keep it. On a new capture we mint a
// visitor id and strip the param from the URL so it isn't bookmarked or shared.
function captureFromURL(): void {
  const params = new URLSearchParams(window.location.search);
  const slug = normalizeSlug(params.get("ref"));
  if (!slug) return;

  const existing = getReferral();
  if (!existing) {
    localStorage.setItem(REF_KEY, slug);
    localStorage.setItem(VID_KEY, newVisitorID());
    localStorage.setItem(TS_KEY, String(Date.now()));
  }

  // Clean the URL regardless, so a refresh doesn't keep re-presenting ?ref=.
  params.delete("ref");
  const qs = params.toString();
  const clean = window.location.pathname + (qs ? `?${qs}` : "") + window.location.hash;
  // BrowserRouter stores its navigation index and location key in
  // history.state. Preserve that bookkeeping while removing only ?ref=.
  window.history.replaceState(window.history.state, "", clean);
}

// startDwellTracking pings accumulated on-page time periodically and once more
// on page-hide (the reliable "session end" signal on mobile + desktop).
function startDwellTracking(r: Referral): void {
  if (dwellStarted) return;
  dwellStarted = true;
  const start = Date.now();
  const elapsed = () => Date.now() - start;

  const beat = window.setInterval(() => {
    if (document.visibilityState === "visible") {
      post(TRACK_PING, { ref: r.ref, vid: r.vid, ms: elapsed() });
    }
  }, HEARTBEAT_MS);

  const flush = () => {
    const body = JSON.stringify({ ref: r.ref, vid: r.vid, ms: elapsed() });
    try {
      if (navigator.sendBeacon) {
        navigator.sendBeacon(TRACK_PING, new Blob([body], { type: "application/json" }));
        return;
      }
    } catch {
      // fall through to fetch
    }
    post(TRACK_PING, { ref: r.ref, vid: r.vid, ms: elapsed() });
  };

  // pagehide fires on real unload + bfcache; visibilitychange→hidden covers
  // tab switches and mobile backgrounding.
  window.addEventListener("pagehide", flush);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") flush();
  });
  // Stop the heartbeat when the page is torn down to avoid a dangling timer in
  // bfcache-restored pages.
  window.addEventListener("pagehide", () => window.clearInterval(beat));
}
