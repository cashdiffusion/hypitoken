import { useEffect, useState } from "react";
import { CheckCircle2, AlertTriangle, XCircle, Clock, Activity } from "lucide-react";
import { PublicHeader } from "@/components/layout/shell";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

interface HistoryPoint {
  status: string;
  latency_ms: number;
  checked_at: number;
}

interface HealthCheck {
  id: number;
  display_name: string;
  provider: string;
  status: string;      // "ok" | "fail"
  latency_ms: number;
  error: string;
  checked_at: number;  // unix seconds
  history: HistoryPoint[];
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
      // Public endpoint — no auth required so visitors landing on /status
      // can see upstream health without signing in. Mirror of /admin/health
      // minus the operator-only error strings + refresh trigger.
      const r = await api<{ checks: HealthCheck[]; as_of: number }>("/health");
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
    const id = setInterval(reload, 120_000);
    return () => clearInterval(id);
  }, []);

  const claudeChecks = checks.filter((c) => c.provider === "anthropic");
  const codexChecks = checks.filter((c) => c.provider === "openai");

  const overall = computeOverall(checks);

  const content = (
    <div className="space-y-8">
      <OverallBanner overall={overall} asOf={asOf} />

      {loading && checks.length === 0 ? (
        <div className="flex items-center justify-center py-16 text-muted-foreground">
          <Activity className="mr-2 h-5 w-5 animate-pulse" />
          Loading…
        </div>
      ) : checks.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="space-y-8">
          <p className="text-right text-xs text-muted-foreground">
            Uptime over the past {SLOT_COUNT} probes · refreshed every 10 min
          </p>
          {claudeChecks.length > 0 && (
            <ProviderSection
              name="Claude API"
              description="Anthropic — claude-haiku probe, covers all Claude models"
              checks={claudeChecks}
            />
          )}
          {codexChecks.length > 0 && (
            <ProviderSection
              name="Codex API"
              description="OpenAI — gpt-5.5 probe (streaming /responses)"
              checks={codexChecks}
            />
          )}
        </div>
      )}

      <p className="text-center text-xs text-muted-foreground">
        Health probes run every 10 minutes · auto-refresh in ~{Math.max(0, Math.round(120 - (Date.now() / 1000 - asOf) % 120))}s
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

// ─── Overall status banner ────────────────────────────────────────────────────

type Overall = "operational" | "degraded" | "outage" | "unknown";

function computeOverall(checks: HealthCheck[]): Overall {
  if (checks.length === 0) return "unknown";
  const ok = checks.filter((c) => c.status === "ok").length;
  if (ok === checks.length) return "operational";
  if (ok === 0) return "outage";
  return "degraded";
}

