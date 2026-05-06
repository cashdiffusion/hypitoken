// react-i18next bootstrap. We keep the dictionary in src/i18n/locales
// — one file per language to keep diffs clean — and lazy-load both at
// startup since the bundle is small enough.
//
// Default language is detected (localStorage → navigator) with fallback
// to English. Changes flow through `i18n.changeLanguage("zh"|"en")`
// which the LanguageToggle component wires to a button next to the
// theme switch.

import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";

import en from "./locales/en";
import zh from "./locales/zh";

export const SUPPORTED_LANGS = ["en", "zh"] as const;
export type SupportedLang = (typeof SUPPORTED_LANGS)[number];

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      en: { translation: en },
      zh: { translation: zh },
    },
    fallbackLng: "en",
    supportedLngs: SUPPORTED_LANGS as unknown as string[],
    nonExplicitSupportedLngs: true, // map zh-CN, zh-TW, etc → zh
    interpolation: { escapeValue: false }, // React already escapes
    detection: {
      order: ["localStorage", "navigator", "htmlTag"],
      lookupLocalStorage: "hypi.lang",
      caches: ["localStorage"],
    },
    returnNull: false,
  });

export default i18n;
