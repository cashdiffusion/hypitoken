// Cross-site single sign-on handoff (hypihub docs/SPEC.md §12).
//
// HypiHub runs on its own origin (hub.novadiffusion.com), so it cannot read the
// JWT this app keeps in localStorage. Instead it sends the browser here with
// `?return=<its /sso url>`; once the visitor is authenticated we mint a
// one-time code and hand the browser back with `?code=…`, which HypiHub trades
// for its own copy of the session server-to-server.
//
// SECURITY — the whole reason this file is so small:
//
//   * The SERVER is the only authority on whether a return_url may be used.
//     Nothing here inspects the origin to decide anything. The allowlist lives
//     in saas.sso_return_origins and is checked by POST /auth/sso/code.
//   * The redirect is built ONLY from the `return_url` the server echoed back
//     in its response — never from the raw query parameter. So even if this
//     module were handed `javascript:…` or an attacker's domain, it cannot
//     become an open redirect unless the server itself blessed that URL.
//
// The one thing the client does read the origin for is cosmetic: naming the
// destination product in the login heading. That decision moves no session.

export const SSO_RETURN_PARAM = "return";

// Origins we can NAME in the UI, mapped to their i18n key. Cosmetic only — a
// miss here means the heading stays generic, never that the handoff is
// refused (and a hit means nothing to the server, which re-checks anyway).
const KNOWN_RETURN_PRODUCTS: Record<string, string> = {
  "https://hub.novadiffusion.com": "auth.sso.hypihub",
};

/** readSsoReturn pulls the raw, unvalidated `?return=` value out of a query string. */
export function readSsoReturn(search: string): string {
  const raw = new URLSearchParams(search).get(SSO_RETURN_PARAM);
  return raw ? raw.trim() : "";
}

/**
 * ssoProductKey returns the i18n key naming the product behind `returnUrl`, or
 * "" when we don't recognise it. Display only — see the security note above.
 */
export function ssoProductKey(returnUrl: string): string {
  if (!returnUrl) return "";
  try {
    return KNOWN_RETURN_PRODUCTS[new URL(returnUrl, window.location.origin).origin] ?? "";
  } catch {
    // A `?return=` that isn't even a URL simply has no name to show. The
    // server will reject it in a moment; that is not this function's problem.
    return "";
  }
}

/** withSsoReturn appends the current `?return=` to an in-app path, if there is one. */
export function withSsoReturn(path: string, returnUrl: string): string {
  if (!returnUrl) return path;
  return `${path}${path.includes("?") ? "&" : "?"}${SSO_RETURN_PARAM}=${encodeURIComponent(returnUrl)}`;
}

/** Response shape of POST /api/v2/auth/sso/code (SPEC §12). */
export interface SsoCodeResp {
  code: string;
  expires_in: number;
  return_url: string;
}

/**
 * ssoRedirectURL glues the one-time code onto the server-echoed return URL.
 * Exported for the handoff hook only; both inputs must come from the response
 * body, never from the address bar.
 *
 * Built through the URL API rather than string concatenation, and that is a
 * security property, not a tidiness one. `return_url` is attacker-influenced
 * (the allowlist pins its ORIGIN, never its query string), so `?code=` glued
 * on the end of `https://hub.example.com/sso?code=<attacker's own code>` would
 * arrive as `?code=attacker&code=victim` — and `URLSearchParams.get("code")`
 * on the far side returns the FIRST one. That is a login-CSRF: the victim's
 * browser ends up signed into the sibling product as the attacker, spending
 * the attacker's wallet and depositing work in the attacker's account.
 * `searchParams.set` REPLACES every existing `code`, so the real one always
 * wins. It also places the parameter in the query even when the URL carries a
 * fragment, where naive concatenation would have hidden the code after `#`
 * and silently broken the handoff.
 *
 * Throws on a malformed destination; the caller treats any throw as "handoff
 * failed" and falls back to normal in-app navigation.
 */
export function ssoRedirectURL(resp: SsoCodeResp | null): string {
  const dest = (resp?.return_url || "").trim();
  const code = (resp?.code || "").trim();
  if (!dest || !code) throw new Error("sso: malformed code response");
  const u = new URL(dest);
  u.searchParams.set("code", code);
  return u.toString();
}
