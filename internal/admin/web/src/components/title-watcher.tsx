// TitleWatcher keeps document.title in sync with the current route. One
// effect at the root of <Routes> so we don't have to sprinkle
// useDocumentTitle into every page. Reads the language from i18next so
// flipping the LanguageToggle reflows the title bar without a reload.

import { useEffect } from "react";
import { useLocation } from "react-router-dom";
import { useTranslation } from "react-i18next";

const BRAND = "HypiToken";

// matchKey returns the i18n key whose translated value should prefix
// "HypiToken" in the title bar. Order matters — `/app/admin/*` must
// match before plain `/app/admin`.
function matchKey(pathname: string): string | null {
  const p = pathname.replace(/\/+$/, "") || "/";
  if (p === "/") return null;
  if (p === "/login") return "auth.login.title";
  if (p === "/register") return "auth.register.title";
  if (p === "/forgot-password") return "auth.forgot.title";
  if (p === "/pricing") return "pricing.pageTitle";
  if (p === "/status") return "status.title";
  if (p === "/docs" || p.startsWith("/docs/")) return "nav.documentation";
  if (p === "/app") return "nav.dashboard";
  if (p === "/app/tokens") return "tokens.title";
  if (p === "/app/billing") return "billing.title";
  if (p === "/app/logs") return "logs.title";
  if (p === "/app/console") return "console.title";
  if (p.startsWith("/app/admin")) return "admin.panelTitle";
  return null;
}

export function TitleWatcher() {
  const { pathname } = useLocation();
  const { t, i18n } = useTranslation();
  useEffect(() => {
    const key = matchKey(pathname);
    if (!key) {
      document.title = BRAND;
      return;
    }
    const label = t(key);
    document.title = label ? `${label} · ${BRAND}` : BRAND;
  }, [pathname, t, i18n.resolvedLanguage, i18n.language]);
  return null;
}
