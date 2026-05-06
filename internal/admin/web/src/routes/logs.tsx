import { useEffect, useState } from "react";
import { Receipt, RefreshCw } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

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

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="font-display text-3xl font-semibold tracking-tight">
            <Receipt className="inline-block h-7 w-7 align-[-3px] text-primary" /> Billing log
          </h1>
          <p className="mt-2 text-sm text-muted-foreground max-w-2xl">
            Every request charged to your account, broken down by token type, official catalog rate,
            and the group multiplier that scaled it. Each row is the exact dollar amount deducted
            from your wallet.
          </p>
        </div>
        <Button
          variant="outline"
          onClick={() => reload(offset)}
          disabled={busy}
          className="gap-2"
        >
          <RefreshCw className={cn("h-4 w-4", busy && "animate-spin")} />
          Refresh
        </Button>
      </header>

      {/* Summary tiles — totals across the current filter window */}
      {sum && (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
          <SumTile label="Requests" value={sum.count.toLocaleString()} />
          <SumTile label="Σ in (tok)" value={fmtTokens(sum.input_tokens)} />
          <SumTile label="Σ out (tok)" value={fmtTokens(sum.output_tokens)} />
          <SumTile label="Cache read (tok)" value={fmtTokens(sum.cache_read_tokens)} />
          <SumTile label="Total billed" value={fmtUSD(sum.cost_usd)} accent />
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
                <th className="px-3 py-2.5 text-left font-medium">Time</th>
                <th className="px-3 py-2.5 text-left font-medium">Model</th>
                <th className="px-3 py-2.5 text-right font-medium">Input</th>
                <th className="px-3 py-2.5 text-right font-medium">Output</th>
                <th className="px-3 py-2.5 text-right font-medium">Cache R/W</th>
                <th className="px-3 py-2.5 text-right font-medium">×</th>
                <th className="px-3 py-2.5 text-right font-medium">Charged</th>
                <th className="px-3 py-2.5 text-right font-medium">Status</th>
                <th className="px-3 py-2.5 text-right font-medium">Latency</th>
              </tr>
            </thead>
            <tbody>
              {entries.length === 0 && !busy && (
                <tr>
                  <td colSpan={9} className="px-3 py-12 text-center text-muted-foreground">
                    No requests billed yet. Spin up an API call against /v1/messages or
                    /v1/chat/completions and the charge will appear here.
                  </td>
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
                    {fmtUSD(e.cost_usd)}
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
          Showing {entries.length} of {data?.summary.count.toLocaleString() || 0} matching records
          {data ? ` · scanned ${data.scanned.toLocaleString()} log lines` : ""}
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
            ← Newer
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
            Older →
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
