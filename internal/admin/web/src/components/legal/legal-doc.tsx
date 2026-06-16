import { useTranslation } from "react-i18next";
import { Reveal } from "@/components/landing/reveal";

// LEGAL_LAST_UPDATED — bump whenever the Terms/Privacy copy changes so the
// "last updated" line stays honest. Single source for both documents.
export const LEGAL_LAST_UPDATED = "2026-06-16";

interface LegalSection {
  h: string;
  b: string[];
}

// LegalDoc renders a structured legal document (Terms or Privacy) from i18n.
// The copy lives under `legal.<docKey>.sections` as an array of {h, b[]} so it
// stays fully translatable (zh/en) without hard-coding prose in the component.
export function LegalDoc({ docKey }: { docKey: "terms" | "privacy" }) {
  const { t } = useTranslation();
  const sections = t(`legal.${docKey}.sections`, { returnObjects: true }) as LegalSection[];
  return (
    <div className="relative overflow-hidden">
      <div className="mx-auto max-w-3xl px-4 py-16 md:px-6 md:py-24">
        <Reveal>
          <span className="eyebrow text-primary">{t("legal.eyebrow")}</span>
          <h1 className="mt-3 font-display text-4xl font-semibold tracking-tight md:text-5xl">
            {t(`legal.${docKey}.title`)}
          </h1>
          <p className="mt-4 max-w-2xl text-base text-muted-foreground">
            {t(`legal.${docKey}.intro`)}
          </p>
          <p className="mt-3 text-xs font-mono uppercase tracking-wider text-muted-foreground/70">
            {t("legal.lastUpdated", { date: LEGAL_LAST_UPDATED })}
          </p>
        </Reveal>
        <div className="mt-12 space-y-9">
          {Array.isArray(sections) &&
            sections.map((s) => (
              <Reveal key={s.h}>
                <section>
                  <h2 className="font-display text-lg font-semibold tracking-tight md:text-xl">
                    {s.h}
                  </h2>
                  {s.b.map((p) => (
                    <p key={p} className="mt-3 text-sm leading-relaxed text-muted-foreground">
                      {p}
                    </p>
                  ))}
                </section>
              </Reveal>
            ))}
        </div>
      </div>
    </div>
  );
}
