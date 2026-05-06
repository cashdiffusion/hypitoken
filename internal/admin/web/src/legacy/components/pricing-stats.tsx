import { useState, useEffect, useCallback } from "react";
import { api } from "@/legacy/lib/api";
import type { Pricing, PricingEntry, RequestsResp } from "@/legacy/lib/types";
import { Card } from "@/components/ui/card";
import { fmtInt } from "@/legacy/lib/utils";

interface Props {
  pricing?: Pricing;
  refreshTick: number;
}

export function PricingStats({ pricing, refreshTick }: Props) {
  const [data, setData] = useState<RequestsResp | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const lookupPrice = (model: string): PricingEntry | null => {
    if (!pricing) return null;
    const m = (model || "").toLowerCase().trim();
    const models = pricing.models || {};
    if (m && models[m]) return models[m];
    if (m) {
      for (let i = m.lastIndexOf("-"); i > 0; i = m.lastIndexOf("-", i - 1)) {
        const p = models[m.slice(0, i)];
        if (p) return p;
      }
    }
    return pricing.default || null;
  };

  const load = useCallback(async () => {
    setBusy(true);
    setErr("");
    try {
      const d = await api<RequestsResp>("/admin/api/requests?limit=1");
      setData(d);
    } catch (x: any) {
      setErr(x.message);
    } finally {
      setBusy(false);
    }
  }, []);
  useEffect(() => {
    load();
  }, [load, refreshTick]);

  const stats = (() => {
    if (!data || !pricing) return null;
    const s = data.summary;
    const input = s.input_tokens || 0;
    const cacheRead = s.cache_read_tokens || 0;
    const cacheCreate = s.cache_create_tokens || 0;
    const denom = input + cacheRead + cacheCreate;
    const hitRate = denom > 0 ? cacheRead / denom : 0;
    const actualCost = s.cost_usd || 0;
    let noCacheCost = 0;
    for (const [name, a] of Object.entries(data.by_model)) {
      const p = lookupPrice(name);
      if (!p) continue;
      const ain = a.input_tokens || 0;
      const acr = a.cache_read_tokens || 0;
      const acw = a.cache_create_tokens || 0;
      const aout = a.output_tokens || 0;
      noCacheCost += ((ain + acr + acw) * p.input_per_1m) / 1e6;
      noCacheCost += (aout * p.output_per_1m) / 1e6;
    }
    return {
      hitRate,
      actualCost,
      noCacheCost,
      savings: noCacheCost - actualCost,
      input,
      cacheRead,
      cacheCreate,
    };
  })();

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
      <Card className="p-5">
        <div className="text-sm text-muted-foreground">Cache hit rate</div>
        <div className="mt-2 text-2xl font-semibold">
          {stats ? (stats.hitRate * 100).toFixed(2) + "%" : busy ? "…" : "—"}
        </div>
        {stats && (
          <div className="mt-1 text-xs text-muted-foreground mono">
            cacheR {fmtInt(stats.cacheRead)} / (input {fmtInt(stats.input)} + cacheR{" "}
            {fmtInt(stats.cacheRead)} + cacheW {fmtInt(stats.cacheCreate)})
          </div>
        )}
      </Card>
      <Card className="p-5">
        <div className="text-sm text-muted-foreground">Saved by caching</div>
        <div className="mt-2 text-2xl font-semibold">
          {stats ? "$" + stats.savings.toFixed(4) : busy ? "…" : "—"}
        </div>
        {stats && (
          <div className="mt-1 text-xs text-muted-foreground mono">
            no-cache ${stats.noCacheCost.toFixed(4)} − actual ${stats.actualCost.toFixed(4)}
          </div>
        )}
      </Card>
      {err && <div className="md:col-span-2 text-sm text-destructive">{err}</div>}
    </div>
  );
}
