import { useState, useEffect, useCallback } from "react";
import { api } from "@/legacy/lib/api";
import type { Pricing, PricingEntry, RequestEntry, RequestsResp } from "@/legacy/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Card } from "@/components/ui/card";
import { cn, fmtInt } from "@/legacy/lib/utils";

interface Props {
  refreshTick: number;
  pricing?: Pricing;
}

const localDate = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;

const cost = (v: number | undefined | null) => "$" + (v || 0).toFixed(4);

export function RequestsExplorer({ refreshTick, pricing }: Props) {
  // Mirror the server-side pricing.Catalog.Lookup: keys in pricing.models are
  // canonical "provider/model"; resolution is exact → hyphen-prefix walk →
  // provider_defaults → global default. Also strip the "(effort)" suffix
  // (e.g. "gpt-5.3-codex(high)") before matching — same as the Go side.
  const canonicalProvider = (p: string | undefined | null): string => {
    const v = (p || "").toLowerCase().trim();
    if (v === "" || v === "anthropic" || v === "claude") return "anthropic";
    if (v === "openai" || v === "codex" || v === "chatgpt") return "openai";
    return v;
  };

  const lookupPrice = (provider: string | undefined, model: string): PricingEntry | null => {
    if (!pricing) return null;
    const models = pricing.models || {};
    const prov = canonicalProvider(provider);
    let m = (model || "").toLowerCase().trim();
    if (m.endsWith(")")) {
      const i = m.lastIndexOf("(");
      if (i > 0) m = m.slice(0, i).trim();
    }
    if (m) {
      const full = `${prov}/${m}`;
      if (models[full]) return models[full];
      for (let i = m.lastIndexOf("-"); i > 0; i = m.lastIndexOf("-", i - 1)) {
        const p = models[`${prov}/${m.slice(0, i)}`];
        if (p) return p;
      }
    }
    const provDef = pricing.provider_defaults?.[prov];
    if (provDef) return provDef;
    return pricing.default || null;
  };

  const costBreakdown = (r: RequestEntry) => {
    const p = lookupPrice(r.provider, r.model);
    if (!p) return null;
    const fmt = (tok: number, per1m: number) =>
      `${fmtInt(tok)} × $${per1m.toFixed(2)}/1M = $${((tok * per1m) / 1e6).toFixed(6)}`;
    const inC = ((r.input_tokens || 0) * p.input_per_1m) / 1e6;
    const outC = ((r.output_tokens || 0) * p.output_per_1m) / 1e6;
    const crC = ((r.cache_read_tokens || 0) * p.cache_read_per_1m) / 1e6;
    const cwC = ((r.cache_create_tokens || 0) * p.cache_create_per_1m) / 1e6;
    return [
      `input  ${fmt(r.input_tokens || 0, p.input_per_1m)}`,
      `output ${fmt(r.output_tokens || 0, p.output_per_1m)}`,
      `cacheR ${fmt(r.cache_read_tokens || 0, p.cache_read_per_1m)}`,
      `cacheW ${fmt(r.cache_create_tokens || 0, p.cache_create_per_1m)}`,
      `total  $${(inC + outC + crC + cwC).toFixed(6)} (logged $${(r.cost_usd || 0).toFixed(6)})`,
    ].join("\n");
  };

  const today = localDate(new Date());
  const sevenAgo = localDate(new Date(Date.now() - 7 * 86400000));
  const [from, setFrom] = useState(sevenAgo);
  const [to, setTo] = useState(today);
  const [client, setClient] = useState("");
  const [model, setModel] = useState("");
  const [pageSize, setPageSize] = useState(50);
  const [page, setPage] = useState(1);
  const [clientsList, setClientsList] = useState<string[]>([]);
  const [data, setData] = useState<RequestsResp | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const loadClients = useCallback(async () => {
    try {
      const d = await api<{ clients: string[] }>("/admin/api/requests/clients");
      setClientsList(d.clients || []);
    } catch {
      // ignore
    }
  }, []);
  useEffect(() => {
    loadClients();
  }, [loadClients]);

  const run = useCallback(
    async (overridePage?: number) => {
      setBusy(true);
      setErr("");
      const effectivePage = overridePage ?? page;
      try {
        const qs = new URLSearchParams();
        if (from) qs.set("from", from);
        if (to) qs.set("to", to);
        if (client) qs.set("client", client);
        if (model) qs.set("model", model);
        if (pageSize) qs.set("limit", String(pageSize));
        qs.set("offset", String((effectivePage - 1) * pageSize));
        const d = await api<RequestsResp>("/admin/api/requests?" + qs.toString());
        setData(d);
        if (overridePage != null) setPage(overridePage);
      } catch (x: any) {
        setErr(x.message);
      } finally {
        setBusy(false);
      }
    },
    [from, to, client, model, pageSize, page],
  );

  useEffect(() => {
    run(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshTick]);

  const onQuery = () => run(1);

  const sortedByClient = data
    ? Object.entries(data.by_client).sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
    : [];
  const sortedByModel = data
    ? Object.entries(data.by_model).sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
    : [];
  const sortedByDay = data ? Object.entries(data.by_day).sort(([a], [b]) => a.localeCompare(b)) : [];

  const modelsList = data ? Object.keys(data.by_model).sort() : [];
  const maxDayCost = Math.max(1e-9, ...sortedByDay.map(([, a]) => a.cost_usd));

  return (
    <section>
      <div className="flex items-baseline justify-between mb-4">
        <h2 className="text-2xl font-semibold tracking-tight">Requests</h2>
        <span className="text-sm text-muted-foreground">
          {data ? `${fmtInt(data.scanned)} lines scanned · ${fmtInt(data.summary.count)} match` : ""}
        </span>
      </div>
      <Card className="overflow-hidden">
        <div className="p-4 grid grid-cols-1 md:grid-cols-6 gap-3 items-end border-b">
          <div className="space-y-1">
            <Label className="text-muted-foreground">From</Label>
            <Input type="date" value={from} onChange={(e) => setFrom(e.currentTarget.value)} />
          </div>
          <div className="space-y-1">
            <Label className="text-muted-foreground">To</Label>
            <Input type="date" value={to} onChange={(e) => setTo(e.currentTarget.value)} />
          </div>
          <div className="space-y-1">
            <Label className="text-muted-foreground">Client</Label>
            <Select value={client || "__any"} onValueChange={(v) => setClient(v === "__any" ? "" : v)}>
              <SelectTrigger>
                <SelectValue placeholder="(any)" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__any">(any)</SelectItem>
                {clientsList.map((c) => (
                  <SelectItem key={c} value={c}>
                    {c}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <Label className="text-muted-foreground">Model</Label>
            <Select value={model || "__any"} onValueChange={(v) => setModel(v === "__any" ? "" : v)}>
              <SelectTrigger>
                <SelectValue placeholder="(any)" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__any">(any)</SelectItem>
                {modelsList.map((m) => (
                  <SelectItem key={m} value={m}>
                    {m}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <Label className="text-muted-foreground">Page size</Label>
            <Select value={String(pageSize)} onValueChange={(v) => setPageSize(Number(v))}>
              <SelectTrigger className="mono text-sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {[25, 50, 100, 200, 500].map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Button disabled={busy} onClick={onQuery}>
            {busy ? "…" : "Query"}
          </Button>
        </div>

        {err && <div className="p-3 bg-red-50 dark:bg-red-900/30 text-destructive text-base">{err}</div>}

        {data && (
          <div className="p-4 grid grid-cols-2 md:grid-cols-5 gap-3 bg-muted/30 border-b">
            {[
              ["Requests", fmtInt(data.summary.count)],
              ["Total $", cost(data.summary.cost_usd)],
              ["Input", fmtInt(data.summary.input_tokens)],
              ["Output", fmtInt(data.summary.output_tokens)],
              ["Errors", fmtInt(data.summary.errors)],
            ].map(([k, v]) => (
              <div key={k} className="bg-background rounded-lg border px-3 py-2">
                <div className="text-xs text-muted-foreground">{k}</div>
                <div className="text-xl font-semibold">{v}</div>
              </div>
            ))}
          </div>
        )}

        {data && sortedByDay.length > 0 && (
          <div className="p-4 border-b">
            <div className="text-base font-medium mb-2">Daily cost</div>
            <div className="flex items-end gap-[3px] h-16">
              {sortedByDay.map(([day, a]) => {
                const pct = Math.round((a.cost_usd / maxDayCost) * 100);
                return (
                  <div key={day} className="flex-1 min-w-[6px] flex flex-col items-stretch justify-end">
                    <div
                      title={`${day}: ${cost(a.cost_usd)} · ${fmtInt(a.count)} req`}
                      className="bg-slate-700 dark:bg-slate-300 rounded-sm"
                      style={{ height: `${Math.max(pct, a.cost_usd > 0 ? 4 : 1)}%` }}
                    />
                  </div>
                );
              })}
            </div>
            <div className="flex justify-between mt-1 text-xs text-muted-foreground mono">
              <span>{sortedByDay[0]![0]}</span>
              <span>{sortedByDay[sortedByDay.length - 1]![0]}</span>
            </div>
          </div>
        )}

        {data && (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4 p-4 border-b">
            <div>
              <div className="text-base font-medium mb-2">By client</div>
              {sortedByClient.length === 0 ? (
                <div className="text-sm text-muted-foreground">—</div>
              ) : (
                <table className="w-full text-sm">
                  <tbody>
                    {sortedByClient.map(([k, a]) => (
                      <tr key={k} className="border-b">
                        <td className="py-1.5 pr-3 font-medium">
                          {k || <span className="text-muted-foreground">(unnamed)</span>}
                        </td>
                        <td className="py-1.5 mono text-right">{cost(a.cost_usd)}</td>
                        <td className="py-1.5 mono text-right text-muted-foreground w-20">
                          {fmtInt(a.count)} req
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
            <div>
              <div className="text-base font-medium mb-2">By model</div>
              {sortedByModel.length === 0 ? (
                <div className="text-sm text-muted-foreground">—</div>
              ) : (
                <table className="w-full text-sm">
                  <tbody>
                    {sortedByModel.map(([k, a]) => (
                      <tr key={k} className="border-b">
                        <td className="py-1.5 pr-3 mono">{k}</td>
                        <td className="py-1.5 mono text-right">{cost(a.cost_usd)}</td>
                        <td className="py-1.5 mono text-right text-muted-foreground w-20">
                          {fmtInt(a.count)} req
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        )}

        {data && (
          <div className="p-4">
            <div className="flex items-center justify-between mb-2">
              <div className="text-base font-medium">Recent matching entries</div>
              {(() => {
                const total = data.summary?.count || 0;
                const totalPages = Math.max(1, Math.ceil(total / pageSize));
                const clampedPage = Math.min(page, totalPages);
                const first = total === 0 ? 0 : (clampedPage - 1) * pageSize + 1;
                const last = Math.min(total, clampedPage * pageSize);
                return (
                  <div className="flex items-center gap-2 text-sm text-muted-foreground">
                    <span className="mono">
                      {fmtInt(first)}–{fmtInt(last)} / {fmtInt(total)}
                    </span>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={busy || clampedPage <= 1}
                      onClick={() => run(clampedPage - 1)}
                    >
                      Prev
                    </Button>
                    <span className="mono">
                      {clampedPage} / {totalPages}
                    </span>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={busy || clampedPage >= totalPages}
                      onClick={() => run(clampedPage + 1)}
                    >
                      Next
                    </Button>
                  </div>
                );
              })()}
            </div>
            {!data.entries || data.entries.length === 0 ? (
              <div className="text-sm text-muted-foreground py-6 text-center">No entries on this page.</div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead className="text-left text-xs uppercase text-muted-foreground border-b">
                    <tr>
                      <th className="py-2 pr-2">Time (UTC)</th>
                      <th className="py-2 pr-2">Client</th>
                      <th className="py-2 pr-2">Model</th>
                      <th className="py-2 pr-2">Auth</th>
                      <th className="py-2 pr-2 text-right">In</th>
                      <th className="py-2 pr-2 text-right">Out</th>
                      <th className="py-2 pr-2 text-right" title="Cache read tokens">
                        Cache R
                      </th>
                      <th className="py-2 pr-2 text-right" title="Cache write / creation tokens">
                        Cache W
                      </th>
                      <th className="py-2 pr-2 text-right">Cost</th>
                      <th className="py-2 pr-2 text-right">Status</th>
                      <th className="py-2 pr-2 text-right">Dur</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.entries.map((r, i) => (
                      <tr
                        key={i}
                        className={cn(
                          "border-b",
                          r.status >= 400 ? "bg-red-50/40 dark:bg-red-900/20" : "",
                        )}
                      >
                        <td className="py-1 pr-2 mono text-muted-foreground">
                          {r.ts.replace("T", " ").slice(0, 19)}
                        </td>
                        <td className="py-1 pr-2">
                          {r.client || <span className="text-muted-foreground">(—)</span>}
                        </td>
                        <td className="py-1 pr-2 mono">{r.model}</td>
                        <td className="py-1 pr-2 mono text-xs text-muted-foreground">
                          {r.auth_label || r.auth_id}
                        </td>
                        <td className="py-1 pr-2 mono text-right">{fmtInt(r.input_tokens)}</td>
                        <td className="py-1 pr-2 mono text-right">{fmtInt(r.output_tokens)}</td>
                        <td className="py-1 pr-2 mono text-right text-muted-foreground">
                          {fmtInt(r.cache_read_tokens)}
                        </td>
                        <td className="py-1 pr-2 mono text-right text-muted-foreground">
                          {fmtInt(r.cache_create_tokens)}
                        </td>
                        <td
                          className="py-1 pr-2 mono text-right cursor-help"
                          title={costBreakdown(r) || "pricing unavailable"}
                        >
                          {cost(r.cost_usd)}
                        </td>
                        <td
                          className={cn(
                            "py-1 pr-2 mono text-right",
                            r.status >= 400 ? "text-destructive" : "",
                          )}
                        >
                          {r.status}
                        </td>
                        <td className="py-1 pr-2 mono text-right text-muted-foreground">
                          {r.duration_ms}ms
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}
      </Card>
    </section>
  );
}
