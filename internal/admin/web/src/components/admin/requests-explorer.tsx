import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Reveal } from "@/components/landing/reveal";
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
import { apiGet } from "@/lib/api";
import { cn, errMsg, fmtInt } from "@/lib/utils";

interface RequestEntry {
  ts: string;
  client?: string;
  provider?: string;
  model: string;
  auth_id: string;
  auth_label?: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  cost_usd: number;
  status: number;
  duration_ms: number;
}

interface RequestAgg {
  count: number;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  errors: number;
}

interface RequestsResp {
  entries: RequestEntry[];
  summary: RequestAgg;
  by_client: Record<string, RequestAgg>;
  by_model: Record<string, RequestAgg>;
  by_day: Record<string, RequestAgg>;
  scanned: number;
}

const localDate = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;

const cost = (v: number | undefined | null) => `$${(v || 0).toFixed(4)}`;

export function RequestsExplorer({ refreshTick }: { refreshTick: number }) {
  const { t } = useTranslation();
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
      const d = await apiGet<{ clients: string[] }>("/admin/requests/clients");
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
        const d = await apiGet<RequestsResp>(`/admin/requests?${qs.toString()}`);
        setData(d);
        if (overridePage != null) setPage(overridePage);
      } catch (x) {
        setErr(errMsg(x));
      } finally {
        setBusy(false);
      }
    },
    [from, to, client, model, pageSize, page],
  );

  // biome-ignore lint/correctness/useExhaustiveDependencies: re-query only when the parent bumps refreshTick; run() identity changes with the filter inputs and must not auto-trigger a fetch.
  useEffect(() => {
    run(1);
  }, [refreshTick]);

  const onQuery = () => run(1);

  const sortedByClient = data
    ? Object.entries(data.by_client).sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
    : [];
  const sortedByModel = data
    ? Object.entries(data.by_model).sort(([, a], [, b]) => b.cost_usd - a.cost_usd)
    : [];
  const sortedByDay = data
    ? Object.entries(data.by_day).sort(([a], [b]) => a.localeCompare(b))
    : [];

  const modelsList = data ? Object.keys(data.by_model).sort() : [];
  const maxDayCost = Math.max(1e-9, ...sortedByDay.map(([, a]) => a.cost_usd));

  return (
    <Reveal>
      <section>
        <div className="mb-4 flex items-baseline justify-between">
          <div>
            <span className="eyebrow text-primary">{t("admin.tabs.requests")}</span>
            <h2 className="mt-1 font-display text-2xl font-semibold tracking-tight">
              {t("legacy.requestsExplorer.title")}
            </h2>
          </div>
          <span className="font-mono text-sm text-muted-foreground tabular-nums">
            {data
              ? t("legacy.requestsExplorer.scanned", {
                  scanned: fmtInt(data.scanned),
                  count: fmtInt(data.summary.count),
                })
              : ""}
          </span>
        </div>
        <div className="glass overflow-hidden rounded-2xl">
          <div className="grid grid-cols-1 items-end gap-3 border-b border-border/60 p-4 md:grid-cols-6">
            <div className="space-y-1">
              <Label className="text-muted-foreground">{t("legacy.requestsExplorer.from")}</Label>
              <Input type="date" value={from} onChange={(e) => setFrom(e.currentTarget.value)} />
            </div>
            <div className="space-y-1">
              <Label className="text-muted-foreground">{t("legacy.requestsExplorer.to")}</Label>
              <Input type="date" value={to} onChange={(e) => setTo(e.currentTarget.value)} />
            </div>
            <div className="space-y-1">
              <Label className="text-muted-foreground">{t("legacy.requestsExplorer.client")}</Label>
              <Select
                value={client || "__any"}
                onValueChange={(v) => setClient(v === "__any" ? "" : v)}
              >
                <SelectTrigger>
                  <SelectValue placeholder={t("legacy.requestsExplorer.any")} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__any">{t("legacy.requestsExplorer.any")}</SelectItem>
                  {clientsList.map((c) => (
                    <SelectItem key={c} value={c}>
                      {c}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label className="text-muted-foreground">{t("legacy.requestsExplorer.model")}</Label>
              <Select
                value={model || "__any"}
                onValueChange={(v) => setModel(v === "__any" ? "" : v)}
              >
                <SelectTrigger>
                  <SelectValue placeholder={t("legacy.requestsExplorer.any")} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__any">{t("legacy.requestsExplorer.any")}</SelectItem>
                  {modelsList.map((m) => (
                    <SelectItem key={m} value={m}>
                      {m}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label className="text-muted-foreground">
                {t("legacy.requestsExplorer.pageSize")}
              </Label>
              <Select value={String(pageSize)} onValueChange={(v) => setPageSize(Number(v))}>
                <SelectTrigger className="font-mono text-sm">
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
              {busy ? t("legacy.requestsExplorer.busy") : t("legacy.requestsExplorer.query")}
            </Button>
          </div>

          {err && (
            <div className="border-b border-destructive/30 bg-destructive/10 p-3 font-mono text-sm text-destructive">
              {err}
            </div>
          )}

          {data && (
            <div className="grid grid-cols-2 gap-3 border-b border-border/60 bg-muted/20 p-4 md:grid-cols-5">
              {[
                [t("legacy.requestsExplorer.tilesRequests"), fmtInt(data.summary.count)],
                [t("legacy.requestsExplorer.tilesTotal"), cost(data.summary.cost_usd)],
                [t("legacy.requestsExplorer.tilesInput"), fmtInt(data.summary.input_tokens)],
                [t("legacy.requestsExplorer.tilesOutput"), fmtInt(data.summary.output_tokens)],
                [t("legacy.requestsExplorer.tilesErrors"), fmtInt(data.summary.errors)],
              ].map(([k, v]) => (
                <div
                  key={k}
                  className="rounded-xl border border-border-strong/60 bg-card/50 px-3 py-2.5 backdrop-blur-sm"
                >
                  <div className="text-[11px] uppercase tracking-wider text-muted-foreground">
                    {k}
                  </div>
                  <div className="mt-1 font-mono text-xl font-semibold tabular-nums">{v}</div>
                </div>
              ))}
            </div>
          )}

          {data && sortedByDay.length > 0 && (
            <div className="p-4 border-b">
              <div className="text-base font-medium mb-2">
                {t("legacy.requestsExplorer.dailyCost")}
              </div>
              <div className="flex items-end gap-[3px] h-16">
                {sortedByDay.map(([day, a]) => {
                  const pct = Math.round((a.cost_usd / maxDayCost) * 100);
                  return (
                    <div
                      key={day}
                      className="flex-1 min-w-[6px] flex flex-col items-stretch justify-end"
                    >
                      <div
                        title={`${day}: ${cost(a.cost_usd)} · ${fmtInt(a.count)} req`}
                        className={cn(
                          "rounded-sm transition-colors",
                          a.cost_usd > 0
                            ? "bg-gradient-to-t from-primary/40 to-primary"
                            : "bg-border",
                        )}
                        style={{ height: `${Math.max(pct, a.cost_usd > 0 ? 4 : 1)}%` }}
                      />
                    </div>
                  );
                })}
              </div>
              <div className="flex justify-between mt-1 text-xs text-muted-foreground font-mono">
                <span>{sortedByDay[0]?.[0]}</span>
                <span>{sortedByDay[sortedByDay.length - 1]?.[0]}</span>
              </div>
            </div>
          )}

          {data && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4 p-4 border-b">
              <div>
                <div className="text-base font-medium mb-2">
                  {t("legacy.requestsExplorer.byClient")}
                </div>
                {sortedByClient.length === 0 ? (
                  <div className="text-sm text-muted-foreground">—</div>
                ) : (
                  <table className="w-full text-sm">
                    <tbody>
                      {sortedByClient.map(([k, a]) => (
                        <tr key={k} className="border-b">
                          <td className="py-1.5 pr-3 font-medium">
                            {k || (
                              <span className="text-muted-foreground">
                                {t("legacy.requestsExplorer.unnamed")}
                              </span>
                            )}
                          </td>
                          <td className="py-1.5 font-mono text-right">{cost(a.cost_usd)}</td>
                          <td className="py-1.5 font-mono text-right text-muted-foreground w-20">
                            {fmtInt(a.count)} req
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
              <div>
                <div className="text-base font-medium mb-2">
                  {t("legacy.requestsExplorer.byModel")}
                </div>
                {sortedByModel.length === 0 ? (
                  <div className="text-sm text-muted-foreground">—</div>
                ) : (
                  <table className="w-full text-sm">
                    <tbody>
                      {sortedByModel.map(([k, a]) => (
                        <tr key={k} className="border-b">
                          <td className="py-1.5 pr-3 font-mono">{k}</td>
                          <td className="py-1.5 font-mono text-right">{cost(a.cost_usd)}</td>
                          <td className="py-1.5 font-mono text-right text-muted-foreground w-20">
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
                <div className="text-base font-medium">{t("legacy.requestsExplorer.recent")}</div>
                {(() => {
                  const total = data.summary?.count || 0;
                  const totalPages = Math.max(1, Math.ceil(total / pageSize));
                  const clampedPage = Math.min(page, totalPages);
                  const first = total === 0 ? 0 : (clampedPage - 1) * pageSize + 1;
                  const last = Math.min(total, clampedPage * pageSize);
                  return (
                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                      <span className="font-mono">
                        {fmtInt(first)}–{fmtInt(last)} / {fmtInt(total)}
                      </span>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={busy || clampedPage <= 1}
                        onClick={() => run(clampedPage - 1)}
                      >
                        {t("legacy.requestsExplorer.prev")}
                      </Button>
                      <span className="font-mono">
                        {clampedPage} / {totalPages}
                      </span>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={busy || clampedPage >= totalPages}
                        onClick={() => run(clampedPage + 1)}
                      >
                        {t("legacy.requestsExplorer.next")}
                      </Button>
                    </div>
                  );
                })()}
              </div>
              {!data.entries || data.entries.length === 0 ? (
                <div className="text-sm text-muted-foreground py-6 text-center">
                  {t("legacy.requestsExplorer.none")}
                </div>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead className="text-left text-xs uppercase text-muted-foreground border-b">
                      <tr>
                        <th className="py-2 pr-2">{t("legacy.requestsExplorer.cols.time")}</th>
                        <th className="py-2 pr-2">{t("legacy.requestsExplorer.cols.client")}</th>
                        <th className="py-2 pr-2">{t("legacy.requestsExplorer.cols.model")}</th>
                        <th className="py-2 pr-2">{t("legacy.requestsExplorer.cols.auth")}</th>
                        <th className="py-2 pr-2 text-right">
                          {t("legacy.requestsExplorer.cols.input")}
                        </th>
                        <th className="py-2 pr-2 text-right">
                          {t("legacy.requestsExplorer.cols.output")}
                        </th>
                        <th className="py-2 pr-2 text-right">
                          {t("legacy.requestsExplorer.cols.cacheR")}
                        </th>
                        <th className="py-2 pr-2 text-right">
                          {t("legacy.requestsExplorer.cols.cacheW")}
                        </th>
                        <th className="py-2 pr-2 text-right">
                          {t("legacy.requestsExplorer.cols.cost")}
                        </th>
                        <th className="py-2 pr-2 text-right">
                          {t("legacy.requestsExplorer.cols.status")}
                        </th>
                        <th className="py-2 pr-2 text-right">
                          {t("legacy.requestsExplorer.cols.duration")}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.entries.map((r) => (
                        <tr
                          key={`${r.ts}-${r.auth_id}-${r.input_tokens}-${r.output_tokens}-${r.cost_usd}`}
                          className={cn(
                            "border-b border-border/50 transition-colors hover:bg-primary/[0.03]",
                            r.status >= 400 && "bg-destructive/[0.07]",
                          )}
                        >
                          <td className="py-1 pr-2 font-mono text-muted-foreground">
                            {r.ts.replace("T", " ").slice(0, 19)}
                          </td>
                          <td className="py-1 pr-2">
                            {r.client || <span className="text-muted-foreground">(—)</span>}
                          </td>
                          <td className="py-1 pr-2 font-mono">{r.model}</td>
                          <td className="py-1 pr-2 font-mono text-xs text-muted-foreground">
                            {r.auth_label || r.auth_id}
                          </td>
                          <td className="py-1 pr-2 font-mono text-right">
                            {fmtInt(r.input_tokens)}
                          </td>
                          <td className="py-1 pr-2 font-mono text-right">
                            {fmtInt(r.output_tokens)}
                          </td>
                          <td className="py-1 pr-2 font-mono text-right text-muted-foreground">
                            {fmtInt(r.cache_read_tokens)}
                          </td>
                          <td className="py-1 pr-2 font-mono text-right text-muted-foreground">
                            {fmtInt(r.cache_create_tokens)}
                          </td>
                          <td className="py-1 pr-2 font-mono text-right">{cost(r.cost_usd)}</td>
                          <td
                            className={cn(
                              "py-1 pr-2 font-mono text-right",
                              r.status >= 400 ? "text-destructive" : "",
                            )}
                          >
                            {r.status}
                          </td>
                          <td className="py-1 pr-2 font-mono text-right text-muted-foreground">
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
        </div>
      </section>
    </Reveal>
  );
}
