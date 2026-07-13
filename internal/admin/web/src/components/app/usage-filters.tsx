import { Check, ChevronDown, Download, Loader2, RotateCcw } from "lucide-react";
import { motion } from "motion/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { apiDownload } from "@/lib/api";
import type { ModelSpend, TagSpend, TokenSpend } from "@/lib/types";
import { cn, errMsg, fmtUSD } from "@/lib/utils";

export type RangeKey = "7d" | "30d" | "90d" | "custom";

export interface UsageFilter {
  range: RangeKey;
  from: string; // YYYY-MM-DD
  to: string;
  tokenId?: number;
  model?: string;
  tag?: string;
}

const RANGES: { key: RangeKey; days: number }[] = [
  { key: "7d", days: 7 },
  { key: "30d", days: 30 },
  { key: "90d", days: 90 },
];

const iso = (d: Date) => d.toISOString().slice(0, 10);

/** Resolve a preset range key into concrete from/to dates. */
export function rangeDates(key: RangeKey): { from: string; to: string } {
  const to = new Date();
  const from = new Date();
  const days = RANGES.find((r) => r.key === key)?.days ?? 30;
  from.setDate(to.getDate() - (days - 1));
  return { from: iso(from), to: iso(to) };
}

export function defaultFilter(): UsageFilter {
  return { range: "30d", ...rangeDates("30d") };
}

export interface FilterBarProps {
  value: UsageFilter;
  onChange: (f: UsageFilter) => void;
  tokens: TokenSpend[];
  models: ModelSpend[];
  tags: TagSpend[];
  showMember?: boolean;
}

