import { useState, useEffect, useCallback } from "react";
import { toast } from "sonner";
import {
  LogOut,
  RefreshCw,
  Activity,
  ShieldCheck,
  Coins,
  ScrollText,
  Receipt,
} from "lucide-react";
import { api, setToken, ApiError } from "@/legacy/lib/api";
import type { AuthRow, ClientRow, Provider, Summary } from "@/legacy/lib/types";
import { OverviewPanel } from "./overview-panel";
import { CredentialsPanel } from "./credentials-panel";
import { TokensPanel } from "./tokens-panel";
import { RequestsExplorer } from "./requests-explorer";
import { PricingStats } from "./pricing-stats";
import { EditAuthModal } from "./modals/edit-auth";
import { UploadModal } from "./modals/upload";
import { APIKeyModal } from "./modals/apikey";
import { OAuthModal } from "./modals/oauth";
import { SessionCookieModal } from "./modals/session-cookie";
import { AddTokenModal } from "./modals/add-token";
import { EditTokenModal } from "./modals/edit-token";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { confirmDialog } from "@/legacy/hooks/use-confirm";
import { cn, fmtDate, fmtInt } from "@/legacy/lib/utils";

type Action = "toggle" | "refresh" | "clear-quota" | "clear-failure" | "delete";
type Tab = "overview" | "credentials" | "tokens" | "requests" | "pricing";

const TABS: { key: Tab; label: string; hint: string; icon: typeof Activity }[] = [
  { key: "overview", label: "Overview", hint: "charts · fleet health", icon: Activity },
  { key: "credentials", label: "Credentials", hint: "auth files · quota", icon: ShieldCheck },
  { key: "tokens", label: "Tokens", hint: "client keys · limits", icon: Coins },
  { key: "requests", label: "Requests", hint: "ledger · search", icon: ScrollText },
  { key: "pricing", label: "Pricing", hint: "models · savings", icon: Receipt },
];

function MetricCell({
  label,
  value,
  unit,
  hint,
  accent,
}: {
  label: string;
  value: string | number;
  unit?: string;
  hint?: string;
  accent?: boolean;
}) {
  return (
    <div className={cn("metric-cell", accent && "metric-cell-accent")}>
      <div className="relative z-10">
        <div className="eyebrow mb-2.5">{label}</div>
        <div className="flex items-baseline gap-1.5">
          <span
            className={cn(
              "font-mono text-2xl md:text-[2rem] leading-none font-medium tracking-tight tabular",
              accent ? "text-primary" : "text-foreground",
            )}
          >
            {value}
          </span>
          {unit && (
            <span className="font-mono text-xs text-muted-foreground uppercase tracking-wider">
              {unit}
            </span>
          )}
        </div>
        {hint && (
          <div className="mt-2 text-[11px] font-mono text-muted-foreground tabular">{hint}</div>
        )}
      </div>
      <span aria-hidden className="metric-cell-corner" />
      <span aria-hidden className="metric-cell-spark" />
    </div>
  );
}

