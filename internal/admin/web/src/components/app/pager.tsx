import { ChevronLeft, ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/* Pager — the shared offset/limit pagination footer for every list view.
 * Contract: the backing API takes ?limit=&offset= and returns { ..., total }.
 * The caller owns offset state and refetches in onChange; `busy` disables both
 * buttons while a page is in flight so double-clicks can't skip pages. */
export function Pager({
  offset,
  limit,
  total,
  count,
  busy,
  onChange,
  className,
}: {
  offset: number;
  limit: number;
  /** Backend total; when unknown (legacy endpoint), pass undefined and the
   * pager falls back to "full page ⇒ assume more". */
  total?: number;
  /** Items on the current page — drives the range label and the fallback. */
  count: number;
  busy?: boolean;
  onChange: (nextOffset: number) => void;
  className?: string;
}) {
  const { t } = useTranslation();
  const hasPrev = offset > 0;
  const hasNext = total !== undefined ? offset + count < total : count >= limit;
  if (!hasPrev && !hasNext && total === undefined) return null;
  return (
    <div
      className={cn(
        "flex items-center justify-between gap-3 text-xs text-muted-foreground",
        className,
      )}
    >
      <span className="tabular-nums">
        {count === 0
          ? t("common.pagerEmpty")
          : t("common.pagerRange", {
              from: (offset + 1).toLocaleString(),
              to: (offset + count).toLocaleString(),
              total: (total ?? offset + count).toLocaleString(),
            })}
      </span>
      <div className="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={!hasPrev || busy}
          onClick={() => onChange(Math.max(0, offset - limit))}
        >
          <ChevronLeft className="h-3.5 w-3.5" />
          {t("common.pagerPrev")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={!hasNext || busy}
          onClick={() => onChange(offset + limit)}
        >
          {t("common.pagerNext")}
          <ChevronRight className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  );
}
