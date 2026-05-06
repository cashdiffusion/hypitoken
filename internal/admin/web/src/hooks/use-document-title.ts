// useDocumentTitle keeps document.title in sync with whatever page is
// currently mounted. The base brand stays "HypiToken"; pages prepend a
// page-specific prefix using the i18n dictionary so language switches
// reflow the title bar too.
//
// Usage: `useDocumentTitle("billing.title")` inside a route component.

import { useEffect } from "react";
import { useTranslation } from "react-i18next";

const BRAND = "HypiToken";

export function useDocumentTitle(i18nKey?: string | null) {
  const { t, i18n } = useTranslation();
  useEffect(() => {
    if (!i18nKey) {
      document.title = BRAND;
      return;
    }
    const label = t(i18nKey);
    document.title = label ? `${label} · ${BRAND}` : BRAND;
  }, [i18nKey, t, i18n.resolvedLanguage, i18n.language]);
}
