import { AlertTriangle, KeyRound, PauseCircle, Search, ShieldCheck, X } from "lucide-react";
import { motion } from "motion/react";
import {
  lazy,
  Suspense,
  useCallback,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { FadeIn } from "@/components/admin/fade-in";
import { CountUp, GlassPanel } from "@/components/app/page-primitives";
import { Pager } from "@/components/app/pager";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm-dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { apiDelete, apiGet, apiPatch, apiPost } from "@/lib/api";
import type { Credential } from "@/lib/types";
import { cn, errMsg } from "@/lib/utils";
import { AddAPIKeyDialog } from "./add-apikey-dialog";
import { AddOAuthDialog } from "./add-oauth-dialog";
import { type CardAction, CredentialCard } from "./credential-card";
import { CredentialDetailDialog } from "./credential-detail-dialog";
import { type CredStatusKey, credStatus } from "./credential-status";
import { EditCredentialDialog } from "./edit-credential-dialog";
import { UploadJSONDialog } from "./upload-json-dialog";

const ParticleField = lazy(() => import("@/components/landing/particle-field"));

const PAGE = 12;
// 30s, not 15s. The list endpoint aggregates the request log; at 15s it lined
// up with the server's cache window so every poll paid the full aggregate, and
// each response re-rendered the whole card grid. Nothing on this page is
// time-critical — a cleared cooldown showing 30s late is fine.
const POLL_MS = 30_000;
const HEALTH_FILTERS = [
  "all",
  "healthy",
  "quota",
  "throttled",
  "paused",
  "hardFail",
  "disabled",
] as const;
type HealthFilter = (typeof HEALTH_FILTERS)[number];
const SORTS = ["default", "recent", "usage24h", "cost", "name"] as const;
type Sort = (typeof SORTS)[number];

type Provider = "anthropic" | "openai";

// reuseUnchanged carries the previous object forward for any credential whose
// serialized form is identical. Every poll returns a freshly parsed array, so
// without this CredentialCard's memo never holds — shallow-comparing `cred`
// against a brand-new object always reports a change, and the whole grid
// re-renders every 30s even when nothing moved. Most polls change nothing, so
// this usually reduces the commit to zero cards.
function reuseUnchanged(prev: Credential[], next: Credential[]): Credential[] {
  if (prev.length === 0) return next;
  const byID = new Map(prev.map((c) => [c.id, c]));
  let reused = 0;
  const merged = next.map((c) => {
    const old = byID.get(c.id);
    if (old && JSON.stringify(old) === JSON.stringify(c)) {
      reused++;
      return old;
    }
    return c;
  });
  // Nothing moved at all — hand back the identical array so React bails out of
  // the state update entirely and the poll costs zero render work.
  if (reused === next.length && next.length === prev.length) return prev;
  return merged;
}

export function CredentialsTab() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [creds, setCreds] = useState<Credential[]>([]);
  const [openKey, setOpenKey] = useState(false);
  const [openOAuth, setOpenOAuth] = useState<null | Provider>(null);
  const [openUpload, setOpenUpload] = useState(false);
  const [detail, setDetail] = useState<Credential | null>(null);
  const [editing, setEditing] = useState<Credential | null>(null);
  const [providerTab, setProviderTab] = useState<Provider>("anthropic");
  const [busyId, setBusyId] = useState("");

  const [q, setQ] = useState("");
  const [health, setHealth] = useState<HealthFilter>("all");
  const [group, setGroup] = useState("all");
  const [sort, setSort] = useState<Sort>("default");
  // Deferred so typing never blocks on re-filtering a large fleet; no network
  // call is involved, the whole list already lives in memory.
  const query = useDeferredValue(q);

  const [oauthOffset, setOauthOffset] = useState(0);
  const [keyOffset, setKeyOffset] = useState(0);

  // Guards against overlapping polls: the list endpoint can take a second on a
  // cold cache, and without this a slow response let the next tick stack a
  // second request on top of it.
  const inFlight = useRef(false);
  const reload = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      const r = await apiGet<{ credentials: Credential[] }>("/admin/credentials");
      setCreds((prev) => reuseUnchanged(prev, r.credentials || []));
    } finally {
      inFlight.current = false;
    }
  }, []);

  // Load on mount + poll so an expired quota cooldown (cleared server-side)
  // stops showing a stale badge without a manual reload. Polling only swaps
  // `creds`; filters, paging and open dialogs are separate state.
  //
  // The tick is skipped while the tab is hidden and a fresh load fires on
  // re-show — same shape as the console dashboard. A background tab used to
  // keep hammering a request that costs the server real work.
  useEffect(() => {
    reload();
    const tick = () => {
      if (document.visibilityState === "hidden") return;
      reload();
    };
    const id = setInterval(tick, POLL_MS);
    const onVisible = () => {
      if (document.visibilityState === "visible") reload();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [reload]);

  // useCallback so CredentialCard's memo actually holds — a fresh function
  // identity here would re-render every card on every parent render.
  const runAction = useCallback(
    async (c: Credential, kind: CardAction) => {
      setBusyId(c.id);
      try {
        if (kind === "toggle") {
          await apiPatch(`/admin/credentials/${encodeURIComponent(c.id)}`, {
            disabled: !c.disabled,
          });
          toast.success(
            c.disabled ? t("admin.creds.toast.enabled") : t("admin.creds.toast.disabled"),
          );
        } else {
          await apiPost(`/admin/credentials/${encodeURIComponent(c.id)}/${kind}`);
          toast.success(
            kind === "refresh"
              ? t("admin.creds.toast.refreshed")
              : kind === "clear-quota"
                ? t("admin.creds.toast.quotaCleared")
                : t("admin.creds.toast.markedHealthy"),
          );
        }
        await reload();
      } catch (e) {
        toast.error(errMsg(e));
      } finally {
        setBusyId("");
      }
    },
    [reload, t],
  );

  const remove = useCallback(
    async (c: Credential) => {
      if (
        !(await confirm({
          title: t("common.delete"),
          description: t("admin.creds.confirmRemove", { name: c.label }),
          confirmLabel: t("common.delete"),
          destructive: true,
        }))
      )
        return;
      try {
        await apiDelete(`/admin/credentials/${encodeURIComponent(c.id)}`);
        toast.success(t("admin.creds.removed"));
        await reload();
      } catch (e) {
        toast.error(errMsg(e));
      }
    },
    [confirm, reload, t],
  );

  const byProvider = useMemo(
    () => ({
      anthropic: creds.filter((c) => c.provider === "anthropic"),
      openai: creds.filter((c) => c.provider === "openai"),
    }),
    [creds],
  );

  const scoped = byProvider[providerTab];

  // Fleet summary is computed from the *unfiltered* provider slice, so the
  // numbers stay a health readout rather than a restatement of the filters.
  const summary = useMemo(() => {
    let healthy = 0;
    let cooling = 0;
    let failed = 0;
    let active = 0;
    let capacity = 0;
    for (const c of scoped) {
      const k = credStatus(c).key;
      if (k === "healthy") healthy++;
      else if (k === "quota" || k === "throttled" || k === "paused" || k === "cooldown") cooling++;
      else if (k === "hardFail") failed++;
      active += c.active_clients;
      capacity += c.max_concurrent;
    }
    return { total: scoped.length, healthy, cooling, failed, active, capacity };
  }, [scoped]);

  const groups = useMemo(() => {
    const s = new Set<string>();
    for (const c of scoped) if (c.group) s.add(c.group);
    return [...s].sort();
  }, [scoped]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const out = scoped.filter((c) => {
      if (health !== "all" && credStatus(c).key !== (health as CredStatusKey)) return false;
      if (group !== "all" && (c.group || "") !== (group === "__none" ? "" : group)) return false;
      if (!needle) return true;
      return [c.label, c.id, c.email, c.group, c.proxy_url, c.base_url]
        .filter(Boolean)
        .some((v) => (v as string).toLowerCase().includes(needle));
    });
    const tokens24h = (c: Credential) =>
      (c.usage?.sum_24h.input_tokens || 0) + (c.usage?.sum_24h.output_tokens || 0);
    const sorted = [...out];
    if (sort === "recent")
      sorted.sort(
        (a, b) =>
          new Date(b.usage?.last_used || 0).getTime() - new Date(a.usage?.last_used || 0).getTime(),
      );
    else if (sort === "usage24h") sorted.sort((a, b) => tokens24h(b) - tokens24h(a));
    else if (sort === "cost")
      sorted.sort((a, b) => (b.usage?.total_cost_usd || 0) - (a.usage?.total_cost_usd || 0));
    else if (sort === "name") sorted.sort((a, b) => a.label.localeCompare(b.label));
    return sorted;
  }, [scoped, query, health, group, sort]);

  const oauths = useMemo(() => filtered.filter((c) => c.kind === "oauth"), [filtered]);
  const apikeys = useMemo(() => filtered.filter((c) => c.kind !== "oauth"), [filtered]);

  const filtersActive = query.trim() !== "" || health !== "all" || group !== "all";
  const resetPaging = () => {
    setOauthOffset(0);
    setKeyOffset(0);
  };
  const clearFilters = () => {
    setQ("");
    setHealth("all");
    setGroup("all");
    resetPaging();
  };

  const renderSection = (
    key: "oauth" | "apikey",
    items: Credential[],
    offset: number,
    setOffset: (n: number) => void,
  ) => (
    <section className="space-y-3">
      <div className="flex items-baseline gap-2">
        <span className="eyebrow">
          {key === "oauth" ? t("admin.creds.sections.oauth") : t("admin.creds.sections.apikey")}
        </span>
        <span className="mono text-xs text-muted-foreground tabular-nums">{items.length}</span>
        <span className="hidden text-xs text-muted-foreground sm:inline">
          {key === "oauth"
            ? t("admin.creds.sections.oauthSub")
            : t("admin.creds.sections.apikeySub")}
        </span>
      </div>
      {items.length === 0 ? (
        <p className="rounded-xl border border-dashed border-border/60 px-5 py-8 text-center text-sm text-muted-foreground">
          {filtersActive ? t("admin.creds.filters.noMatch") : t("admin.creds.sections.empty")}
        </p>
      ) : (
        <>
          <div
            key={`${providerTab}-${key}-${offset}`}
            className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3"
          >
            {items.slice(offset, offset + PAGE).map((c) => (
              <div key={c.id} className="h-full">
                <CredentialCard
                  cred={c}
                  busy={busyId === c.id}
                  onEdit={setEditing}
                  onDetail={setDetail}
                  onDelete={remove}
                  onAction={runAction}
                />
              </div>
            ))}
          </div>
          <Pager
            offset={offset}
            limit={PAGE}
            total={items.length}
            count={items.slice(offset, offset + PAGE).length}
            onChange={setOffset}
          />
        </>
      )}
    </section>
  );

  return (
    <FadeIn className="space-y-5">
      {/* Fleet summary — the ambient particle layer is the one 3D element on
          this page; it sits behind the numbers and never enters the grid. */}
      <div>
        <div className="glass relative overflow-hidden rounded-2xl p-5 md:p-6">
          <div
            aria-hidden
            className="pointer-events-none absolute inset-0 opacity-[0.45] [mask-image:radial-gradient(120%_120%_at_85%_-10%,black,transparent_70%)]"
          >
            <Suspense fallback={null}>
              <ParticleField color="#34d399" count={900} />
            </Suspense>
          </div>
          <div
            aria-hidden
            className="pointer-events-none absolute -right-16 -top-16 h-56 w-56 rounded-full bg-primary/10 blur-3xl"
          />
          <div className="relative">
            <span className="eyebrow text-primary">{t("admin.creds.summary.eyebrow")}</span>
            <div className="mt-4 grid grid-cols-2 gap-4 lg:grid-cols-5">
              <SummaryCell
                icon={KeyRound}
                label={t("admin.creds.summary.total")}
                value={summary.total}
              />
              <SummaryCell
                icon={ShieldCheck}
                label={t("admin.creds.summary.healthy")}
                value={summary.healthy}
                tone="text-success"
              />
              <SummaryCell
                icon={PauseCircle}
                label={t("admin.creds.summary.cooling")}
                value={summary.cooling}
                tone={summary.cooling > 0 ? "text-warning" : undefined}
              />
              <SummaryCell
                icon={AlertTriangle}
                label={t("admin.creds.summary.failed")}
                value={summary.failed}
                tone={summary.failed > 0 ? "text-destructive" : undefined}
              />
              <div className="min-w-0">
                <div className="eyebrow mb-1.5">{t("admin.creds.summary.slots")}</div>
                <div className="mono text-2xl font-semibold tabular-nums">
                  {summary.active}
                  <span className="text-base text-muted-foreground">
                    /{summary.capacity || "∞"}
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Provider tabs — glass segmented control */}
      <div className="glass no-scrollbar flex w-fit gap-1 overflow-x-auto rounded-xl p-1">
        {(["anthropic", "openai"] as const).map((p) => {
          const active = providerTab === p;
          return (
            <button
              key={p}
              type="button"
              onClick={() => {
                setProviderTab(p);
                setGroup("all");
                resetPaging();
              }}
              className={cn(
                "relative inline-flex shrink-0 items-center gap-2 rounded-lg px-3.5 py-1.5 text-sm transition-colors",
                active ? "text-primary-foreground" : "text-muted-foreground hover:text-foreground",
              )}
            >
              {active && (
                <motion.span
                  layoutId="admin-cred-tab-pill"
                  className="absolute inset-0 -z-10 rounded-lg bg-primary shadow-[0_8px_24px_-12px_color-mix(in_oklch,var(--primary)_70%,transparent)]"
                  transition={{ type: "spring", stiffness: 380, damping: 32 }}
                />
              )}
              {p === "anthropic" ? t("admin.creds.claudeTab") : t("admin.creds.codexTab")}
              <span
                className={cn(
                  "rounded-full px-1.5 py-0.5 font-mono text-xs",
                  active ? "bg-primary-foreground text-primary" : "bg-muted",
                )}
              >
                {byProvider[p].length}
              </span>
            </button>
          );
        })}
      </div>

      <div>
        <GlassPanel
          title={
            providerTab === "anthropic" ? t("admin.creds.claudeTitle") : t("admin.creds.codexTitle")
          }
          description={
            providerTab === "anthropic" ? t("admin.creds.claudeSub") : t("admin.creds.codexSub")
          }
          action={
            <div className="flex flex-wrap justify-end gap-2">
              <Button onClick={() => setOpenKey(true)}>{t("admin.creds.addApiKey")}</Button>
              <Button variant="outline" onClick={() => setOpenOAuth(providerTab)}>
                {providerTab === "anthropic"
                  ? t("admin.creds.addOauthClaude")
                  : t("admin.creds.addOauthCodex")}
              </Button>
              <Button variant="outline" onClick={() => setOpenUpload(true)}>
                {t("admin.creds.uploadJson")}
              </Button>
            </div>
          }
        >
          <div className="space-y-5">
            <div className="flex flex-wrap items-center gap-2">
              <div className="relative min-w-[200px] flex-1">
                <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={q}
                  onChange={(e) => {
                    setQ(e.target.value);
                    resetPaging();
                  }}
                  name="credential-search"
                  placeholder={t("admin.creds.filters.searchPlaceholder")}
                  className="h-9 pl-8"
                />
              </div>
              <Select
                value={health}
                onValueChange={(v) => {
                  setHealth(v as HealthFilter);
                  resetPaging();
                }}
              >
                <SelectTrigger
                  className="h-9 w-[140px]"
                  aria-label={t("admin.creds.filters.healthAll")}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {HEALTH_FILTERS.map((h) => (
                    <SelectItem key={h} value={h}>
                      {h === "all"
                        ? t("admin.creds.filters.healthAll")
                        : t(`admin.creds.status.${h}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Select
                value={group}
                onValueChange={(v) => {
                  setGroup(v);
                  resetPaging();
                }}
              >
                <SelectTrigger
                  className="h-9 w-[150px]"
                  aria-label={t("admin.creds.filters.groupAll")}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">{t("admin.creds.filters.groupAll")}</SelectItem>
                  <SelectItem value="__none">{t("admin.creds.filters.groupNone")}</SelectItem>
                  {groups.map((g) => (
                    <SelectItem key={g} value={g}>
                      {g}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Select value={sort} onValueChange={(v) => setSort(v as Sort)}>
                <SelectTrigger className="h-9 w-[150px]" aria-label={t("admin.creds.sort.default")}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SORTS.map((s) => (
                    <SelectItem key={s} value={s}>
                      {t(`admin.creds.sort.${s}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {filtersActive && (
                <Button variant="ghost" size="sm" onClick={clearFilters}>
                  <X className="size-3.5" />
                  {t("admin.creds.filters.clear")}
                </Button>
              )}
            </div>

            {renderSection("oauth", oauths, oauthOffset, setOauthOffset)}
            {renderSection("apikey", apikeys, keyOffset, setKeyOffset)}
          </div>
        </GlassPanel>
      </div>

      <AddAPIKeyDialog
        open={openKey}
        onOpenChange={setOpenKey}
        provider={providerTab}
        onCreated={reload}
      />
      <AddOAuthDialog provider={openOAuth} onClose={() => setOpenOAuth(null)} onCreated={reload} />
      <UploadJSONDialog
        open={openUpload}
        onOpenChange={setOpenUpload}
        provider={providerTab}
        onCreated={reload}
      />
      <CredentialDetailDialog cred={detail} onClose={() => setDetail(null)} />
      <EditCredentialDialog cred={editing} onClose={() => setEditing(null)} onSaved={reload} />
    </FadeIn>
  );
}

function SummaryCell({
  icon: Icon,
  label,
  value,
  tone,
}: {
  icon: React.ElementType;
  label: string;
  value: number;
  tone?: string;
}) {
  return (
    <div className="min-w-0">
      <div className="eyebrow mb-1.5 flex items-center gap-1.5">
        <Icon className="size-3" />
        {label}
      </div>
      <div className={cn("mono text-2xl font-semibold tabular-nums", tone)}>
        <CountUp value={value} />
      </div>
    </div>
  );
}