function OverallBanner({ overall, asOf }: { overall: Overall; asOf: number }) {
  // Solid colored block — matches status.claude.com's "All Systems Operational" header.
  const cfg = {
    operational: { icon: CheckCircle2, label: "All Systems Operational", bg: "bg-[#76ad2a]" },
    degraded:    { icon: AlertTriangle, label: "Partial Service Degradation", bg: "bg-[#eaa82a]" },
    outage:      { icon: XCircle,       label: "Major Service Outage",        bg: "bg-[#e04343]" },
    unknown:     { icon: Clock,         label: "Awaiting First Probe",        bg: "bg-zinc-500" },
  } as const;
  const { icon: Icon, label, bg } = cfg[overall];
  const since = asOf ? new Date(asOf * 1000).toLocaleString() : "—";

  return (
    <div className={cn("rounded-md px-5 py-4 text-white shadow-sm", bg)}>
      <div className="flex items-center gap-3">
        <Icon className="h-5 w-5 shrink-0" />
        <span className="text-base font-medium md:text-lg">{label}</span>
        <span className="ml-auto hidden text-xs text-white/80 sm:block">Updated {since}</span>
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
      <div className="flex items-center justify-between mb-3">
        <div>
          <h2 className="font-display text-xl font-semibold">{name}</h2>
          <p className="text-xs text-muted-foreground">{description}</p>
        </div>
        <StatusPill allOk={allOk} anyOk={anyOk} />
      </div>

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

// ─── Credential row with status.claude.com-style uptime bars ─────────────────

// Match status.claude.com: 90 slots, bar width 3, gap 2 → viewBox width 448 (90*5 - 2).
const SLOT_COUNT = 90;
const BAR_W = 3;
const BAR_GAP = 2;
const BAR_H = 34;
const VB_W = SLOT_COUNT * (BAR_W + BAR_GAP) - BAR_GAP; // 448

// Color palette lifted directly from status.claude.com SVG.
const FILL_OK = "#76ad2a";    // operational green
const FILL_FAIL = "#e04343";  // outage red
const FILL_NONE = "#B0AEA5";  // no data gray

function CredRow({ check }: { check: HealthCheck }) {
  const ok = check.status === "ok";
  const age = check.checked_at ? Math.round((Date.now() / 1000 - check.checked_at) / 60) : null;

  return (
    <div className="bg-card px-5 py-4">
      {/* Top row: name + current status */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <span className="font-mono text-sm font-medium text-foreground">{check.display_name}</span>
          {!ok && check.error && (
            <span className="hidden max-w-xs truncate text-xs text-muted-foreground sm:block" title={check.error}>
              {check.error.slice(0, 80)}{check.error.length > 80 ? "…" : ""}
            </span>
          )}
        </div>
        <div className="flex items-center gap-4 text-xs">
          {ok && check.latency_ms > 0 && (
            <span className={cn(
              "font-mono tabular-nums text-muted-foreground",
              check.latency_ms > 5000 ? "text-amber-500" : ""
            )}>
              {check.latency_ms}ms
            </span>
          )}
          {age !== null && (
            <span className="text-muted-foreground" title={new Date(check.checked_at * 1000).toLocaleString()}>
              {age === 0 ? "just now" : `${age}m ago`}
            </span>
          )}
          <span className={cn(
            "font-medium",
            ok ? "text-emerald-700 dark:text-emerald-400" : "text-red-600 dark:text-red-400"
          )}>
            {ok ? "Operational" : "Down"}
          </span>
        </div>
      </div>

      {/* Uptime bar */}
      <UptimeBar history={check.history} />
    </div>
  );
}

function UptimeBar({ history }: { history: HistoryPoint[] }) {
  // Pad on the left: oldest data on the left, newest on the right.
  const padded: (HistoryPoint | null)[] = [
    ...Array(Math.max(0, SLOT_COUNT - history.length)).fill(null),
    ...history.slice(-SLOT_COUNT),
  ];

  const uptimePct = history.length === 0
    ? null
    : ((history.filter(h => h.status === "ok").length / history.length) * 100);

  return (
    <div className="mt-3">
      {/* SVG bar — preserveAspectRatio=none lets the 448-wide viewBox stretch
          to the container's full width while keeping bars as crisp rects. */}
      <svg
        className="block h-[34px] w-full"
        preserveAspectRatio="none"
        viewBox={`0 0 ${VB_W} ${BAR_H}`}
        height={BAR_H}
      >
        {padded.map((pt, i) => {
          const x = i * (BAR_W + BAR_GAP);
          const fill = pt == null ? FILL_NONE : pt.status === "ok" ? FILL_OK : FILL_FAIL;
          let title = "No data";
          if (pt) {
            const ts = new Date(pt.checked_at * 1000).toLocaleString();
            title = pt.status === "ok"
              ? `${ts} — operational (${pt.latency_ms}ms)`
              : `${ts} — failed`;
          }
          return (
            <rect key={i} x={x} y={0} width={BAR_W} height={BAR_H} fill={fill}>
              <title>{title}</title>
            </rect>
          );
        })}
      </svg>

      {/* Bottom row: "older — X% uptime — newer" with flanking rules */}
      <div className="mt-2 flex items-center gap-3 text-[11px] text-muted-foreground">
        <span className="shrink-0">older</span>
        <span className="h-px flex-1 bg-border" />
        <span className="shrink-0 font-mono tabular-nums">
          {uptimePct === null ? "awaiting data" : `${uptimePct.toFixed(2)} % uptime`}
        </span>
        <span className="h-px flex-1 bg-border" />
        <span className="shrink-0">now</span>
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