export function FilterBar({ value, onChange, tokens, models, tags, showMember }: FilterBarProps) {
  const { t } = useTranslation();
  const [tokenOpen, setTokenOpen] = useState(false);

  const dirty = value.range !== "30d" || value.tokenId != null || !!value.model || !!value.tag;
  const selected = tokens.find((k) => k.token_id === value.tokenId);

  const setRange = (key: RangeKey) => onChange({ ...value, range: key, ...rangeDates(key) });

  return (
    <div className="flex flex-wrap items-center gap-2">
      {/* range pills — layoutId gives the selection a single sliding highlight */}
      <div className="flex items-center gap-0.5 rounded-full bg-muted/60 p-0.5">
        {RANGES.map((r) => (
          <button
            key={r.key}
            type="button"
            onClick={() => setRange(r.key)}
            className={cn(
              "relative rounded-full px-3 py-1.5 text-sm font-medium transition-colors",
              value.range === r.key
                ? "text-primary-foreground"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {value.range === r.key && (
              <motion.span
                layoutId="usage-range-pill"
                className="absolute inset-0 rounded-full bg-primary"
                transition={{ type: "spring", stiffness: 380, damping: 30 }}
              />
            )}
            <span className="relative z-10">{t(`usage.filter.range${r.key}`)}</span>
          </button>
        ))}
      </div>

      {/* custom dates */}
      <div className="flex items-center gap-1.5">
        <Input
          type="date"
          value={value.from}
          max={value.to}
          onChange={(e) => onChange({ ...value, range: "custom", from: e.target.value })}
          className="h-9 w-[9.5rem] text-xs"
          aria-label={t("usage.filter.from")}
        />
        <span className="text-muted-foreground">–</span>
        <Input
          type="date"
          value={value.to}
          min={value.from}
          max={iso(new Date())}
          onChange={(e) => onChange({ ...value, range: "custom", to: e.target.value })}
          className="h-9 w-[9.5rem] text-xs"
          aria-label={t("usage.filter.to")}
        />
      </div>

      {/* key picker */}
      <Popover open={tokenOpen} onOpenChange={setTokenOpen}>
        <PopoverTrigger asChild>
          <Button variant="outline" size="sm" className="h-9 gap-1.5">
            {selected
              ? selected.name || t("usage.token.deleted", { id: selected.token_id })
              : t("usage.filter.tokensAll")}
            <ChevronDown className="h-3.5 w-3.5 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="max-h-80 w-80 overflow-y-auto p-1.5">
          <button
            type="button"
            onClick={() => {
              onChange({ ...value, tokenId: undefined });
              setTokenOpen(false);
            }}
            className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted"
          >
            <span className="w-4">
              {value.tokenId == null && <Check className="h-3.5 w-3.5 text-primary" />}
            </span>
            {t("usage.filter.tokensAll")}
          </button>
          {tokens.map((k) => (
            <button
              key={k.token_id}
              type="button"
              onClick={() => {
                onChange({ ...value, tokenId: k.token_id });
                setTokenOpen(false);
              }}
              className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-muted"
            >
              <span className="w-4">
                {value.tokenId === k.token_id && <Check className="h-3.5 w-3.5 text-primary" />}
              </span>
              <span className="min-w-0 flex-1 truncate">
                {k.name || t("usage.token.deleted", { id: k.token_id })}
                {showMember && k.email && (
                  <span className="ml-1 text-xs text-muted-foreground">{k.email}</span>
                )}
              </span>
              <span className="shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
                {fmtUSD(k.spent_usd)}
              </span>
            </button>
          ))}
        </PopoverContent>
      </Popover>

      {/* model */}
      <Select
        value={value.model || "__all"}
        onValueChange={(v) => onChange({ ...value, model: v === "__all" ? undefined : v })}
      >
        <SelectTrigger className="h-9 w-auto min-w-[9rem] gap-1.5 text-sm">
          <SelectValue placeholder={t("usage.filter.modelAll")} />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="__all">{t("usage.filter.modelAll")}</SelectItem>
          {models
            .filter((m) => m.model)
            .map((m) => (
              <SelectItem key={m.model} value={m.model}>
                {m.model}
              </SelectItem>
            ))}
        </SelectContent>
      </Select>

      {/* tags — few enough that toggle chips beat a dropdown */}
      {tags.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          {tags.map((tag) => (
            <button
              key={tag.tag}
              type="button"
              onClick={() =>
                onChange({ ...value, tag: value.tag === tag.tag ? undefined : tag.tag })
              }
            >
              <Badge
                variant={value.tag === tag.tag ? "default" : "outline"}
                className="cursor-pointer transition-colors"
              >
                {tag.tag}
              </Badge>
            </button>
          ))}
        </div>
      )}

      {dirty && (
        <Button
          variant="ghost"
          size="sm"
          className="h-9 gap-1.5"
          onClick={() => onChange(defaultFilter())}
        >
          <RotateCcw className="h-3.5 w-3.5" />
          {t("usage.filter.reset")}
        </Button>
      )}
    </div>
  );
}

/* --- Export --------------------------------------------------------------- */

/** Builds the querystring shared by every usage endpoint, so the CSV a user
 * downloads is exactly the data they're looking at. Anything else is a trust
 * bug. */
export function usageQuery(f: UsageFilter): string {
  const q = new URLSearchParams({ from: f.from, to: f.to });
  if (f.tokenId != null) q.set("token_id", String(f.tokenId));
  if (f.model) q.set("model", f.model);
  if (f.tag) q.set("tag", f.tag);
  return q.toString();
}

export function ExportButton({ basePath, filter }: { basePath: string; filter: UsageFilter }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);

  const run = async () => {
    setBusy(true);
    try {
      await apiDownload(
        `${basePath}/export.csv?${usageQuery(filter)}`,
        `usage-${filter.from}-${filter.to}.csv`,
      );
      toast.success(t("usage.exported"));
    } catch (e) {
      toast.error(errMsg(e, t("usage.exportFailed")));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Button variant="outline" onClick={run} disabled={busy} className="gap-2">
      {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
      {busy ? t("usage.exporting") : t("usage.export")}
    </Button>
  );
}
