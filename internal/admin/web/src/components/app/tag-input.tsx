import { X } from "lucide-react";
import { type KeyboardEvent, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";

/* TagInput — free-form labels on a key ("研发部", "前端").
 *
 * These are what make the team spend report answer "what did each department
 * cost", so the input has to be frictionless: type, Enter, done. Backspace on an
 * empty field removes the last chip (the convention every tag field uses).
 *
 * Bounds mirror db.SanitizeTags on the server, which is the real enforcement —
 * these just stop the user hitting a wall they can't see. */
const MAX_TAGS = 8;
const MAX_LEN = 32;

export function TagInput({
  value,
  onChange,
  suggestions = [],
}: {
  value: string[];
  onChange: (tags: string[]) => void;
  /** Labels already in use elsewhere, offered as one-click adds. */
  suggestions?: string[];
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState("");

  const add = (raw: string) => {
    const tag = raw.trim().slice(0, MAX_LEN);
    if (!tag || value.includes(tag) || value.length >= MAX_TAGS) return;
    onChange([...value, tag]);
    setDraft("");
  };

  const remove = (tag: string) => onChange(value.filter((v) => v !== tag));

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      add(draft);
      return;
    }
    if (e.key === "Backspace" && draft === "" && value.length > 0) {
      remove(value[value.length - 1]);
    }
  };

  const unused = suggestions.filter((s) => !value.includes(s)).slice(0, 6);

  return (
    <div className="space-y-2">
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {value.map((tag) => (
            <Badge key={tag} variant="secondary" className="gap-1 pr-1">
              {tag}
              <button
                type="button"
                onClick={() => remove(tag)}
                className="rounded-full p-0.5 hover:bg-foreground/10"
                aria-label={t("common.delete")}
              >
                <X className="h-3 w-3" />
              </button>
            </Badge>
          ))}
        </div>
      )}
      <Input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKeyDown}
        onBlur={() => add(draft)}
        maxLength={MAX_LEN}
        disabled={value.length >= MAX_TAGS}
        placeholder={
          value.length >= MAX_TAGS
            ? t("tokens.tags.full", { n: MAX_TAGS })
            : t("tokens.tags.placeholder")
        }
      />
      {unused.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-xs text-muted-foreground">{t("tokens.tags.suggestions")}</span>
          {unused.map((s) => (
            <button key={s} type="button" onClick={() => add(s)}>
              <Badge variant="outline" className="cursor-pointer text-xs hover:bg-muted">
                + {s}
              </Badge>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