export function Dashboard({ onLogout }: { onLogout: () => void }) {
  const [data, setData] = useState<Summary | null>(null);
  const [err, setErr] = useState("");
  const [editing, setEditing] = useState<AuthRow | null>(null);
  // Like oauthing/apikeying: track which provider tab initiated the upload
  // so the modal can adjust its copy + stamp the right `provider` on the
  // uploaded file. null = modal closed.
  const [uploading, setUploading] = useState<Provider | null>(null);
  // Track which provider the user is adding — the OAuth and API-key
  // modals differ per upstream (URLs, placeholder text). null = closed.
  const [oauthing, setOauthing] = useState<Provider | null>(null);
  const [apikeying, setAPIKeying] = useState<Provider | null>(null);
  const [sessionCookieOpen, setSessionCookieOpen] = useState(false);
  const [addingToken, setAddingToken] = useState(false);
  const [editingToken, setEditingToken] = useState<ClientRow | null>(null);
  const [lastTick, setLastTick] = useState(Date.now());
  const [tab, setTab] = useState<Tab>(() => {
    const stored = localStorage.getItem("cpa.admin.tab") as Tab;
    return TABS.some((t) => t.key === stored) ? stored : "overview";
  });
  useEffect(() => {
    localStorage.setItem("cpa.admin.tab", tab);
  }, [tab]);
  const [refreshTick, setRefreshTick] = useState(0);
  const [refreshing, setRefreshing] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const d = await api<Summary>("/admin/api/summary");
      setData(d);
      setErr("");
      setLastTick(Date.now());
    } catch (x: any) {
      if (x instanceof ApiError && x.status === 401) {
        setToken("");
        onLogout();
        return;
      }
      setErr(x.message);
    }
  }, [onLogout]);

  const manualRefresh = useCallback(async () => {
    setRefreshing(true);
    await refresh();
    setRefreshTick((t) => t + 1);
    setTimeout(() => setRefreshing(false), 500);
  }, [refresh]);

  useEffect(() => {
    refresh();
    // Skip the tick when the tab is hidden — no point polling dashboards
    // the operator isn't looking at, and it collapses the server-side
    // request-log scans that each refresh triggers.
    const tick = () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      refresh();
    };
    const t = setInterval(tick, 10000);
    const onVisible = () => {
      if (document.visibilityState === "visible") refresh();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(t);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [refresh]);

  const onDeleteToken = async (cl: ClientRow) => {
    if (!cl.full_token) return;
    const ok = await confirmDialog({
      title: "Delete client token",
      message: `Token "${cl.label || cl.token}" will be removed. Clients using it stop working immediately. This cannot be undone.`,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await api(`/admin/api/tokens/${encodeURIComponent(cl.full_token)}`, { method: "DELETE" });
      await refresh();
    } catch (x: any) {
      toast.error("Delete failed", { description: x.message });
    }
  };

  const onAction = async (a: AuthRow, act: Action) => {
    try {
      if (act === "toggle") {
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}`, {
          method: "PATCH",
          body: JSON.stringify({ disabled: !a.disabled }),
        });
      } else if (act === "refresh") {
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}/refresh`, { method: "POST" });
      } else if (act === "clear-quota") {
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}/clear-quota`, { method: "POST" });
      } else if (act === "clear-failure") {
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}/clear-failure`, { method: "POST" });
      } else if (act === "delete") {
        const ok = await confirmDialog({
          title: "Delete credential",
          message: `${a.label || a.id} will be removed and its JSON file deleted. Any in-flight sessions on it will fail.`,
          confirmLabel: "Delete",
          danger: true,
        });
        if (!ok) return;
        await api(`/admin/api/auths/${encodeURIComponent(a.id)}`, { method: "DELETE" });
      }
      await refresh();
    } catch (x: any) {
      toast.error("Action failed", { description: x.message });
    }
  };

  const auths = data?.auths || [];
  const oauths = auths.filter((a) => a.kind === "oauth");
  const apikeys = auths.filter((a) => a.kind === "apikey");
  const knownGroups = (() => {
    const s = new Set<string>(["public"]);
    if (data) {
      for (const a of data.auths) if (a.group) s.add(a.group);
      for (const c of data.clients) if (c.group) s.add(c.group);
    }
    return Array.from(s).sort();
  })();
  const totalCreds = auths.length;
  const healthyCreds = auths.filter((a) => a.healthy).length;
  const totals = { in: 0, out: 0, in24: 0 };
  for (const a of auths) {
    const t = a.usage?.total;
    if (t) {
      totals.in += t.input_tokens || 0;
      totals.out += t.output_tokens || 0;
    }
    const h24 = a.usage?.sum_24h;
    if (h24) totals.in24 += h24.input_tokens || 0;
  }
  return (
    <div className="relative min-h-screen pb-16">
      <div className="max-w-[1440px] mx-auto px-4 sm:px-6 lg:px-10 py-6 md:py-9 space-y-8 md:space-y-10">
        {/* MASTHEAD */}
        <header className="stagger space-y-5">
          <div className="flex items-center justify-between gap-4">
            <div className="eyebrow flex items-center gap-2.5">
              <span className="relative inline-flex h-2 w-2">
                <span className="absolute inline-flex h-full w-full rounded-full bg-primary opacity-75 animate-ping" />
                <span className="relative inline-flex rounded-full h-2 w-2 bg-primary" />
              </span>
              <span>CPA · Claude / Control Console</span>
            </div>
            <div className="flex items-center gap-2">
              <ThemeToggle />
              <Button
                variant="outline"
                size="icon"
                onClick={() => {
                  setToken("");
                  onLogout();
                }}
                aria-label="Logout"
                title="Logout"
                className="border-border-strong bg-card/60 hover:bg-card"
              >
                <LogOut className="h-4 w-4" />
              </Button>
            </div>
          </div>

          <div className="flex flex-col lg:flex-row lg:items-end lg:justify-between gap-5 pt-1">
            <div className="space-y-2.5 max-w-3xl">
              <h1 className="font-display text-4xl sm:text-5xl lg:text-6xl leading-[0.95] tracking-tight">
                Operator <span className="text-primary">console</span>.
              </h1>
              <p className="text-sm lg:text-base text-muted-foreground max-w-2xl">
                Active rotation window{" "}
                <span className="mono tabular text-foreground">
                  {data ? data.active_window_minutes : "···"}
                </span>{" "}
                min
                {data?.default_proxy_url && (
                  <>
                    {" · default proxy "}
                    <span className="mono text-foreground break-all">{data.default_proxy_url}</span>
                  </>
                )}
              </p>
            </div>

            <div className="flex items-center gap-2 lg:justify-end">
              <Button
                variant="outline"
                onClick={manualRefresh}
                className="gap-2"
                aria-label="Refresh"
              >
                <RefreshCw
                  className={cn("h-4 w-4 transition-transform", refreshing && "animate-spin")}
                />
                <span className="hidden sm:inline">Refresh</span>
              </Button>
              <span className="eyebrow tabular opacity-60 hidden md:inline">
                last · {fmtDate(new Date(lastTick).toISOString())}
              </span>
            </div>
          </div>

          {err && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive font-mono animate-in fade-in slide-in-from-top-1">
              {err}
            </div>
          )}
        </header>

        <datalist id="groups-datalist">
          {knownGroups.map((g) => (
            <option key={g} value={g} />
          ))}
        </datalist>

        {/* METRICS STRIP */}
        <section className="stagger">
          <div className="hud-strip">
            <div className="hud-strip-grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5">
            <MetricCell
              label="Credentials"
              value={`${healthyCreds}`}
              unit={`/ ${totalCreds}`}
              hint="healthy"
              accent
            />
            <MetricCell label="OAuth" value={fmtInt(oauths.length)} />
            <MetricCell label="API keys" value={fmtInt(apikeys.length)} />
            <MetricCell label="Σ in" value={fmtInt(totals.in24)} unit="tok" />
            <MetricCell label="Σ out" value={fmtInt(totals.out)} unit="tok" />
            </div>
          </div>
        </section>

        {/* TAB NAV */}
        <nav
          role="tablist"
          aria-label="Sections"
          className="border-b border-border-strong"
        >
          <div className="flex overflow-x-auto gap-x-1 gap-y-2 items-end no-scrollbar">
            {TABS.map(({ key, label, hint, icon: Icon }) => {
              const active = tab === key;
              return (
                <button
                  key={key}
                  role="tab"
                  aria-selected={active}
                  onClick={() => setTab(key)}
                  className={cn(
                    "group relative px-3.5 md:px-5 py-3 -mb-px transition-colors flex items-baseline gap-2.5 shrink-0 whitespace-nowrap",
                    active ? "text-foreground" : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  <Icon className="h-4 w-4 self-center" />
                  <span
                    className={cn(
                      "font-display text-base md:text-xl lg:text-2xl tracking-tight",
                      active && "font-medium",
                    )}
                  >
                    {label}
                  </span>
                  <span className="eyebrow hidden lg:inline opacity-60">{hint}</span>
                  <span
                    className={cn(
                      "absolute inset-x-0 bottom-0 h-[2px] bg-primary transition-transform origin-left",
                      active ? "scale-x-100" : "scale-x-0 group-hover:scale-x-50",
                    )}
                  />
                </button>
              );
            })}
          </div>
        </nav>

        {/* TAB PANELS */}
        <div className="stagger pt-2 md:pt-4">
          {tab === "overview" && <OverviewPanel summary={data} pricing={data?.pricing} refreshTick={refreshTick} />}
          {tab === "credentials" && (
            <CredentialsPanel
              summary={data}
              onAction={onAction}
              onEdit={setEditing}
              onAddOAuth={(p) => setOauthing(p)}
              onAddAPIKey={(p) => setAPIKeying(p)}
              onUpload={(p) => setUploading(p)}
              onAddSessionCookie={() => setSessionCookieOpen(true)}
            />
          )}
          {tab === "tokens" && (
            <TokensPanel
              summary={data}
              onAdd={() => setAddingToken(true)}
              onEdit={setEditingToken}
              onDelete={onDeleteToken}
            />
          )}
          {tab === "requests" && (
            <RequestsExplorer refreshTick={refreshTick} pricing={data?.pricing} />
          )}
          {tab === "pricing" && (
            <section className="space-y-8">
              <div className="flex items-end justify-between gap-4 flex-wrap">
                <div>
                  <div className="eyebrow mb-1.5">§ Pricing</div>
                  <h2 className="font-display text-3xl md:text-4xl tracking-tight">
                    Model rate{" "}
                    <span className="text-muted-foreground">
                      · {data?.pricing ? Object.keys(data.pricing.models).length : "···"} models
                    </span>
                  </h2>
                </div>
                <span className="eyebrow opacity-70">edit in config.yaml / pricing.models</span>
              </div>
              <PricingStats pricing={data?.pricing} refreshTick={refreshTick} />
              <div className="bg-card border border-border-strong rounded-md overflow-hidden">
                {data?.pricing && (
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead className="text-left border-b border-border-strong">
                        <tr className="eyebrow">
                          <th className="py-3 px-4 font-[inherit]">Provider</th>
                          <th className="py-3 px-4 font-[inherit]">Model</th>
                          <th className="py-3 px-4 font-[inherit]">Input / 1M</th>
                          <th className="py-3 px-4 font-[inherit]">Output / 1M</th>
                          <th className="py-3 px-4 font-[inherit]">Cache-read / 1M</th>
                          <th className="py-3 px-4 font-[inherit]">Cache-create / 1M</th>
                        </tr>
                      </thead>
                      <tbody>
                        {Object.entries(data.pricing.models)
                          .sort(([a], [b]) => a.localeCompare(b))
                          .map(([name, p]) => {
                            // Catalog keys are "provider/model"; legacy bare
                            // keys default to Anthropic.
                            const slash = name.indexOf("/");
                            const prov = slash > 0 ? name.slice(0, slash) : "anthropic";
                            const model = slash > 0 ? name.slice(slash + 1) : name;
                            return (
                            <tr
                              key={name}
                              className="border-b border-border last:border-0 hover:bg-muted/50 transition-colors"
                            >
                              <td className="py-2.5 px-4 mono text-xs uppercase opacity-70">{prov}</td>
                              <td className="py-2.5 px-4 mono text-sm">{model}</td>
                              <td className="py-2.5 px-4 mono text-sm">
                                ${p.input_per_1m.toFixed(2)}
                              </td>
                              <td className="py-2.5 px-4 mono text-sm">
                                ${p.output_per_1m.toFixed(2)}
                              </td>
                              <td className="py-2.5 px-4 mono text-sm">
                                ${p.cache_read_per_1m.toFixed(2)}
                              </td>
                              <td className="py-2.5 px-4 mono text-sm">
                                ${p.cache_create_per_1m.toFixed(2)}
                              </td>
                            </tr>
                          );
                          })}
                        <tr className="bg-muted/50">
                          <td className="py-2.5 px-4 mono text-xs uppercase opacity-70">—</td>
                          <td className="py-2.5 px-4 mono text-sm text-muted-foreground">
                            (default / fallback)
                          </td>
                          <td className="py-2.5 px-4 mono text-sm">
                            ${data.pricing.default.input_per_1m.toFixed(2)}
                          </td>
                          <td className="py-2.5 px-4 mono text-sm">
                            ${data.pricing.default.output_per_1m.toFixed(2)}
                          </td>
                          <td className="py-2.5 px-4 mono text-sm">
                            ${data.pricing.default.cache_read_per_1m.toFixed(2)}
                          </td>
                          <td className="py-2.5 px-4 mono text-sm">
                            ${data.pricing.default.cache_create_per_1m.toFixed(2)}
                          </td>
                        </tr>
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            </section>
          )}
        </div>

        <footer className="pt-8 mt-6 border-t border-border eyebrow flex justify-between items-center flex-wrap gap-2">
          <span>v1.0 · mutable credentials + client tokens · config.yaml entries are read-only</span>
          <span className="opacity-60">CPA · Claude / {new Date().getFullYear()}</span>
        </footer>
      </div>

      {editing && (
        <EditAuthModal
          auth={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            refresh();
          }}
        />
      )}
      {uploading && (
        <UploadModal
          provider={uploading}
          onClose={() => setUploading(null)}
          onSaved={() => {
            setUploading(null);
            refresh();
          }}
        />
      )}
      {oauthing && (
        <OAuthModal
          provider={oauthing}
          onClose={() => setOauthing(null)}
          onSaved={() => {
            setOauthing(null);
            refresh();
          }}
        />
      )}
      {sessionCookieOpen && (
        <SessionCookieModal
          onClose={() => setSessionCookieOpen(false)}
          onSaved={() => {
            setSessionCookieOpen(false);
            refresh();
          }}
        />
      )}
      {apikeying && (
        <APIKeyModal
          provider={apikeying}
          onClose={() => setAPIKeying(null)}
          onSaved={() => {
            setAPIKeying(null);
            refresh();
          }}
        />
      )}
      {addingToken && (
        <AddTokenModal
          onClose={() => setAddingToken(false)}
          onSaved={() => {
            setAddingToken(false);
            refresh();
          }}
        />
      )}
      {editingToken && (
        <EditTokenModal
          row={editingToken}
          onClose={() => setEditingToken(null)}
          onSaved={() => {
            setEditingToken(null);
            refresh();
          }}
        />
      )}
    </div>
  );
}
