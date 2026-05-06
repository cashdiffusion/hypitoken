// LanguageToggle is a paired companion to ThemeToggle. Two-state button
// — flips i18n between en and zh, persists to localStorage via the
// detector, and updates the document lang attribute so screen readers
// pick up the change.

import { Globe } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { useEffect } from "react";

export function LanguageToggle() {
  const { i18n, t } = useTranslation();
  const current = (i18n.resolvedLanguage || i18n.language || "en").startsWith("zh") ? "zh" : "en";

  // Keep <html lang> in sync. Some screen readers and the system spell
  // checker key off this; harmless to set on every render.
  useEffect(() => {
    document.documentElement.lang = current === "zh" ? "zh-CN" : "en";
  }, [current]);

  const next = current === "zh" ? "en" : "zh";
  const label = current === "zh" ? "中" : "EN";

  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={() => i18n.changeLanguage(next)}
      title={t("lang.toggle")}
      aria-label={t("lang.toggle")}
      className="relative"
    >
      <Globe className="h-4 w-4" />
      <span className="absolute -bottom-0.5 -right-0.5 rounded-sm bg-background px-1 text-[9px] font-mono font-semibold leading-tight">
        {label}
      </span>
    </Button>
  );
}
