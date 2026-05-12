import { useEffect, useState } from "react";
import { Receipt, RefreshCw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api";
import { lookupPriceCard } from "@/lib/pricing";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

interface PriceCard {
  input_per_1m: number;
  output_per_1m: number;
  cache_read_per_1m: number;
  cache_create_per_1m: number;
}

// Per-request entry returned by /api/v2/me/requests. Shape matches
// internal/requestlog.Record verbatim — fields not relevant to billing
// (auth_id, auth_label, auth_kind) are present but unused in the table.
interface RequestEntry {
  ts: string;
  client?: string;
  client_token: string;
  provider?: string;
  auth_id: string;
  auth_label?: string;
  auth_kind: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  cost_usd: number;
  status: number;
  duration_ms: number;
  stream: boolean;
  path?: string;
  attempts?: number;
  error?: string;
  user_id?: number;
  multiplier?: number;
}

interface Aggregate {
  count: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  cost_usd: number;
  errors: number;
}

interface QueryResult {
  summary: Aggregate;
  by_model: Record<string, Aggregate>;
  entries: RequestEntry[];
  scanned: number;
  pricing?: Record<string, PriceCard>;
}

const PAGE = 50;

function fmtTokens(n: number): string {
  if (!n) return "0";
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "k";
  return String(n);
}

// Render a charge in USD with enough decimals to ground-truth — wallets
// are in USD so always show 4 decimals; thousand separator for clarity.
function fmtUSD(n: number): string {
  return "$" + (n || 0).toLocaleString("en-US", {
    minimumFractionDigits: 4,
    maximumFractionDigits: 4,
  });
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function statusClass(s: number): string {
  if (s >= 500) return "text-red-500";
  if (s >= 400) return "text-amber-500";
  if (s >= 200 && s < 300) return "text-emerald-500";
  return "text-muted-foreground";
}

export default function LogsPage() {
  const { t } = useTranslation();
  const [data, setData] = useState<QueryResult | null>(null);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const reload = async (o = offset) => {
    setBusy(true);
    setErr("");
    try {
      const r = await api<QueryResult>(`/me/requests?limit=${PAGE}&offset=${o}`);
      setData(r);
    } catch (e: any) {
      setErr(e.message || "load failed");
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    reload(0);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const entries = data?.entries || [];
  const sum = data?.summary;
  const pricing = data?.pricing || {};

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="font-display text-3xl font-semibold tracking-tight">
            <Receipt className="inline-block h-7 w-7 align-[-3px] text-primary" /> {t("logs.title")}
          </h1>
          <p className="mt-2 text-sm text-muted-foreground max-w-2xl">{t("logs.sub")}</p>
        </div>
        <Button
          variant="outline"
          onClick={() => reload(offset)}
          disabled={busy}
          className="gap-2"
        >
          <RefreshCw className={cn("h-4 w-4", busy && "animate-spin")} />
          {t("common.refresh")}
        </Button>
      </header>

      {sum && (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
          <SumTile label={t("logs.summary.requests")} value={sum.count.toLocaleString()} />
          <SumTile label={t("logs.summary.sumIn")} value={fmtTokens(sum.input_tokens)} />
          <SumTile label={t("logs.summary.sumOut")} value={fmtTokens(sum.output_tokens)} />
          <SumTile label={t("logs.summary.cacheRead")} value={fmtTokens(sum.cache_read_tokens)} />
          <SumTile label={t("logs.summary.totalBilled")} value={fmtUSD(sum.cost_usd)} accent />
        </div>
      )}

      {err && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive font-mono">
          {err}
        </div>
      )}

      <div className="rounded-xl border border-border overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted/30 text-xs text-muted-foreground uppercase tracking-wider">
              <tr>
                <th className="px-3 py-2.5 text-left font-medium">{t("logs.columns.time")}</th>
                <th className="px-3 py-2.5 text-left font-medium">{t("logs.columns.model")}</th>
                <th className="px-3 py-2.5 text-right font-medium">{t("logs.columns.input")}</th>
                <th className="px-3 py-2.5 text-right font-medium">{t("logs.columns.output")}</th>
                <th className="px-3 py-2.5 text-right font-medium">{t("logs.columns.cacheRW")}</th>
                <th className="px-3 py-2.5 text-right font-medium">{t("logs.columns.mult")}</th>
                <th className="px-3 py-2.5 text-right font-medium">{t("logs.columns.charged")}</th>
                <th className="px-3 py-2.5 text-right font-medium">{t("logs.columns.status")}</th>
                <th className="px-3 py-2.5 text-right font-medium">{t("logs.columns.latency")}</th>
              </tr>
            </thead>
            <tbody>
              {entries.length === 0 && !busy && (
                <tr>
                  <td colSpan={9} className="px-3 py-12 text-center text-muted-foreground">{t("logs.none")}</td>
                </tr>
              )}
              {entries.map((e, i) => (
                <tr key={i} className="border-t border-border hover:bg-accent/30">
                  <td className="px-3 py-2 font-mono text-xs whitespace-nowrap" title={e.ts}>
                    {fmtTime(e.ts)}
                  </td>
                  <td className="px-3 py-2 font-mono text-xs">
                    <div className="font-medium text-foreground">{e.model}</div>
                    <div className="text-muted-foreground/70 text-[11px]">
                      {(e.provider || "anthropic")} · {e.auth_kind}
                    </div>
                  </td>
                  <td className="px-3 py-2 font-mono tabular-nums text-right">
                    {fmtTokens(e.input_tokens)}
                  </td>
                  <td className="px-3 py-2 font-mono tabular-nums text-right">
                    {fmtTokens(e.output_tokens)}
                  </td>
                  <td className="px-3 py-2 font-mono tabular-nums text-right text-muted-foreground/80">
                    {fmtTokens(e.cache_read_tokens)} / {fmtTokens(e.cache_create_tokens)}
                  </td>
                  <td className="px-3 py-2 font-mono tabular-nums text-right text-muted-foreground/80">
                    {e.multiplier ? e.multiplier.toFixed(2) + "×" : "—"}
                  </td>
                  <td className="px-3 py-2 font-mono tabular-nums text-right font-semibold">
                    <ChargedCell entry={e} priceCard={lookupPriceCard(pricing, e.provider, e.model)} />
                  </td>
                  <td className={cn("px-3 py-2 font-mono tabular-nums text-right", statusClass(e.status))}>
                    {e.status}
                  </td>
                  <td className="px-3 py-2 font-mono tabular-nums text-right text-muted-foreground">
                    {e.duration_ms}ms
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>
          {t("logs.showing", { shown: entries.length, total: data?.summary.count.toLocaleString() || 0 })}
          {data ? t("logs.scanned", { n: data.scanned.toLocaleString() }) : ""}
        </span>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={offset === 0 || busy}
            onClick={() => {
              const next = Math.max(0, offset - PAGE);
              setOffset(next);
              reload(next);
            }}
          >
            {t("logs.newer")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={entries.length < PAGE || busy}
            onClick={() => {
              const next = offset + PAGE;
              setOffset(next);
              reload(next);
            }}
          >
            {t("logs.older")}
          </Button>
        </div>
      </div>
    </div>
  );
}

function SumTile({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <div
      className={cn(
        "rounded-lg border p-3",
        accent ? "border-primary/30 bg-primary/5" : "border-border bg-card",
      )}
    >
      <div className="text-[11px] uppercase tracking-wider text-muted-foreground">{label}</div>
      <div
        className={cn(
          "mt-1 font-mono text-xl font-semibold tabular-nums",
          accent ? "text-primary" : "text-foreground",
        )}
      >
        {value}
      </div>
    </div>
  );
}

// ChargedCell renders the per-row charged amount with a hover popup that
// breaks down the calculation: each token bucket priced at the official
// catalog rate, summed to the official cost, multiplied by the group
// coefficient. Lets users reconcile every dollar against a transparent
// formula rather than a black-box number. Falls back to a plain number
// when pricing for the row's model isn't in the response (rare — only if
// the catalog lookup hit the global default and the model name isn't a
// known prefix).
function ChargedCell({ entry, priceCard }: { entry: RequestEntry; priceCard?: PriceCard }) {
  const { t } = useTranslation();
  if (!priceCard) {
    return <span>{fmtUSD(entry.cost_usd)}</span>;
  }
  const inUSD  = (entry.input_tokens        * priceCard.input_per_1m)        / 1e6;
  const outUSD = (entry.output_tokens       * priceCard.output_per_1m)       / 1e6;
  const crUSD  = (entry.cache_read_tokens   * priceCard.cache_read_per_1m)   / 1e6;
  const cwUSD  = (entry.cache_create_tokens * priceCard.cache_create_per_1m) / 1e6;
  const officialUSD = inUSD + outUSD + crUSD + cwUSD;
  const mult = entry.multiplier && entry.multiplier > 0 ? entry.multiplier : 1;
  const computed = officialUSD * mult;
  // Sanity-check: server-side cost_usd should match our recompute. Drift
  // hints at a stale pricing card; surface it visually so it doesn't go
  // unnoticed.
  const drift = Math.abs(computed - entry.cost_usd) > 0.0005;

  return (
    <TooltipProvider delayDuration={150}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            className={cn(
              "underline decoration-dotted decoration-border-strong underline-offset-4 cursor-help",
              drift && "text-amber-500",
            )}
          >
            {fmtUSD(entry.cost_usd)}
          </button>
        </TooltipTrigger>
        <TooltipContent
          side="left"
          align="start"
          sideOffset={8}
          className="max-w-md w-[22rem] p-0 bg-popover text-popover-foreground border border-border-strong shadow-2xl rounded-lg overflow-hidden"
        >
          <div className="bg-muted/50 px-4 py-2.5 border-b border-border">
            <div className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground">{t("logs.popup.header")}</div>
            <div className="font-mono text-xs text-foreground mt-0.5 truncate">{entry.model}</div>
          </div>

          <div className="px-4 py-3 space-y-2 text-xs font-mono">
            <FormulaRow label={t("logs.popup.input")} tokens={entry.input_tokens} ratePer1M={priceCard.input_per_1m} subtotal={inUSD} />
            <FormulaRow label={t("logs.popup.output")} tokens={entry.output_tokens} ratePer1M={priceCard.output_per_1m} subtotal={outUSD} />
            <FormulaRow label={t("logs.popup.cacheR")} tokens={entry.cache_read_tokens} ratePer1M={priceCard.cache_read_per_1m} subtotal={crUSD} dim />
            <FormulaRow label={t("logs.popup.cacheW")} tokens={entry.cache_create_tokens} ratePer1M={priceCard.cache_create_per_1m} subtotal={cwUSD} dim />

            <div className="pt-2 mt-2 border-t border-dashed border-border flex items-baseline justify-between">
              <span className="text-muted-foreground text-[10px] uppercase tracking-wider">{t("logs.popup.official")}</span>
              <span className="tabular-nums">${officialUSD.toFixed(6)}</span>
            </div>

            <div className="flex items-baseline justify-between">
              <span className="text-muted-foreground text-[10px] uppercase tracking-wider">{t("logs.popup.multiplier")}</span>
              <span className="tabular-nums">{mult.toFixed(2)}×</span>
            </div>

            <div className="pt-2 mt-2 border-t border-border flex items-baseline justify-between bg-primary/[0.06] -mx-4 px-4 py-2 -mb-3">
              <span className="text-foreground font-semibold text-[11px] uppercase tracking-wider">{t("logs.popup.youPaid")}</span>
              <span className="tabular-nums text-foreground font-semibold text-sm">{fmtUSD(entry.cost_usd)}</span>
            </div>

            {drift && (
              <div className="text-amber-500 text-[10px] pt-2">
                {t("logs.popup.drift", { recomputed: "$" + computed.toFixed(6), stored: fmtUSD(entry.cost_usd) })}
              </div>
            )}
          </div>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

function FormulaRow({
  label,
  tokens,
  ratePer1M,
  subtotal,
  dim,
}: {
  label: string;
  tokens: number;
  ratePer1M: number;
  subtotal: number;
  dim?: boolean;
}) {
  return (
    <div className={cn("flex items-baseline justify-between gap-3", dim && "opacity-70")}>
      <span className="text-muted-foreground text-[10px] uppercase tracking-wider min-w-[5rem]">{label}</span>
      <span className="text-[11px] text-foreground tabular-nums whitespace-nowrap">
        {fmtTokens(tokens)} × ${ratePer1M.toFixed(2)}/M
      </span>
      <span className="tabular-nums text-foreground min-w-[5.5rem] text-right">${subtotal.toFixed(6)}</span>
    </div>
  );
}
