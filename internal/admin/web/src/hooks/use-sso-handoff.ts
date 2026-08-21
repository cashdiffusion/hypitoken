import { useCallback, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { apiPost } from "@/lib/api";
import { type SsoCodeResp, ssoRedirectURL } from "@/lib/sso";

// useSsoHandoff completes an authenticated session by handing it to the
// sibling product that sent the visitor here (hypihub docs/SPEC.md §12).
//
// `returnUrl` is the raw, unvalidated `?return=` value. It is passed to the
// server and nowhere else: the redirect is built from the URL the SERVER
// echoes back, so a bug here cannot turn into an open redirect. See lib/sso.ts.
//
// Deliberately failure-tolerant. Every reason the handoff can break — the
// origin isn't allowlisted (400), the network dropped, the endpoint 500s, the
// deployment simply doesn't have the SSO routes yet — ends the same way: a
// non-blocking toast and the caller's normal in-app navigation. A broken
// handoff must never strand a signed-in user on a dead login page.
export function useSsoHandoff(returnUrl: string) {
  const { t } = useTranslation();
  const [pending, setPending] = useState(false);
  // One code per visit. Both the "just logged in" path and the "already
  // authenticated" effect can fire for the same login, and StrictMode
  // double-invokes effects in dev; minting a second single-use code would
  // silently invalidate nothing but does put a spare credential in a log.
  const started = useRef(false);

  /**
   * finish hands the browser to `returnUrl` when one was requested, otherwise
   * (or on any failure) runs `fallback` — the caller's normal post-login
   * navigation. Resolves once the outcome is decided; when the handoff wins,
   * the page is already navigating away.
   */
  const finish = useCallback(
    async (fallback: () => void): Promise<void> => {
      if (!returnUrl) {
        fallback();
        return;
      }
      // A handoff is already in flight (or the browser is leaving): do not
      // navigate on top of it.
      if (started.current) return;
      started.current = true;
      setPending(true);
      try {
        const resp = await apiPost<SsoCodeResp>("/auth/sso/code", { return_url: returnUrl });
        window.location.replace(ssoRedirectURL(resp));
      } catch {
        started.current = false;
        setPending(false);
        // Message is intentionally the same whatever went wrong: whether an
        // origin is allowlisted is not something a login page should confirm.
        toast.error(t("auth.sso.failed"));
        fallback();
      }
    },
    [returnUrl, t],
  );

  return { finish, pending };
}
