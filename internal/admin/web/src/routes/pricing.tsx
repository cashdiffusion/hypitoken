import { useEffect, useState } from "react";
import { Sparkles } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { PublicHeader } from "@/components/layout/shell";
import { api } from "@/lib/api";
import type { PricingGroup } from "@/lib/types";

export default function PricingPage({ embedded }: { embedded?: boolean }) {
  const [groups, setGroups] = useState<PricingGroup[]>([]);

  useEffect(() => {
    api<{ groups: PricingGroup[] }>("/groups").then((r) => setGroups(r.groups || []));
  }, []);

  const content = (
    <div className="space-y-6">
      <div>
        <h1 className="font-display text-4xl font-semibold tracking-tight md:text-5xl">Pricing tiers</h1>
        <p className="mt-3 max-w-2xl text-muted-foreground">
          Wallet stays in real USD. Each tier has its own RMB↔USD peg per provider plus a multiplier.
          Lower peg = cheaper inference.
        </p>
      </div>
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {groups.map((g) => (
          <Card key={g.ID} className={g.IsDefault ? "border-primary/40 bg-primary/[0.04]" : ""}>
            <CardHeader>
              <div className="flex items-center justify-between">
                <CardTitle className="font-display text-2xl tracking-tight">{g.Name}</CardTitle>
                {g.IsDefault && <span className="inline-flex items-center gap-1 rounded-full border border-primary/30 bg-primary/15 px-2 py-0.5 text-xs font-mono uppercase tracking-wider text-primary"><Sparkles className="h-3 w-3" /> default</span>}
              </div>
              <CardDescription>{g.Description}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="space-y-3">
                <Row label="Codex peg" value={`¥${g.CodexRMBPerUSD.toFixed(2)} = $1`} mult={g.CodexMultiplier} />
                <Row label="Claude peg" value={`¥${g.ClaudeRMBPerUSD.toFixed(2)} = $1`} mult={g.ClaudeMultiplier} />
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      <div className="rounded-lg border border-border-strong bg-muted/30 p-6">
        <h3 className="font-display text-lg font-medium">How billing works</h3>
        <p className="mt-2 max-w-3xl text-sm text-muted-foreground">
          For each request: <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">bill = official_cost × (peg_RMB / live_CNY) × multiplier</code>.
          Example: a Claude call that officially costs <strong className="text-foreground">$0.10</strong> bills at the default tier
          (peg ¥2, live ¥7.2, mult 1.0) = <strong className="text-foreground">$0.0278</strong> from your wallet.
          The same Codex call at peg ¥0.5 = <strong className="text-foreground">$0.0069</strong>.
        </p>
      </div>
    </div>
  );

  if (embedded) return content;
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <PublicHeader />
      <div className="mx-auto max-w-7xl px-4 py-12 md:px-6 md:py-16">{content}</div>
    </div>
  );
}

function Row({ label, value, mult }: any) {
  return (
    <div className="flex items-center justify-between rounded-md border border-border bg-card p-3">
      <span className="text-sm text-muted-foreground">{label}</span>
      <div className="text-right">
        <div className="font-mono text-sm font-semibold tabular-nums">{value}</div>
        <div className="text-xs text-muted-foreground">× {mult.toFixed(2)}</div>
      </div>
    </div>
  );
}
