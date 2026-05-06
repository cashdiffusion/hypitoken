import { useEffect, useState } from "react";
import { CheckCircle2, AlertTriangle, XCircle, Clock, Activity } from "lucide-react";
import { PublicHeader } from "@/components/layout/shell";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

interface HealthCheck {
  id: number;
  display_name: string;
  provider: string;
  status: string;      // "ok" | "fail"
  latency_ms: number;
  error: string;
  checked_at: number;  // unix seconds
}

interface Props {
  embedded?: boolean;
}

export default function StatusPage({ embedded }: Props) {
  const [checks, setChecks] = useState<HealthCheck[]>([]);
  const [asOf, setAsOf] = useState<number>(0);
  const [loading, setLoading] = useState(true);

  const reload = async () => {
    try {
      const r = await api<{ checks: HealthCheck[]; as_of: number }>("/admin/health");
      setChecks(r.checks || []);
      setAsOf(r.as_of || 0);
    } catch {
      setChecks([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    reload();
    // Auto-refresh every 2 minutes — backend drives actual probes every 10 min
    const id = setInterval(reload, 120_000);
    return () => clearInterval(id);
  }, []);

  // Group by provider
  const claudeChecks = checks.filter((c) => c.provider === "anthropic");
  const codexChecks = checks.filter((c) => c.provider === "openai");

  const overall = computeOverall(checks);

  const content = (
    <div className="space-y-8">
      {/* Big status banner */}
      <OverallBanner overall={overall} asOf={asOf} />

      {loading && checks.length === 0 ? (
        <div className="flex items-center justify-center py-16 text-muted-foreground">
          <Activity className="mr-2 h-5 w-5 animate-pulse" />
          Loading…
        </div>
      ) : checks.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="space-y-6">
          {claudeChecks.length > 0 && (
            <ProviderSection
              name="Claude API"
              description="Anthropic — claude-haiku probe, applied to all Claude models"
              checks={claudeChecks}
            />
          )}
          {codexChecks.length > 0 && (
            <ProviderSection
              name="Codex API"
              description="OpenAI — gpt-4o-mini probe"
              checks={codexChecks}
            />
          )}
        </div>
      )}

      <p className="text-center text-xs text-muted-foreground">
        Health probes run every 10 minutes · next refresh in ~{Math.round(120 - (Date.now() / 1000 - asOf) % 120)}s
      </p>
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
      <div className="mx-auto max-w-3xl px-4 py-12 md:px-6 md:py-16">
        <h1 className="font-display text-4xl font-semibold tracking-tight md:text-5xl">System status</h1>
        <p className="mt-2 text-muted-foreground">Real-time health of upstream LLM credentials.</p>
        <div className="mt-10">{content}</div>
      </div>
    </div>
  );
}

// ─── Overall status banner (status.claude.com style) ─────────────────────────

type Overall = "operational" | "degraded" | "outage" | "unknown";

function computeOverall(checks: HealthCheck[]): Overall {
  if (checks.length === 0) return "unknown";
  const ok = checks.filter((c) => c.status === "ok").length;
  if (ok === checks.length) return "operational";
  if (ok === 0) return "outage";
  return "degraded";
}

function OverallBanner({ overall, asOf }: { overall: Overall; asOf: number }) {
  const cfg = {
    operational: {
      icon: CheckCircle2,
      label: "All systems operational",
      bar: "bg-emerald-500",
      bg: "bg-emerald-50 dark:bg-emerald-950/30 border-emerald-200 dark:border-emerald-800",
      text: "text-emerald-700 dark:text-emerald-400",
    },
    degraded: {
      icon: AlertTriangle,
      label: "Partial degradation",
      bar: "bg-amber-400",
      bg: "bg-amber-50 dark:bg-amber-950/30 border-amber-200 dark:border-amber-800",
      text: "text-amber-700 dark:text-amber-400",
    },
    outage: {
      icon: XCircle,
      label: "Major outage",
      bar: "bg-red-500",
      bg: "bg-red-50 dark:bg-red-950/30 border-red-200 dark:border-red-800",
      text: "text-red-700 dark:text-red-400",
    },
    unknown: {
      icon: Clock,
      label: "Awaiting first probe",
      bar: "bg-zinc-400",
      bg: "bg-zinc-50 dark:bg-zinc-900/50 border-zinc-200 dark:border-zinc-700",
      text: "text-zinc-600 dark:text-zinc-400",
    },
  } as const;
  const { icon: Icon, label, bar, bg, text } = cfg[overall];
  const since = asOf ? new Date(asOf * 1000).toLocaleString() : "—";

  return (
    <div className={cn("rounded-2xl border-2 p-6 md:p-8", bg)}>
      {/* Accent bar */}
      <div className={cn("mb-5 h-1.5 w-16 rounded-full", bar)} />
      <div className="flex items-start gap-4">
        <Icon className={cn("mt-0.5 h-9 w-9 shrink-0", text)} />
        <div>
          <div className={cn("font-display text-2xl font-semibold md:text-3xl", text)}>{label}</div>
          <div className="mt-1 text-sm text-muted-foreground">Updated {since}</div>
        </div>
      </div>
    </div>
  );
}

// ─── Provider section ─────────────────────────────────────────────────────────

function ProviderSection({ name, description, checks }: { name: string; description: string; checks: HealthCheck[] }) {
  const allOk = checks.every((c) => c.status === "ok");
  const anyOk = checks.some((c) => c.status === "ok");

  return (
    <div>
      {/* Section header */}
      <div className="flex items-center justify-between mb-3">
        <div>
          <h2 className="font-display text-xl font-semibold">{name}</h2>
          <p className="text-xs text-muted-foreground">{description}</p>
        </div>
        <StatusPill allOk={allOk} anyOk={anyOk} />
      </div>

      {/* Credential rows */}
      <div className="divide-y divide-border rounded-xl border border-border overflow-hidden">
        {checks.map((c) => (
          <CredRow key={c.id} check={c} />
        ))}
      </div>
    </div>
  );
}

function StatusPill({ allOk, anyOk }: { allOk: boolean; anyOk: boolean }) {
  if (allOk)
    return <span className="rounded-full border border-emerald-300 bg-emerald-50 dark:bg-emerald-950/40 dark:border-emerald-800 px-3 py-1 text-xs font-mono uppercase tracking-wider text-emerald-700 dark:text-emerald-400">Operational</span>;
  if (anyOk)
    return <span className="rounded-full border border-amber-300 bg-amber-50 dark:bg-amber-950/40 dark:border-amber-800 px-3 py-1 text-xs font-mono uppercase tracking-wider text-amber-700 dark:text-amber-400">Degraded</span>;
  return <span className="rounded-full border border-red-300 bg-red-50 dark:bg-red-950/40 dark:border-red-800 px-3 py-1 text-xs font-mono uppercase tracking-wider text-red-700 dark:text-red-400">Down</span>;
}

function CredRow({ check }: { check: HealthCheck }) {
  const ok = check.status === "ok";
  const age = check.checked_at ? Math.round((Date.now() / 1000 - check.checked_at) / 60) : null;

  return (
    <div className="flex items-center justify-between bg-card px-4 py-3 text-sm">
      <div className="flex items-center gap-3">
        {/* Status indicator */}
        <span className={cn(
          "inline-block h-2.5 w-2.5 shrink-0 rounded-full",
          ok
            ? "bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,.5)]"
            : "bg-red-500 shadow-[0_0_8px_rgba(239,68,68,.5)]"
        )} />
        <span className="font-mono text-sm font-medium">{check.display_name}</span>
        {!ok && check.error && (
          <span className="hidden max-w-xs truncate text-xs text-muted-foreground sm:block" title={check.error}>
            {check.error.slice(0, 80)}{check.error.length > 80 ? "…" : ""}
          </span>
        )}
      </div>
      <div className="flex items-center gap-5 text-xs text-muted-foreground">
        {check.latency_ms > 0 && (
          <span className={cn("font-mono tabular-nums", check.latency_ms > 5000 ? "text-amber-500" : "")}>
            {check.latency_ms}ms
          </span>
        )}
        {age !== null && (
          <span title={new Date(check.checked_at * 1000).toLocaleString()}>
            {age === 0 ? "just now" : `${age}m ago`}
          </span>
        )}
        <span className={cn(
          "rounded px-1.5 py-0.5 font-mono text-xs uppercase",
          ok ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-400"
             : "bg-red-100 text-red-700 dark:bg-red-950/60 dark:text-red-400"
        )}>
          {ok ? "ok" : "fail"}
        </span>
      </div>
    </div>
  );
}

function EmptyState() {
  return (
    <div className="rounded-xl border border-border bg-card p-12 text-center">
      <Clock className="mx-auto h-10 w-10 text-muted-foreground" />
      <h3 className="mt-4 font-display text-lg font-medium">No probes recorded yet</h3>
      <p className="mt-2 text-sm text-muted-foreground">
        Health checks run automatically every 10 minutes. Check back shortly.
      </p>
    </div>
  );
}
