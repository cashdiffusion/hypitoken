import { useEffect, useState } from "react";
import { CheckCircle2, AlertCircle, RefreshCw, Activity } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { PublicHeader } from "@/components/layout/shell";
import { api } from "@/lib/api";
import type { ModelHealth } from "@/lib/types";
import { useAuth } from "@/hooks/use-auth";

interface Props {
  embedded?: boolean;
}

export default function StatusPage({ embedded }: Props) {
  const [checks, setChecks] = useState<ModelHealth[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const { user } = useAuth();

  const reload = async () => {
    try {
      // Public-ish endpoint: this requires admin role behind /api/v2/admin/health.
      // Non-admins will see an error — fall back to gracefully showing empty.
      const r = await api<{ checks: ModelHealth[] }>("/admin/health");
      setChecks(r.checks || []);
    } catch {
      setChecks([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    reload();
  }, []);

  const overall = computeOverall(checks);

  // Group by model
  const byModel: Record<string, ModelHealth[]> = {};
  checks.forEach((c) => {
    if (!byModel[c.model]) byModel[c.model] = [];
    byModel[c.model]!.push(c);
  });

  const content = (
    <div className="space-y-8">
      <BigStatusBanner overall={overall} />

      {user?.role === "admin" && (
        <div className="flex justify-end">
          <Button variant="outline" size="sm" onClick={async () => {
            setRefreshing(true);
            try {
              await api("/admin/health/refresh", { method: "POST" });
              await new Promise((r) => setTimeout(r, 2000));
              await reload();
            } finally {
              setRefreshing(false);
            }
          }} className="gap-2" disabled={refreshing}>
            <RefreshCw className={`h-3.5 w-3.5 ${refreshing ? "animate-spin" : ""}`} /> {refreshing ? "Probing…" : "Probe now"}
          </Button>
        </div>
      )}

      {checks.length === 0 ? (
        <Card>
          <CardContent className="p-12 text-center">
            <Activity className="mx-auto h-10 w-10 text-muted-foreground" />
            <h3 className="mt-4 font-display text-lg font-medium">No checks recorded yet</h3>
            <p className="mt-2 text-sm text-muted-foreground">Probes run every 30 minutes against API-key credentials. OAuth credentials use the live error feed instead.</p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-4">
          {Object.keys(byModel).sort().map((model) => (
            <Card key={model} className="overflow-hidden">
              <CardContent className="p-0">
                <div className="flex items-center justify-between border-b border-border bg-muted/40 px-5 py-3">
                  <div className="flex items-center gap-3">
                    <ModelIcon checks={byModel[model]!} />
                    <div>
                      <div className="font-display text-lg font-medium">{model}</div>
                      <div className="text-xs text-muted-foreground">{byModel[model]!.length} credential{byModel[model]!.length === 1 ? "" : "s"}</div>
                    </div>
                  </div>
                  <ModelStatusBadge checks={byModel[model]!} />
                </div>
                <div className="divide-y divide-border">
                  {byModel[model]!.map((c) => (
                    <div key={c.id} className="flex items-center justify-between px-5 py-3 text-sm">
                      <div className="flex items-center gap-3">
                        <StatusDot ok={c.status === "ok"} />
                        <code className="font-mono text-xs text-muted-foreground">{c.auth_id}</code>
                      </div>
                      <div className="flex items-center gap-6 text-xs text-muted-foreground">
                        {c.latency_ms > 0 && <span className="font-mono tabular-nums">{c.latency_ms}ms</span>}
                        <span>{new Date(c.checked_at * 1000).toLocaleString()}</span>
                      </div>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );

  if (embedded) {
    return (
      <div className="space-y-4">
        <h1 className="font-display text-3xl font-semibold tracking-tight">Status</h1>
        {content}
      </div>
    );
  }
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <PublicHeader />
      <div className="mx-auto max-w-5xl px-4 py-12 md:px-6 md:py-16">
        <h1 className="font-display text-4xl font-semibold tracking-tight md:text-5xl">System status</h1>
        <p className="mt-2 text-muted-foreground">Live health for upstream LLM credentials.</p>
        <div className="mt-10">{content}</div>
      </div>
    </div>
  );
}

function computeOverall(checks: ModelHealth[]): "operational" | "degraded" | "outage" | "unknown" {
  if (checks.length === 0) return "unknown";
  const ok = checks.filter((c) => c.status === "ok").length;
  const total = checks.length;
  if (ok === total) return "operational";
  if (ok === 0) return "outage";
  return "degraded";
}

function BigStatusBanner({ overall }: { overall: "operational" | "degraded" | "outage" | "unknown" }) {
  const map = {
    operational: { label: "All systems operational", color: "text-success", bg: "bg-success/10 border-success/30", icon: CheckCircle2 },
    degraded: { label: "Partial degradation", color: "text-warning", bg: "bg-warning/10 border-warning/30", icon: AlertCircle },
    outage: { label: "Major outage", color: "text-destructive", bg: "bg-destructive/10 border-destructive/30", icon: AlertCircle },
    unknown: { label: "No data yet", color: "text-muted-foreground", bg: "bg-muted/40 border-border-strong", icon: Activity },
  } as const;
  const { label, color, bg, icon: Icon } = map[overall];
  return (
    <div className={`rounded-xl border-2 p-6 md:p-8 ${bg}`}>
      <div className="flex items-center gap-4">
        <Icon className={`h-10 w-10 md:h-12 md:w-12 ${color}`} />
        <div>
          <div className={`font-display text-2xl md:text-3xl font-semibold ${color}`}>{label}</div>
          <div className="mt-1 text-sm text-muted-foreground">As of {new Date().toLocaleString()}</div>
        </div>
      </div>
    </div>
  );
}

function StatusDot({ ok }: { ok: boolean }) {
  return <span className={`inline-block h-2.5 w-2.5 rounded-full ${ok ? "bg-success shadow-[0_0_8px_color-mix(in_oklch,var(--color-success)_70%,transparent)]" : "bg-destructive shadow-[0_0_8px_color-mix(in_oklch,var(--color-destructive)_70%,transparent)]"}`} />;
}

function ModelIcon({ checks }: { checks: ModelHealth[] }) {
  const ok = checks.every((c) => c.status === "ok");
  return ok ? <CheckCircle2 className="h-5 w-5 text-success" /> : <AlertCircle className="h-5 w-5 text-warning" />;
}

function ModelStatusBadge({ checks }: { checks: ModelHealth[] }) {
  const ok = checks.filter((c) => c.status === "ok").length;
  const total = checks.length;
  if (ok === total) return <span className="rounded-full border border-success/30 bg-success/15 px-3 py-1 text-xs font-mono uppercase tracking-wider text-success">operational</span>;
  if (ok === 0) return <span className="rounded-full border border-destructive/30 bg-destructive/15 px-3 py-1 text-xs font-mono uppercase tracking-wider text-destructive">down</span>;
  return <span className="rounded-full border border-warning/30 bg-warning/15 px-3 py-1 text-xs font-mono uppercase tracking-wider text-warning">{ok}/{total} ok</span>;
}
