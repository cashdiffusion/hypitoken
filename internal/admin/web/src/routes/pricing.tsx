import { useEffect, useState } from "react";
import { Sparkles } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { PublicHeader } from "@/components/layout/shell";
import { api } from "@/lib/api";
import type { PricingGroup } from "@/lib/types";

// Official Anthropic & OpenAI model pricing (USD per 1M tokens, as of May 2025)
const CLAUDE_MODELS = [
  {
    name: "claude-opus-4-7",
    display: "Claude Opus 4.7",
    input: 5.0,
    output: 25.0,
    cacheWrite: 6.25,
    cacheRead: 0.5,
  },
  {
    name: "claude-sonnet-4-6",
    display: "Claude Sonnet 4.6",
    input: 3.0,
    output: 15.0,
    cacheWrite: 3.75,
    cacheRead: 0.3,
  },
  {
    name: "claude-sonnet-4-5",
    display: "Claude Sonnet 4.5",
    input: 3.0,
    output: 15.0,
    cacheWrite: 3.75,
    cacheRead: 0.3,
  },
  {
    name: "claude-haiku-4-5",
    display: "Claude Haiku 4.5",
    input: 1.0,
    output: 5.0,
    cacheWrite: 1.25,
    cacheRead: 0.1,
  },
];

const CODEX_MODELS = [
  {
    name: "codex-davinci-002",
    display: "o3",
    input: 2.0,
    output: 8.0,
    cacheWrite: null,
    cacheRead: null,
  },
  {
    name: "o4-mini",
    display: "o4-mini",
    input: 1.1,
    output: 4.4,
    cacheWrite: null,
    cacheRead: null,
  },
  {
    name: "gpt-4.1",
    display: "GPT-4.1",
    input: 2.0,
    output: 8.0,
    cacheWrite: null,
    cacheRead: null,
  },
  {
    name: "gpt-4.1-mini",
    display: "GPT-4.1 mini",
    input: 0.4,
    output: 1.6,
    cacheWrite: null,
    cacheRead: null,
  },
  {
    name: "gpt-4o-mini",
    display: "GPT-4o mini",
    input: 0.15,
    output: 0.6,
    cacheWrite: null,
    cacheRead: null,
  },
];

type ModelRow = { name: string; display: string; input: number; output: number; cacheWrite: number | null; cacheRead: number | null };

function ModelPriceTable({ models, hasCaching }: { models: ModelRow[]; hasCaching: boolean }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs text-muted-foreground uppercase tracking-wider">
            <th className="py-2 pr-4 font-medium">Model</th>
            <th className="py-2 pr-4 font-medium text-right">Input</th>
            <th className="py-2 pr-4 font-medium text-right">Output</th>
            {hasCaching && <>
              <th className="py-2 pr-4 font-medium text-right">Cache write</th>
              <th className="py-2 pr-4 font-medium text-right">Cache read</th>
            </>}
          </tr>
        </thead>
        <tbody>
          {models.map((m) => (
            <tr key={m.name} className="border-b border-border/50 last:border-0">
              <td className="py-2.5 pr-4 font-medium">{m.display}</td>
              <td className="py-2.5 pr-4 font-mono tabular-nums text-right">${m.input.toFixed(2)}</td>
              <td className="py-2.5 pr-4 font-mono tabular-nums text-right">${m.output.toFixed(2)}</td>
              {hasCaching && <>
                <td className="py-2.5 pr-4 font-mono tabular-nums text-right text-muted-foreground">
                  {m.cacheWrite != null ? `$${m.cacheWrite.toFixed(2)}` : "—"}
                </td>
                <td className="py-2.5 pr-4 font-mono tabular-nums text-right text-muted-foreground">
                  {m.cacheRead != null ? `$${m.cacheRead.toFixed(2)}` : "—"}
                </td>
              </>}
            </tr>
          ))}
        </tbody>
      </table>
      <p className="mt-2 text-xs text-muted-foreground">Per 1M tokens. Official published rates.</p>
    </div>
  );
}

export default function PricingPage({ embedded }: { embedded?: boolean }) {
  const [groups, setGroups] = useState<PricingGroup[]>([]);

  useEffect(() => {
    api<{ groups: PricingGroup[] }>("/groups").then((r) => setGroups(r.groups || []));
  }, []);

  const content = (
    <div className="space-y-12">
      <div>
        <h1 className="font-display text-4xl font-semibold tracking-tight md:text-5xl">Pricing</h1>
        <p className="mt-3 max-w-2xl text-muted-foreground">
          Official Anthropic and OpenAI rates. We apply a billing multiplier per access group —
          a multiplier of <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">1.0</code> means
          you pay the official rate exactly.
        </p>
      </div>

      {/* Model price tables */}
      <div className="space-y-8">
        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight">Claude models</h2>
          <p className="mt-1 text-sm text-muted-foreground">Anthropic pricing with prompt-cache support.</p>
          <div className="mt-4 rounded-lg border border-border">
            <div className="p-4">
              <ModelPriceTable models={CLAUDE_MODELS} hasCaching={true} />
            </div>
          </div>
        </div>

        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight">Codex / OpenAI models</h2>
          <p className="mt-1 text-sm text-muted-foreground">OpenAI pricing via Codex endpoint.</p>
          <div className="mt-4 rounded-lg border border-border">
            <div className="p-4">
              <ModelPriceTable models={CODEX_MODELS} hasCaching={false} />
            </div>
          </div>
        </div>
      </div>

      {/* Access groups */}
      {groups.length > 0 && (
        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight">Access groups</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Each group defines per-provider multipliers applied on top of official rates.
          </p>
          <div className="mt-4 grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {groups.map((g) => (
              <Card key={g.ID} className={g.IsDefault ? "border-primary/40 bg-primary/[0.04]" : ""}>
                <CardHeader className="pb-2">
                  <div className="flex items-center justify-between">
                    <CardTitle className="font-display text-xl tracking-tight">{g.Name}</CardTitle>
                    {g.IsDefault && (
                      <span className="inline-flex items-center gap-1 rounded-full border border-primary/30 bg-primary/15 px-2 py-0.5 text-xs font-mono uppercase tracking-wider text-primary">
                        <Sparkles className="h-3 w-3" /> default
                      </span>
                    )}
                  </div>
                  {g.Description && <p className="text-xs text-muted-foreground">{g.Description}</p>}
                </CardHeader>
                <CardContent className="space-y-2">
                  <div className="flex items-center justify-between rounded-md border border-border bg-card p-3">
                    <span className="text-sm text-muted-foreground">Claude multiplier</span>
                    <span className="font-mono text-sm font-semibold">{g.ClaudeMultiplier.toFixed(2)}×</span>
                  </div>
                  <div className="flex items-center justify-between rounded-md border border-border bg-card p-3">
                    <span className="text-sm text-muted-foreground">Codex multiplier</span>
                    <span className="font-mono text-sm font-semibold">{g.CodexMultiplier.toFixed(2)}×</span>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </div>
      )}
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
