import { useEffect, useState } from "react";
import { Receipt, RefreshCw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api";
import { lookupPriceCard } from "@/lib/pricing";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { Reveal, RevealStagger, RevealItem } from "@/components/landing/reveal";
import { SpotlightCard } from "@/components/landing/interactions";
import { PageHeader } from "@/components/app/page-primitives";
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
    <div className="space-y-8">
      <PageHeader
        eyebrow={t("nav.logs")}
        icon={Receipt}
        title={t("logs.title")}
        sub={t("logs.sub")}
        action={
          <Button variant="outline" onClick={() => reload(offset)} disabled={busy} className="gap-2">
            <RefreshCw className={cn("h-4 w-4", busy && "animate-spin")} />
            {t("common.refresh")}
          </Button>
        }
      />

      {sum && (
        <RevealStagger className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
          <RevealItem className="flex"><SumTile label={t("logs.summary.requests")} value={sum.count.toLocaleString()} /></RevealItem>
          <RevealItem className="flex"><SumTile label={t("logs.summary.sumIn")} value={fmtTokens(sum.input_tokens)} /></RevealItem>
          <RevealItem className="flex"><SumTile label={t("logs.summary.sumOut")} value={fmtTokens(sum.output_tokens)} /></RevealItem>
          <RevealItem className="flex"><SumTile label={t("logs.summary.cacheRead")} value={fmtTokens(sum.cache_read_tokens)} /></RevealItem>
          <RevealItem className="flex"><SumTile label={t("logs.summary.totalBilled")} value={fmtUSD(sum.cost_usd)} accent /></RevealItem>
        </RevealStagger>
      )}

      {err && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive font-mono">
          {err}
        </div>
      )}

      <Reveal className="glass overflow-hidden rounded-2xl">
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
      </Reveal>

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
    <SpotlightCard tiltDeg={0} className={cn("w-full rounded-xl p-3.5", accent && "ring-1 ring-primary/30")}>
      <div className="text-[11px] uppercase tracking-wider text-muted-foreground">{label}</div>
      <div
        className={cn(
          "mt-1 font-mono text-xl font-semibold tabular-nums",
          accent ? "text-primary" : "text-foreground",
        )}
      >
        {value}
      </div>
    </SpotlightCard>
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

  const rows: Array<{
    key: string;
    label: string;
    tokens: number;
    rate: number;
    sub: number;
    dim?: boolean;
  }> = [
    { key: "in", label: t("logs.popup.input"), tokens: entry.input_tokens, rate: priceCard.input_per_1m, sub: inUSD },
    { key: "out", label: t("logs.popup.output"), tokens: entry.output_tokens, rate: priceCard.output_per_1m, sub: outUSD },
    { key: "cr", label: t("logs.popup.cacheR"), tokens: entry.cache_read_tokens, rate: priceCard.cache_read_per_1m, sub: crUSD, dim: true },
    { key: "cw", label: t("logs.popup.cacheW"), tokens: entry.cache_create_tokens, rate: priceCard.cache_create_per_1m, sub: cwUSD, dim: true },
  ];

  return (
    <TooltipProvider delayDuration={120} disableHoverableContent={false}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            className={cn(
              "group relative inline-flex items-baseline gap-1 cursor-help font-semibold tabular-nums",
              "transition-colors hover:text-primary",
              drift && "text-amber-500 hover:text-amber-400",
            )}
          >
            <span>{fmtUSD(entry.cost_usd)}</span>
            {/* Animated dotted-underline that draws in on hover. */}
            <span
              aria-hidden
              className={cn(
                "pointer-events-none absolute inset-x-0 -bottom-0.5 h-px",
                "bg-[linear-gradient(to_right,currentColor_50%,transparent_50%)] bg-[length:4px_1px] bg-repeat-x",
                "scale-x-0 origin-left transition-transform duration-300 ease-out group-hover:scale-x-100",
                drift ? "opacity-90" : "opacity-60",
              )}
            />
          </button>
        </TooltipTrigger>
        <TooltipContent
          side="left"
          align="start"
          sideOffset={10}
          collisionPadding={12}
          className={cn(
            "w-[22rem] max-w-[92vw] p-0 overflow-hidden",
            "rounded-xl border border-border-strong/80 bg-popover text-popover-foreground",
            "shadow-[0_20px_70px_-20px_rgba(0,0,0,0.45),0_4px_12px_-6px_rgba(0,0,0,0.25)]",
          )}
        >
          {/* Hairline accent ribbon along the top edge — primary→transparent. */}
          <div
            aria-hidden
            className="h-[2px] w-full bg-gradient-to-r from-primary/0 via-primary/70 to-primary/0 animate-in fade-in-0 zoom-in-95 fill-mode-both"
            style={{ animationDuration: "260ms" }}
          />

          {/* Header — model + total. */}
          <div
            className="flex items-start justify-between gap-3 px-4 pt-3.5 pb-3 border-b border-border/60 animate-in fade-in-0 slide-in-from-top-1 fill-mode-both"
            style={{ animationDelay: "40ms", animationDuration: "280ms" }}
          >
            <div className="min-w-0 flex-1">
              <div className="font-mono text-[9px] uppercase tracking-[0.18em] text-muted-foreground/80">
                {t("logs.popup.header")}
              </div>
              <div className="mt-1 font-mono text-xs font-medium text-foreground truncate" title={entry.model}>
                {entry.model}
              </div>
              <div className="mt-0.5 font-mono text-[10px] text-muted-foreground">
                {(entry.provider || "anthropic")} · {entry.auth_kind}
              </div>
            </div>
            <div className="text-right shrink-0">
              <div className="font-mono text-[9px] uppercase tracking-[0.18em] text-muted-foreground/80">
                Total
              </div>
              <div className="mt-1 font-mono text-sm font-semibold tabular-nums text-foreground">
                {fmtUSD(entry.cost_usd)}
              </div>
            </div>
          </div>

          {/* Body — table-like rows with column eyebrows. */}
          <div className="px-4 pt-2.5 pb-3 space-y-1">
            <div className="grid grid-cols-[auto_1fr_auto] gap-x-3 items-baseline pb-1.5 font-mono text-[9px] uppercase tracking-[0.15em] text-muted-foreground/60">
              <span>{t("logs.popup.colCategory", "Category")}</span>
              <span className="text-right">{t("logs.popup.colFormula", "Tokens × Rate / 1M")}</span>
              <span className="text-right">{t("logs.popup.colSub", "Subtotal")}</span>
            </div>
            {rows.map((r, i) => (
              <div
                key={r.key}
                className={cn(
                  "grid grid-cols-[auto_1fr_auto] gap-x-3 items-baseline font-mono text-[11px] py-0.5",
                  r.dim && "opacity-70",
                  "animate-in fade-in-0 slide-in-from-left-2 fill-mode-both",
                )}
                style={{
                  animationDelay: `${100 + i * 50}ms`,
                  animationDuration: "280ms",
                }}
              >
                <span className="text-foreground/85">{r.label}</span>
                <span className="text-right text-foreground/70 tabular-nums whitespace-nowrap">
                  <span className="text-foreground">{fmtTokens(r.tokens)}</span>
                  <span className="text-muted-foreground/60"> × </span>
                  <span>${r.rate.toFixed(2)}</span>
                  <span className="text-muted-foreground/60">/M</span>
                </span>
                <span className="text-right tabular-nums text-foreground">
                  ${r.sub.toFixed(6)}
                </span>
              </div>
            ))}

            {/* Σ official total */}
            <div
              className="grid grid-cols-[auto_1fr_auto] gap-x-3 items-baseline font-mono text-[11px] pt-2 mt-1 border-t border-dashed border-border/60 animate-in fade-in-0 fill-mode-both"
              style={{
                animationDelay: `${100 + rows.length * 50}ms`,
                animationDuration: "300ms",
              }}
            >
              <span className="text-muted-foreground/80">Σ {t("logs.popup.official")}</span>
              <span />
              <span className="text-right tabular-nums font-medium">
                ${officialUSD.toFixed(6)}
              </span>
            </div>

            {/* Multiplier line — only if non-trivial */}
            {Math.abs(mult - 1) > 1e-6 && (
              <div
                className="grid grid-cols-[auto_1fr_auto] gap-x-3 items-baseline font-mono text-[11px] animate-in fade-in-0 fill-mode-both"
                style={{
                  animationDelay: `${100 + (rows.length + 1) * 50}ms`,
                  animationDuration: "300ms",
                }}
              >
                <span className="text-muted-foreground/80">{t("logs.popup.multiplier")}</span>
                <span />
                <span className="text-right tabular-nums">{mult.toFixed(2)}×</span>
              </div>
            )}
          </div>

          {/* Settled — accented strip at the bottom */}
          <div
            className={cn(
              "relative px-4 py-2.5 flex items-baseline justify-between gap-3",
              "border-t border-border/60 bg-primary/[0.07]",
              "animate-in fade-in-0 slide-in-from-bottom-1 fill-mode-both",
            )}
            style={{
              animationDelay: `${100 + (rows.length + 2) * 50}ms`,
              animationDuration: "320ms",
            }}
          >
            <span className="font-mono text-[10px] uppercase tracking-[0.15em] text-foreground/80">
              {t("logs.popup.youPaid")}
            </span>
            <span className="font-mono text-sm font-semibold tabular-nums text-foreground">
              {fmtUSD(entry.cost_usd)}
            </span>
            <span
              aria-hidden
              className="absolute inset-y-0 left-0 w-[2px] bg-primary"
            />
          </div>

          {/* Drift warning */}
          {drift && (
            <div
              className="px-4 py-2 border-t border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400 font-mono text-[10px] leading-relaxed animate-in fade-in-0 fill-mode-both"
              style={{
                animationDelay: `${100 + (rows.length + 3) * 50}ms`,
                animationDuration: "320ms",
              }}
            >
              {t("logs.popup.drift", { recomputed: "$" + computed.toFixed(6), stored: fmtUSD(entry.cost_usd) })}
            </div>
          )}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
