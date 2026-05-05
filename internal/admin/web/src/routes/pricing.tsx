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
        <h1 className="font-display text-4xl font-semibold tracking-tight md:text-5xl">Access tiers</h1>
        <p className="mt-3 max-w-2xl text-muted-foreground">
          Each tier defines the per-request rate applied to official token costs.
          The multiplier scales the official Anthropic / OpenAI price directly.
        </p>
      </div>
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {groups.map((g) => (
          <Card key={g.ID} className={g.IsDefault ? "border-primary/40 bg-primary/[0.04]" : ""}>
            <CardHeader>
              <div className="flex items-center justify-between">
                <CardTitle className="font-display text-2xl tracking-tight">{g.Name}</CardTitle>
                {g.IsDefault && (
                  <span className="inline-flex items-center gap-1 rounded-full border border-primary/30 bg-primary/15 px-2 py-0.5 text-xs font-mono uppercase tracking-wider text-primary">
                    <Sparkles className="h-3 w-3" /> default
                  </span>
                )}
              </div>
              <CardDescription>{g.Description}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="space-y-3">
                <Row label="Codex multiplier" value={`${g.CodexMultiplier.toFixed(2)}×`} />
                <Row label="Claude multiplier" value={`${g.ClaudeMultiplier.toFixed(2)}×`} />
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      <div className="rounded-lg border border-border-strong bg-muted/30 p-6">
        <h3 className="font-display text-lg font-medium">How billing works</h3>
        <p className="mt-2 max-w-3xl text-sm text-muted-foreground">
          Each request is charged at{" "}
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">official_cost × multiplier</code>.
          The official cost is the standard Anthropic / OpenAI per-token rate. A multiplier of{" "}
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">1.0</code> means you pay exactly the official rate.
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

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between rounded-md border border-border bg-card p-3">
      <span className="text-sm text-muted-foreground">{label}</span>
      <div className="font-mono text-sm font-semibold tabular-nums">{value}</div>
    </div>
  );
}
