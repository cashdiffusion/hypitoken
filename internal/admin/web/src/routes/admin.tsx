import {
  AlertTriangle,
  Ban,
  Building2,
  CheckCircle2,
  ChevronRight,
  Gauge,
  Gift,
  KeyRound,
  LayoutDashboard,
  Megaphone,
  Pencil,
  RefreshCw,
  ScrollText,
  Shield,
  ShieldOff,
  ShoppingCart,
  Sparkles,
  Tag,
  Trash2,
  Users,
} from "lucide-react";
import { motion } from "motion/react";
import React, { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { NavLink, Route, Routes, useLocation } from "react-router-dom";
import { toast } from "sonner";
import { AdminDashboard } from "@/components/admin/admin-dashboard";
import { AttributionTab } from "@/components/admin/attribution-tab";
import { OverviewPanel } from "@/components/admin/overview-panel";
import { ReferralTab } from "@/components/admin/referral-tab";
import { RequestsExplorer } from "@/components/admin/requests-explorer";
import { Sparkline as MiniSpark } from "@/components/admin/sparkline";
import { UpstreamUsageDialog } from "@/components/admin/upstream-usage-dialog";
import { WorkspacesTab } from "@/components/admin/workspaces-tab";
import { GlassPanel, PageHeader } from "@/components/app/page-primitives";
import { Pager } from "@/components/app/pager";
import { Reveal } from "@/components/landing/reveal";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm-dialog";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { apiDelete, apiGet, apiPatch, apiPost } from "@/lib/api";
import type { AdminAdjustment, AdminOrder, Credential, PricingGroup, User } from "@/lib/types";
import { cn, errMsg, fmtInt, fmtUSD } from "@/lib/utils";

const TABS = [
  { to: "dashboard", labelKey: "admin.tabs.dashboard", icon: Sparkles },
  { to: "fleet", labelKey: "admin.tabs.fleet", icon: LayoutDashboard },
  { to: "users", labelKey: "admin.tabs.users", icon: Users },
  { to: "workspaces", labelKey: "admin.tabs.workspaces", icon: Building2 },
  { to: "groups", labelKey: "admin.tabs.groups", icon: Tag },
  { to: "credentials", labelKey: "admin.tabs.credentials", icon: KeyRound },
  { to: "requests", labelKey: "admin.tabs.requests", icon: ScrollText },
  { to: "payments", labelKey: "admin.tabs.payments", icon: ShoppingCart },
  { to: "growth", labelKey: "admin.tabs.growth", icon: Megaphone },
  { to: "referral", labelKey: "admin.tabs.referral", icon: Gift },
];

export default function AdminPage() {
  const { t } = useTranslation();
  // Auto-refresh tick. The fleet/overview panels load once on mount, so a
  // credential whose quota cooldown has expired (cleared server-side on the
  // next Snapshot) would keep showing a stale "配额耗尽" badge until a manual
  // reload. Bump a tick every 15s so those panels re-fetch and reflect live
  // server state. The requests log is intentionally left manual — auto-
  // refreshing it mid-investigation is disruptive.
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 15000);
    return () => clearInterval(id);
  }, []);
  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow={t("nav.operator")}
        icon={Shield}
        title={t("admin.panelTitle")}
        sub={t("admin.panelSub")}
      />

      <AdminTabBar />

      <Routes>
        <Route index element={<AdminDashboard />} />
        <Route path="dashboard" element={<AdminDashboard />} />
        <Route path="fleet" element={<OverviewPanel refreshTick={tick} />} />
        {/* legacy alias — earlier deeplinks pointed at /overview */}
        <Route path="overview" element={<OverviewPanel refreshTick={tick} />} />
        <Route path="users" element={<UsersTab />} />
        <Route path="workspaces" element={<WorkspacesTab />} />
        <Route path="groups" element={<GroupsTab />} />
        <Route path="credentials" element={<CredentialsTab />} />
        <Route path="requests" element={<RequestsExplorer refreshTick={0} />} />
        <Route path="payments" element={<PaymentsTab />} />
        <Route path="growth" element={<AttributionTab />} />
        <Route path="referral" element={<ReferralTab />} />
      </Routes>
    </div>
  );
}

// AdminTabBar — a glass tab strip whose active tab carries a shared underline
// pill that slides between tabs via a motion layoutId, the same language as the
// app sidebar. Horizontally scrollable on narrow screens; never wraps.
function AdminTabBar() {
  const { t } = useTranslation();
  const { pathname } = useLocation();
  const isActive = (to: string) =>
    to === "dashboard"
      ? pathname === "/app/admin" || pathname === "/app/admin/dashboard"
      : pathname === `/app/admin/${to}` || pathname.startsWith(`/app/admin/${to}/`);
  return (
    <div className="glass no-scrollbar flex gap-1 overflow-x-auto rounded-xl p-1">
      {TABS.map((tab) => {
        const active = isActive(tab.to);
        return (
          <NavLink
            key={tab.to}
            to={`/app/admin/${tab.to}`}
            end={tab.to === "dashboard"}
            className={cn(
              "relative inline-flex shrink-0 items-center gap-2 rounded-lg px-3.5 py-2 text-sm transition-colors",
              active ? "text-primary-foreground" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {active && (
              <motion.span
                layoutId="admin-tab-pill"
                className="absolute inset-0 -z-10 rounded-lg bg-primary shadow-[0_8px_24px_-12px_color-mix(in_oklch,var(--primary)_70%,transparent)]"
                transition={{ type: "spring", stiffness: 380, damping: 32 }}
              />
            )}
            <tab.icon className="h-3.5 w-3.5" />
            {t(tab.labelKey)}
          </NavLink>
        );
      })}
    </div>
  );
}

const USERS_PAGE = 50;

function UsersTab() {
  const { t } = useTranslation();
  const [users, setUsers] = useState<User[]>([]);
  const [groups, setGroups] = useState<PricingGroup[]>([]);
  // qInput is the live textbox; q is the debounced value that actually hits
  // the API, so we don't fire one request per keystroke.
  const [qInput, setQInput] = useState("");
  const [q, setQ] = useState("");
  const [offset, setOffset] = useState(0);
  const [total, setTotal] = useState<number | undefined>(undefined);
  const [busy, setBusy] = useState(true);
  // user id with an in-flight row mutation — disables that row's controls.
  const [rowBusy, setRowBusy] = useState<number | null>(null);

  useEffect(() => {
    const id = setTimeout(() => {
      setQ(qInput);
      setOffset(0); // a new search always starts from page 1
    }, 300);
    return () => clearTimeout(id);
  }, [qInput]);

  const reload = async () => {
    setBusy(true);
    try {
      const [u, g] = await Promise.all([
        apiGet<{ users: User[]; total?: number }>(
          `/admin/users?q=${encodeURIComponent(q)}&limit=${USERS_PAGE}&offset=${offset}`,
        ),
        apiGet<{ groups: PricingGroup[] }>("/admin/groups"),
      ]);
      setUsers(u.users || []);
      setTotal(u.total);
      setGroups(g.groups || []);
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };
  // biome-ignore lint/correctness/useExhaustiveDependencies: reload closes over q/offset; re-run only when the search query or page changes.
  useEffect(() => {
    reload();
  }, [q, offset]);

  // Shared row mutation: optimistic disable via rowBusy, then refresh the
  // current page so the controlled select/badges reflect server truth.
  const patchUser = async (id: number, body: Record<string, unknown>, okMsg: string) => {
    setRowBusy(id);
    try {
      await apiPatch(`/admin/users/${id}`, body);
      toast.success(okMsg);
      await reload();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setRowBusy(null);
    }
  };

  return (
    <Reveal>
      <GlassPanel
        title={t("admin.users.headingCount", { n: total ?? users.length })}
        action={
          <Input
            placeholder={t("admin.users.searchPlaceholder")}
            value={qInput}
            onChange={(e) => setQInput(e.target.value)}
            className="w-full sm:w-64"
          />
        }
        bodyClassName="p-0"
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("admin.users.cols.email")}</TableHead>
              <TableHead>{t("admin.users.cols.role")}</TableHead>
              <TableHead>{t("admin.users.cols.group")}</TableHead>
              <TableHead className="text-right">{t("admin.users.cols.balance")}</TableHead>
              <TableHead></TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="py-12 text-center text-muted-foreground">
                  {busy ? t("common.loading") : t("admin.users.empty")}
                </TableCell>
              </TableRow>
            )}
            {users.map((u) => (
              <TableRow key={u.id} className={u.disabled ? "opacity-50" : ""}>
                <TableCell className="font-medium">{u.email}</TableCell>
                <TableCell>
                  <span
                    className={`rounded border px-2 py-0.5 text-xs font-mono uppercase tracking-wider ${u.role === "admin" ? "border-primary/40 bg-primary/15 text-primary" : "border-border bg-muted"}`}
                  >
                    {u.role}
                  </span>
                </TableCell>
                <TableCell>{groups.find((g) => g.ID === u.group_id)?.Name || u.group_id}</TableCell>
                <TableCell className="font-mono tabular-nums text-right">
                  {fmtUSD(u.balance_usd)}
                </TableCell>
                <TableCell>
                  <AdjustBalanceButton userID={u.id} onDone={reload} />
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    <select
                      className="rounded border border-border bg-card px-2 py-1 text-xs disabled:opacity-50"
                      value={u.group_id}
                      disabled={rowBusy === u.id}
                      onChange={(e) =>
                        patchUser(
                          u.id,
                          { group_id: parseInt(e.target.value, 10) },
                          t("admin.users.groupUpdated"),
                        )
                      }
                    >
                      {groups.map((g) => (
                        <option key={g.ID} value={g.ID}>
                          {g.Name}
                        </option>
                      ))}
                    </select>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={rowBusy === u.id}
                      onClick={() =>
                        patchUser(
                          u.id,
                          { role: u.role === "admin" ? "user" : "admin" },
                          t("admin.users.roleUpdated"),
                        )
                      }
                    >
                      {u.role === "admin" ? t("admin.users.makeUser") : t("admin.users.makeAdmin")}
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-destructive"
                      disabled={rowBusy === u.id}
                      onClick={() =>
                        patchUser(
                          u.id,
                          { disabled: !u.disabled },
                          u.disabled ? t("admin.users.enabled") : t("admin.users.disabled"),
                        )
                      }
                    >
                      {u.disabled ? t("admin.users.enable") : t("admin.users.disable")}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <Pager
          offset={offset}
          limit={USERS_PAGE}
          total={total}
          count={users.length}
          busy={busy}
          onChange={setOffset}
          className="border-t border-border px-4 py-3"
        />
      </GlassPanel>
    </Reveal>
  );
}

function AdjustBalanceButton({ userID, onDone }: { userID: number; onDone: () => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [delta, setDelta] = useState("");
  const [note, setNote] = useState("");
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        {t("admin.users.adjust")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-[400px]">
          <DialogHeader>
            <DialogTitle>{t("admin.users.adjustTitle")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div className="space-y-2">
              <Label>{t("admin.users.delta")}</Label>
              <Input
                type="number"
                step="0.01"
                placeholder={t("admin.users.deltaPlaceholder")}
                value={delta}
                onChange={(e) => setDelta(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label>{t("admin.users.note")}</Label>
              <Input
                placeholder={t("admin.users.notePlaceholder")}
                value={note}
                onChange={(e) => setNote(e.target.value)}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              onClick={async () => {
                await apiPost(`/admin/users/${userID}/balance`, {
                  delta_usd: parseFloat(delta),
                  note,
                });
                toast.success(t("admin.users.balanceUpdated"));
                onDone();
                setOpen(false);
                setDelta("");
                setNote("");
              }}
            >
              {t("admin.users.apply")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function GroupsTab() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [groups, setGroups] = useState<PricingGroup[]>([]);
  const reload = async () => {
    const r = await apiGet<{ groups: PricingGroup[] }>("/admin/groups");
    setGroups(r.groups || []);
  };
  // biome-ignore lint/correctness/useExhaustiveDependencies: load once on mount; reload runs imperatively after mutations.
  useEffect(() => {
    reload();
  }, []);
  return (
    <Reveal>
      <GlassPanel
        title={t("admin.groups.heading")}
        action={<GroupDialog onDone={reload} />}
        bodyClassName="p-0"
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("admin.groups.cols.name")}</TableHead>
              <TableHead className="text-right">{t("admin.groups.cols.claudeMult")}</TableHead>
              <TableHead className="text-right">{t("admin.groups.cols.codexMult")}</TableHead>
              <TableHead>{t("admin.groups.cols.credGroup")}</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.map((g) => (
              <TableRow key={g.ID}>
                <TableCell>
                  <div className="font-medium">{g.Name}</div>
                  <div className="text-xs text-muted-foreground">{g.Description}</div>
                </TableCell>
                <TableCell className="font-mono tabular-nums text-right">
                  {g.ClaudeMultiplier.toFixed(2)}×
                </TableCell>
                <TableCell className="font-mono tabular-nums text-right">
                  {g.CodexMultiplier.toFixed(2)}×
                </TableCell>
                <TableCell className="font-mono text-xs">{g.CredentialGroup || "—"}</TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    <GroupDialog group={g} onDone={reload} />
                    {!g.IsDefault && (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-destructive"
                        onClick={async () => {
                          if (
                            !(await confirm({
                              title: t("common.delete"),
                              description: t("admin.groups.confirmDelete", { name: g.Name }),
                              confirmLabel: t("common.delete"),
                              destructive: true,
                            }))
                          )
                            return;
                          await apiDelete(`/admin/groups/${g.ID}`);
                          toast.success(t("admin.groups.deleted"));
                          reload();
                        }}
                      >
                        {t("common.delete")}
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </GlassPanel>
    </Reveal>
  );
}

// GroupDialog drives both create (no `group`) and edit (`group` provided)
// of a pricing tier. Edit posts to PATCH /admin/groups/:id (full-field
// update). The user-facing dashboard reads each user's multipliers live
// from /me, so a multiplier change here surfaces on the user's next page
// load with no extra wiring.
function GroupDialog({ group, onDone }: { group?: PricingGroup; onDone: () => void }) {
  const { t } = useTranslation();
  const editing = !!group;
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [claudeMult, setClaudeMult] = useState("0.3");
  const [codexMult, setCodexMult] = useState("0.05");
  const [credGroup, setCredGroup] = useState("");
  // Seed the form from the latest group values every time the dialog opens,
  // so a reload between edits is reflected (useState initialisers don't re-run).
  const openDialog = () => {
    setName(group?.Name ?? "");
    setDesc(group?.Description ?? "");
    setClaudeMult(group ? String(group.ClaudeMultiplier) : "0.3");
    setCodexMult(group ? String(group.CodexMultiplier) : "0.05");
    setCredGroup(group?.CredentialGroup ?? "");
    setOpen(true);
  };
  return (
    <>
      {editing ? (
        <Button size="sm" variant="ghost" onClick={openDialog}>
          {t("common.edit")}
        </Button>
      ) : (
        <Button onClick={openDialog}>{t("admin.groups.newBtn")}</Button>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editing ? t("admin.groups.editTitle") : t("admin.groups.newTitle")}
            </DialogTitle>
          </DialogHeader>
          <p className="text-xs text-muted-foreground">{t("admin.groups.newSub")}</p>
          <div className="grid gap-3 py-2">
            <div className="space-y-2">
              <Label>{t("admin.groups.labels.name")}</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label>{t("admin.groups.labels.desc")}</Label>
              <Input value={desc} onChange={(e) => setDesc(e.target.value)} />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label>{t("admin.groups.labels.claudeMult")}</Label>
                <Input value={claudeMult} onChange={(e) => setClaudeMult(e.target.value)} />
              </div>
              <div className="space-y-2">
                <Label>{t("admin.groups.labels.codexMult")}</Label>
                <Input value={codexMult} onChange={(e) => setCodexMult(e.target.value)} />
              </div>
            </div>
            <div className="space-y-2">
              <Label>{t("admin.groups.labels.credGroup")}</Label>
              <Input value={credGroup} onChange={(e) => setCredGroup(e.target.value)} />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              onClick={async () => {
                const body = {
                  name,
                  description: desc,
                  claude_multiplier: parseFloat(claudeMult),
                  codex_multiplier: parseFloat(codexMult),
                  credential_group: credGroup,
                };
                if (editing && group) {
                  await apiPatch(`/admin/groups/${group.ID}`, body);
                  toast.success(t("admin.groups.updated"));
                } else {
                  await apiPost("/admin/groups", body);
                  toast.success(t("admin.groups.created"));
                }
                onDone();
                setOpen(false);
              }}
            >
              {editing ? t("common.save") : t("common.create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function CredentialsTab() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [creds, setCreds] = useState<Credential[]>([]);
  const [openKey, setOpenKey] = useState(false);
  const [openOAuth, setOpenOAuth] = useState<null | "anthropic" | "openai">(null);
  const [openUpload, setOpenUpload] = useState(false);
  const [usageFor, setUsageFor] = useState<{ id: string; label: string; provider: string } | null>(
    null,
  );
  const [providerTab, setProviderTab] = useState<"anthropic" | "openai" | "kiro">("anthropic");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<Credential | null>(null);
  const [busyId, setBusyId] = useState<string>("");
  const reload = async () => {
    const r = await apiGet<{ credentials: Credential[] }>("/admin/credentials");
    setCreds(r.credentials || []);
  };
  // Load on mount + poll every 15s so an expired quota cooldown (cleared
  // server-side) stops showing a stale "配额耗尽" badge without a manual reload.
  // reload also runs imperatively after mutations. Polling only swaps `creds`,
  // so expanded rows / open dialogs (separate state) are preserved.
  // biome-ignore lint/correctness/useExhaustiveDependencies: mount + interval poll; reload's closure only touches stable setters.
  useEffect(() => {
    reload();
    const id = setInterval(reload, 15000);
    return () => clearInterval(id);
  }, []);

  const toggleExpand = (id: string) => {
    const s = new Set(expanded);
    if (s.has(id)) s.delete(id);
    else s.add(id);
    setExpanded(s);
  };

  const runAction = async (
    c: Credential,
    kind: "refresh" | "clear-quota" | "clear-failure" | "toggle",
  ) => {
    setBusyId(c.id);
    try {
      if (kind === "toggle") {
        await apiPatch(`/admin/credentials/${encodeURIComponent(c.id)}`, { disabled: !c.disabled });
        toast.success(c.disabled ? "已启用" : "已禁用");
      } else {
        await apiPost(`/admin/credentials/${encodeURIComponent(c.id)}/${kind}`);
        toast.success(
          kind === "refresh"
            ? "已刷新 token"
            : kind === "clear-quota"
              ? "已清除配额标记"
              : "已标记为健康",
        );
      }
      await reload();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusyId("");
    }
  };

  const claudeCreds = creds.filter((c) => c.provider === "anthropic");
  const codexCreds = creds.filter((c) => c.provider === "openai");
  const visible = providerTab === "anthropic" ? claudeCreds : codexCreds;
  // Kiro creds live behind a separate endpoint (different storage, different
  // auth flow), so we keep them out of the unified `creds` list and render
  // them in their own panel below the provider tabs.

  return (
    <div className="space-y-4">
      {/* Provider tabs — glass segmented control */}
      <div className="glass no-scrollbar flex w-fit gap-1 overflow-x-auto rounded-xl p-1">
        {(["anthropic", "openai", "kiro"] as const).map((p) => {
          const count =
            p === "anthropic" ? claudeCreds.length : p === "openai" ? codexCreds.length : 0; // kiro count rendered inside the panel itself
          const active = providerTab === p;
          return (
            <button
              key={p}
              type="button"
              onClick={() => setProviderTab(p)}
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
              {p === "anthropic"
                ? t("admin.creds.claudeTab")
                : p === "openai"
                  ? t("admin.creds.codexTab")
                  : "Kiro"}
              {p !== "kiro" && (
                <span
                  className={cn(
                    "rounded-full px-1.5 py-0.5 font-mono text-xs",
                    active ? "bg-white/20" : "bg-muted",
                  )}
                >
                  {count}
                </span>
              )}
            </button>
          );
        })}
      </div>

      {providerTab === "kiro" && <KiroCredentialsPanel />}

      {providerTab !== "kiro" && (
        <Reveal>
          <GlassPanel
            title={
              providerTab === "anthropic"
                ? t("admin.creds.claudeTitle")
                : t("admin.creds.codexTitle")
            }
            description={
              providerTab === "anthropic" ? t("admin.creds.claudeSub") : t("admin.creds.codexSub")
            }
            action={
              <div className="flex flex-wrap justify-end gap-2">
                <Button onClick={() => setOpenKey(true)}>{t("admin.creds.addApiKey")}</Button>
                {providerTab === "anthropic" ? (
                  <Button variant="outline" onClick={() => setOpenOAuth("anthropic")}>
                    {t("admin.creds.addOauthClaude")}
                  </Button>
                ) : (
                  <Button variant="outline" onClick={() => setOpenOAuth("openai")}>
                    {t("admin.creds.addOauthCodex")}
                  </Button>
                )}
                <Button variant="outline" onClick={() => setOpenUpload(true)}>
                  {t("admin.creds.uploadJson")}
                </Button>
              </div>
            }
            bodyClassName="p-0"
          >
            {visible.length === 0 ? (
              <div className="p-12 text-center text-sm text-muted-foreground">
                {t("tokens.none")}
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8"></TableHead>
                    <TableHead>{t("admin.creds.cols.label")}</TableHead>
                    <TableHead>{t("admin.creds.cols.kind")}</TableHead>
                    <TableHead>{t("admin.creds.cols.group")}</TableHead>
                    <TableHead>槽位</TableHead>
                    <TableHead className="text-right">近 24h</TableHead>
                    <TableHead className="text-right">累计</TableHead>
                    <TableHead>{t("admin.creds.cols.expires")}</TableHead>
                    <TableHead>{t("admin.creds.cols.health")}</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visible.map((c) => {
                    const isOpen = expanded.has(c.id);
                    const slotPct =
                      c.max_concurrent > 0
                        ? Math.min(100, Math.round((c.active_clients / c.max_concurrent) * 100))
                        : 0;
                    const u = c.usage;
                    const busy = busyId === c.id;
                    return (
                      <React.Fragment key={c.id}>
                        <TableRow className={c.disabled ? "opacity-50" : ""}>
                          <TableCell>
                            <button
                              type="button"
                              onClick={() => toggleExpand(c.id)}
                              className="text-muted-foreground hover:text-foreground transition-transform"
                              aria-label={isOpen ? "收起" : "展开"}
                            >
                              <ChevronRight
                                className={cn("size-4 transition-transform", isOpen && "rotate-90")}
                              />
                            </button>
                          </TableCell>
                          <TableCell>
                            <div className="font-medium">{c.label}</div>
                            <code className="text-xs text-muted-foreground">{c.id}</code>
                            {c.email && (
                              <div className="text-xs text-muted-foreground truncate max-w-[260px]">
                                {c.email}
                              </div>
                            )}
                            {/* Always-visible failure reason. Mirrors the AlertStrip
                              inside the expanded detail panel, so operators don't have to
                              click to see *why* a credential is in trouble. */}
                            {c.failure_reason && !c.quota_exceeded && (
                              <div
                                className={cn(
                                  "mt-1 flex items-start gap-1 text-[11px] font-mono leading-tight max-w-[280px]",
                                  c.hard_failure ? "text-destructive" : "text-warning",
                                )}
                                title={c.failure_reason}
                              >
                                {c.hard_failure ? (
                                  <ShieldOff className="size-3 shrink-0 mt-0.5" />
                                ) : (
                                  <AlertTriangle className="size-3 shrink-0 mt-0.5" />
                                )}
                                <span className="truncate">{c.failure_reason}</span>
                              </div>
                            )}
                            {c.quota_exceeded && (
                              <div
                                className="mt-1 flex items-start gap-1 text-[11px] font-mono leading-tight text-warning max-w-[280px]"
                                title={
                                  c.quota_reset_at
                                    ? `Resets ${new Date(c.quota_reset_at).toLocaleString()}`
                                    : "no reset time"
                                }
                              >
                                <AlertTriangle className="size-3 shrink-0 mt-0.5" />
                                <span className="truncate">
                                  配额耗尽
                                  {c.quota_reset_at
                                    ? ` · 重置 ${new Date(c.quota_reset_at).toLocaleString()}`
                                    : " · 无重置时间"}
                                </span>
                              </div>
                            )}
                            {c.last_client_cancel &&
                              Date.now() - new Date(c.last_client_cancel).getTime() <
                                3600 * 1000 && (
                                <div
                                  className="mt-1 flex items-start gap-1 text-[11px] font-mono leading-tight text-muted-foreground max-w-[280px]"
                                  title={`${new Date(c.last_client_cancel).toLocaleString()}${c.client_cancel_reason ? ` · ${c.client_cancel_reason}` : ""}`}
                                >
                                  <Ban className="size-3 shrink-0 mt-0.5" />
                                  <span className="truncate">
                                    客户端取消
                                    {c.client_cancel_reason ? ` · ${c.client_cancel_reason}` : ""}
                                  </span>
                                </div>
                              )}
                          </TableCell>
                          <TableCell>
                            <div className="flex flex-col gap-0.5">
                              <span className="rounded border border-border bg-muted px-2 py-0.5 text-xs font-mono uppercase w-fit">
                                {c.kind}
                              </span>
                              {c.plan_type && (
                                <span className="text-[10px] font-mono uppercase text-muted-foreground">
                                  {c.plan_type}
                                </span>
                              )}
                            </div>
                          </TableCell>
                          <TableCell className="font-mono text-xs">{c.group || "—"}</TableCell>
                          <TableCell>
                            <div className="font-mono tabular-nums text-xs">
                              {c.active_clients}/{c.max_concurrent > 0 ? c.max_concurrent : "∞"}
                            </div>
                            {c.max_concurrent > 0 && (
                              <div className="mt-1 h-1 w-20 bg-muted rounded-full overflow-hidden">
                                <div
                                  className={cn(
                                    "h-full",
                                    slotPct > 80 ? "bg-warning" : "bg-success",
                                  )}
                                  style={{ width: `${slotPct}%` }}
                                />
                              </div>
                            )}
                          </TableCell>
                          <TableCell className="font-mono tabular-nums text-right text-xs">
                            {u ? (
                              <>
                                <div>
                                  {fmtInt(u.sum_24h.input_tokens + u.sum_24h.output_tokens)}
                                </div>
                                <div className="text-muted-foreground">
                                  {fmtInt(u.sum_24h.requests || 0)} req
                                </div>
                              </>
                            ) : (
                              <span className="text-muted-foreground">—</span>
                            )}
                          </TableCell>
                          <TableCell className="font-mono tabular-nums text-right text-xs">
                            {u ? (
                              <>
                                <div>{fmtUSD(u.total_cost_usd)}</div>
                                <div className="text-muted-foreground">
                                  {fmtInt(u.total?.requests || 0)} req
                                </div>
                              </>
                            ) : (
                              <span className="text-muted-foreground">—</span>
                            )}
                          </TableCell>
                          <TableCell className="font-mono text-xs">
                            <Expiry cred={c} />
                          </TableCell>
                          <TableCell>
                            <HealthPill cred={c} />
                          </TableCell>
                          <TableCell className="text-right">
                            <div className="flex items-center justify-end gap-1">
                              <Button
                                size="sm"
                                variant="ghost"
                                title="编辑"
                                onClick={() => setEditing(c)}
                              >
                                <Pencil className="size-3.5" />
                              </Button>
                              {c.kind === "oauth" && (
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  title="刷新 token"
                                  disabled={busy}
                                  onClick={() => runAction(c, "refresh")}
                                >
                                  <RefreshCw className={cn("size-3.5", busy && "animate-spin")} />
                                </Button>
                              )}
                              {c.kind === "oauth" &&
                                (c.provider === "anthropic" || c.provider === "openai") && (
                                  <Button
                                    size="sm"
                                    variant="ghost"
                                    title={
                                      c.provider === "openai"
                                        ? "chatgpt.com wham/usage 主动探针"
                                        : "后端配额"
                                    }
                                    onClick={() =>
                                      setUsageFor({
                                        id: c.id,
                                        label: c.label,
                                        provider: c.provider,
                                      })
                                    }
                                  >
                                    <Gauge className="size-3.5" />
                                  </Button>
                                )}
                              {c.quota_exceeded && (
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  title="清除配额标记"
                                  disabled={busy}
                                  onClick={() => runAction(c, "clear-quota")}
                                >
                                  <CheckCircle2 className="size-3.5 text-warning" />
                                </Button>
                              )}
                              {(c.hard_failure ||
                                (!c.healthy && !c.quota_exceeded && !c.disabled)) && (
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  title="标记为健康"
                                  disabled={busy}
                                  onClick={() => runAction(c, "clear-failure")}
                                >
                                  <CheckCircle2 className="size-3.5 text-success" />
                                </Button>
                              )}
                              <Button
                                size="sm"
                                variant="ghost"
                                className="text-destructive"
                                title={t("common.delete")}
                                onClick={async () => {
                                  if (
                                    !(await confirm({
                                      title: t("common.delete"),
                                      description: t("admin.creds.confirmRemove", {
                                        name: c.label,
                                      }),
                                      confirmLabel: t("common.delete"),
                                      destructive: true,
                                    }))
                                  )
                                    return;
                                  await apiDelete(`/admin/credentials/${encodeURIComponent(c.id)}`);
                                  toast.success(t("admin.creds.removed"));
                                  reload();
                                }}
                              >
                                <Trash2 className="size-3.5" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                        {isOpen && (
                          <TableRow className="bg-muted/20 hover:bg-muted/20">
                            <TableCell colSpan={10} className="py-4">
                              <CredentialDetail c={c} />
                            </TableCell>
                          </TableRow>
                        )}
                      </React.Fragment>
                    );
                  })}
                </TableBody>
              </Table>
            )}
          </GlassPanel>
        </Reveal>
      )}

      <AddAPIKeyDialog
        open={openKey}
        onOpenChange={setOpenKey}
        provider={providerTab === "openai" ? "openai" : "anthropic"}
        onCreated={reload}
      />
      <AddOAuthDialog provider={openOAuth} onClose={() => setOpenOAuth(null)} onCreated={reload} />
      <UploadJSONDialog
        open={openUpload}
        onOpenChange={setOpenUpload}
        provider={providerTab === "openai" ? "openai" : "anthropic"}
        onCreated={reload}
      />
      <UpstreamUsageDialog
        authId={usageFor?.id ?? null}
        authLabel={usageFor?.label || ""}
        provider={usageFor?.provider === "openai" ? "openai" : "anthropic"}
        onClose={() => setUsageFor(null)}
      />
      <EditCredentialDialog cred={editing} onClose={() => setEditing(null)} onSaved={reload} />
    </div>
  );
}

function CredentialDetail({ c }: { c: Credential }) {
  const u = c.usage;
  const daily = (u?.daily || []) as {
    day: string;
    input_tokens?: number;
    output_tokens?: number;
    cache_read_tokens?: number;
    cache_create_tokens?: number;
  }[];
  const sparkPoints = daily.map((d) => ({
    label: d.day,
    value: (d.input_tokens || 0) + (d.output_tokens || 0),
  }));
  const failureBanner = !c.quota_exceeded && c.failure_reason;
  return (
    <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 px-2">
      {/* Alerts */}
      {(c.quota_exceeded || failureBanner || c.last_client_cancel || c.refresh_suspended) && (
        <div className="lg:col-span-3 flex flex-col gap-2">
          {c.quota_exceeded && (
            <AlertStrip
              tone="warning"
              icon={<AlertTriangle className="size-3.5" />}
              label="配额已耗尽"
            >
              {c.quota_reset_at
                ? `重置于 ${new Date(c.quota_reset_at).toLocaleString()}`
                : "未报告重置时间"}
            </AlertStrip>
          )}
          {failureBanner && (
            <AlertStrip
              tone={c.hard_failure ? "error" : "warning"}
              icon={
                c.hard_failure ? (
                  <ShieldOff className="size-3.5" />
                ) : (
                  <AlertTriangle className="size-3.5" />
                )
              }
              label={c.hard_failure ? "硬失败" : "近期失败"}
            >
              {c.failure_reason}
            </AlertStrip>
          )}
          {c.refresh_suspended && (
            <AlertStrip tone="error" icon={<ShieldOff className="size-3.5" />} label="刷新已冻结">
              {c.refresh_suspended_reason || (c.disabled ? "credential disabled" : "hard failure")}
              {" · "}
              <span className="opacity-70">
                {c.disabled ? "启用后会自动恢复" : "执行「标记为健康」后会自动恢复"}
              </span>
            </AlertStrip>
          )}
          {c.last_client_cancel &&
            Date.now() - new Date(c.last_client_cancel).getTime() < 3600 * 1000 && (
              <AlertStrip tone="muted" icon={<Ban className="size-3.5" />} label="客户端取消">
                {new Date(c.last_client_cancel).toLocaleString()}
                {c.client_cancel_reason ? ` · ${c.client_cancel_reason}` : ""}
              </AlertStrip>
            )}
        </div>
      )}

      {/* Usage card */}
      <div className="rounded-md border border-border bg-card p-3">
        <div className="text-xs font-medium uppercase text-muted-foreground mb-2">用量</div>
        {u ? (
          <div className="space-y-2 text-xs">
            <Row
              k="24h 输入/输出"
              v={`${fmtInt(u.sum_24h.input_tokens)} / ${fmtInt(u.sum_24h.output_tokens)}`}
            />
            {u.sum_24h.cache_read_tokens > 0 && (
              <Row k="24h 缓存读取" v={fmtInt(u.sum_24h.cache_read_tokens)} />
            )}
            <Row
              k="累计请求"
              v={`${fmtInt(u.total.requests)}${u.total.errors > 0 ? ` (${fmtInt(u.total.errors)} err)` : ""}`}
            />
            <Row k="累计成本" v={fmtUSD(u.total_cost_usd)} />
            {u.last_used && <Row k="最近使用" v={new Date(u.last_used).toLocaleString()} />}
            {c.kind === "oauth" && c.provider === "openai" && u.sum_5h && (
              <Row
                k="滚动 5h"
                v={`in ${fmtInt(u.sum_5h.input_tokens)} · out ${fmtInt(u.sum_5h.output_tokens)}`}
              />
            )}
          </div>
        ) : (
          <div className="text-xs text-muted-foreground">暂无用量数据</div>
        )}
      </div>

      {/* 14-day spark */}
      <div className="rounded-md border border-border bg-card p-3">
        <div className="text-xs font-medium uppercase text-muted-foreground mb-2">
          近 14 天 token
        </div>
        {sparkPoints.length > 0 ? (
          <MiniSpark data={sparkPoints} />
        ) : (
          <div className="text-xs text-muted-foreground">无历史数据</div>
        )}
      </div>

      {/* Config */}
      <div className="rounded-md border border-border bg-card p-3">
        <div className="text-xs font-medium uppercase text-muted-foreground mb-2">配置</div>
        <div className="space-y-2 text-xs">
          <Row k="代理" v={c.proxy_url || <span className="text-muted-foreground">direct</span>} />
          {c.base_url && (
            <Row k="Base URL" v={<span className="font-mono break-all">{c.base_url}</span>} />
          )}
          <Row k="最大并发" v={c.max_concurrent > 0 ? String(c.max_concurrent) : "∞"} />
          <Row k="文件支撑" v={c.file_backed ? "是" : "否（config.yaml）"} />
          {c.disabled && <Row k="状态" v={<span className="text-warning">已禁用</span>} />}
        </div>
      </div>

      {/* Active client tokens */}
      {c.active_clients > 0 && c.client_tokens && c.client_tokens.length > 0 && (
        <div className="rounded-md border border-border bg-card p-3 lg:col-span-2">
          <div className="text-xs font-medium uppercase text-muted-foreground mb-2">
            活跃客户端 token ({c.client_tokens.length})
          </div>
          <ul className="space-y-0.5 text-xs font-mono">
            {c.client_tokens.map((t: string) => (
              <li key={t} className="truncate">
                {t}
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Model map */}
      {c.model_map && Object.keys(c.model_map).length > 0 && (
        <div className="rounded-md border border-border bg-card p-3 lg:col-span-2">
          <div className="text-xs font-medium uppercase text-muted-foreground mb-2">
            模型映射 ({Object.keys(c.model_map).length})
          </div>
          <div className="space-y-1 text-xs font-mono">
            {Object.keys(c.model_map)
              .sort()
              .map((k) => (
                <div key={k} className="break-all leading-relaxed">
                  <span>{k}</span>
                  {c.model_map?.[k] ? (
                    <>
                      <span className="text-muted-foreground"> → </span>
                      <span>{c.model_map?.[k]}</span>
                    </>
                  ) : (
                    <span className="text-muted-foreground"> (不改写)</span>
                  )}
                </div>
              ))}
          </div>
        </div>
      )}
    </div>
  );
}

function Row({ k, v }: { k: string; v: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-muted-foreground shrink-0">{k}</span>
      <span className="font-mono tabular-nums text-right truncate">{v}</span>
    </div>
  );
}

function AlertStrip({
  tone,
  icon,
  label,
  children,
}: {
  tone: "warning" | "error" | "muted";
  icon: React.ReactNode;
  label: string;
  children: React.ReactNode;
}) {
  const tones: Record<string, string> = {
    warning: "bg-warning/10 text-warning border-warning/25",
    error: "bg-destructive/10 text-destructive border-destructive/25",
    muted: "bg-muted text-muted-foreground border-border",
  };
  return (
    <div
      className={cn("flex items-center gap-2 rounded-md border px-3 py-1.5 text-xs", tones[tone])}
    >
      <span className="shrink-0">{icon}</span>
      <span className="font-medium uppercase tracking-wider text-[10px]">{label}</span>
      <span className="font-mono truncate ml-auto opacity-90 text-right max-w-[60%]">
        {children}
      </span>
    </div>
  );
}

function EditCredentialDialog({
  cred,
  onClose,
  onSaved,
}: {
  cred: Credential | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [label, setLabel] = useState("");
  const [group, setGroup] = useState("");
  const [proxy, setProxy] = useState("");
  const [base, setBase] = useState("");
  const [maxC, setMaxC] = useState("");
  const [disabled, setDisabled] = useState(false);
  const [modelMap, setModelMap] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (cred) {
      setLabel(cred.label || "");
      setGroup(cred.group || "");
      setProxy(cred.proxy_url || "");
      setBase(cred.base_url || "");
      setMaxC(String(cred.max_concurrent ?? 0));
      setDisabled(!!cred.disabled);
      setModelMap(
        cred.model_map && Object.keys(cred.model_map).length > 0
          ? JSON.stringify(cred.model_map, null, 2)
          : "",
      );
    }
  }, [cred]);

  if (!cred) return null;
  const isAPIKey = cred.kind === "apikey";

  const save = async () => {
    setBusy(true);
    try {
      const body: {
        label: string;
        group: string;
        proxy_url: string;
        max_concurrent: number;
        disabled: boolean;
        base_url?: string;
        model_map?: Record<string, string>;
      } = {
        label,
        group,
        proxy_url: proxy,
        max_concurrent: Number(maxC) || 0,
        disabled,
      };
      if (isAPIKey) {
        body.base_url = base;
      }
      if (modelMap.trim() === "") {
        body.model_map = {};
      } else {
        try {
          body.model_map = JSON.parse(modelMap);
        } catch (e) {
          toast.error(`model_map JSON 解析失败：${errMsg(e)}`);
          setBusy(false);
          return;
        }
      }
      await apiPatch(`/admin/credentials/${encodeURIComponent(cred.id)}`, body);
      toast.success("已保存");
      onSaved();
      onClose();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={!!cred} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>编辑凭证 · {cred.label}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3 py-2">
          <div className="space-y-2">
            <Label>标签</Label>
            <Input value={label} onChange={(e) => setLabel(e.target.value)} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label>分组</Label>
              <Input
                value={group}
                onChange={(e) => setGroup(e.target.value)}
                placeholder="empty = public"
              />
            </div>
            <div className="space-y-2">
              <Label>最大并发（0 = 无限）</Label>
              <Input type="number" min={0} value={maxC} onChange={(e) => setMaxC(e.target.value)} />
            </div>
          </div>
          <div className="space-y-2">
            <Label>代理 URL</Label>
            <Input
              value={proxy}
              onChange={(e) => setProxy(e.target.value)}
              placeholder="http:// 或 socks5://"
              className="font-mono"
            />
          </div>
          {isAPIKey && (
            <div className="space-y-2">
              <Label>Base URL</Label>
              <Input
                value={base}
                onChange={(e) => setBase(e.target.value)}
                placeholder="留空走默认"
                className="font-mono"
              />
            </div>
          )}
          <div className="space-y-2">
            <Label>Model map（JSON）</Label>
            <Textarea
              value={modelMap}
              onChange={(e) => setModelMap(e.target.value)}
              className="font-mono text-xs h-32"
              placeholder={'{\n  "claude-opus-4-7": "claude-opus-4-8"\n}'}
            />
            <p className="text-[11px] text-muted-foreground">
              仅改写：列出的模型把请求体 model 改写为右值；空值（如{" "}
              <code className="font-mono">{`"opus": ""`}</code>）或未列出的键 =
              不改写直接放行。不会限制可用模型。
            </p>
            {!isAPIKey && (
              <p className="text-[11px] text-muted-foreground">
                Claude OAuth 默认：<code className="font-mono">claude-opus-4-6</code> 和{" "}
                <code className="font-mono">claude-opus-4-7</code> →{" "}
                <code className="font-mono">claude-opus-4-8</code>
                （已显示在上方，可编辑覆盖；清空则全部走默认）。
              </p>
            )}
          </div>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={disabled}
              onChange={(e) => setDisabled(e.target.checked)}
              className="size-4"
            />
            <span>禁用（不再被调度）</span>
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t_cancel()}
          </Button>
          <Button disabled={busy} onClick={save}>
            {busy ? "保存中…" : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function t_cancel() {
  return "取消";
}

function AddOAuthDialog({
  provider,
  onClose,
  onCreated,
}: {
  provider: "anthropic" | "openai" | null;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [step, setStep] = useState<1 | 2>(1);
  const [proxy, setProxy] = useState("");
  const [label, setLabel] = useState("");
  const [maxC, setMaxC] = useState("5");
  const [group, setGroup] = useState("");
  const [sess, setSess] = useState<{
    session_id: string;
    auth_url: string;
    redirect_uri: string;
  } | null>(null);
  const [callback, setCallback] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    // Reset state when dialog opens with a fresh provider.
    if (provider) {
      setStep(1);
      setProxy("");
      setLabel("");
      setMaxC("5");
      setGroup("");
      setSess(null);
      setCallback("");
    }
  }, [provider]);

  if (!provider) return null;

  const copy =
    provider === "anthropic"
      ? {
          title: "Sign in with Claude",
          intro:
            "We'll generate a Claude OAuth login URL. If this server can't reach claude.ai / api.anthropic.com directly, set a proxy — it's used for the token exchange and every subsequent request with this credential.",
          primary: "Generate login URL",
        }
      : {
          title: "Sign in with ChatGPT (Codex)",
          intro:
            "We'll generate a ChatGPT Codex OAuth login URL. If this server can't reach auth.openai.com / chatgpt.com directly, set a proxy — it's used for the token exchange and every subsequent request with this credential.",
          primary: "Generate login URL",
        };

  const start = async () => {
    setBusy(true);
    try {
      const r = await apiPost<{ session_id: string; auth_url: string; redirect_uri: string }>(
        "/admin/credentials/oauth/start",
        {
          provider,
          proxy_url: proxy,
          label,
        },
      );
      setSess({ session_id: r.session_id, auth_url: r.auth_url, redirect_uri: r.redirect_uri });
      setStep(2);
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  const copyURL = async () => {
    if (!sess) return;
    try {
      await navigator.clipboard.writeText(sess.auth_url);
      toast.success("Login URL copied");
    } catch {
      // ignore
    }
  };

  const finish = async () => {
    if (!sess) return;
    setBusy(true);
    try {
      await apiPost("/admin/credentials/oauth/finish", {
        session_id: sess.session_id,
        callback: callback.trim(),
        max_concurrent: Number(maxC) || 0,
        group,
      });
      toast.success("OAuth credential added");
      onCreated();
      onClose();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={!!provider} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{copy.title}</DialogTitle>
        </DialogHeader>
        {step === 1 && (
          <div className="grid gap-3 py-2">
            <p className="text-sm text-muted-foreground">{copy.intro}</p>
            <div className="space-y-2">
              <Label>Proxy URL (optional)</Label>
              <Input
                placeholder="http:// or socks5://"
                value={proxy}
                onChange={(e) => setProxy(e.target.value)}
                className="font-mono"
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label>Label</Label>
                <Input
                  value={label}
                  onChange={(e) => setLabel(e.target.value)}
                  placeholder="team-a"
                />
              </div>
              <div className="space-y-2">
                <Label>Max concurrent</Label>
                <Input
                  type="number"
                  min={0}
                  value={maxC}
                  onChange={(e) => setMaxC(e.target.value)}
                />
              </div>
            </div>
            <div className="space-y-2">
              <Label>Group (optional)</Label>
              <Input
                value={group}
                onChange={(e) => setGroup(e.target.value)}
                placeholder="empty = public"
              />
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button disabled={busy} onClick={start}>
                {busy ? "Starting…" : copy.primary}
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === 2 && sess && (
          <div className="grid gap-3 py-2">
            <div className="space-y-2 text-sm text-muted-foreground">
              <p>
                <b>1.</b> Copy the login URL and open it in a browser where you can sign in:
              </p>
            </div>
            <div className="space-y-2">
              <Label>Login URL</Label>
              <div className="flex gap-2">
                <Input
                  readOnly
                  onFocus={(e) => e.currentTarget.select()}
                  className="flex-1 font-mono text-xs bg-muted"
                  value={sess.auth_url}
                />
                <Button variant="outline" onClick={copyURL}>
                  Copy
                </Button>
              </div>
            </div>
            <div className="text-sm text-muted-foreground space-y-2 pt-2">
              <p>
                <b>2.</b> After you authorize, the browser redirects to{" "}
                <code className="font-mono break-all">{sess.redirect_uri}?code=…&amp;state=…</code>.
                That page usually fails to load — that's fine.
              </p>
              <p>
                <b>3.</b> Copy the full URL from the address bar (or the{" "}
                <code className="font-mono">code#state</code> string shown on a manual-copy page)
                and paste it below.
              </p>
            </div>
            <div className="space-y-2">
              <Label>Callback URL or code#state</Label>
              <Textarea
                className="font-mono text-xs h-28"
                placeholder={`${sess.redirect_uri}?code=xxx&state=yyy`}
                value={callback}
                onChange={(e) => setCallback(e.target.value)}
              />
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setStep(1)}>
                Back
              </Button>
              <Button disabled={busy || !callback.trim()} onClick={finish}>
                {busy ? "Exchanging…" : "Finish"}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function HealthPill({ cred }: { cred: Credential }) {
  // Tooltip composer — always prefer a multi-line tooltip carrying every
  // diagnostic we have, so hovering tells the whole story.
  const tip = (lines: (string | false | undefined)[]) => lines.filter(Boolean).join("\n");
  const frozen: string | undefined = cred?.refresh_suspended
    ? `刷新已冻结：${cred.refresh_suspended_reason || "需手动恢复"}`
    : undefined;

  if (cred.disabled) {
    return (
      <span
        className="rounded border border-muted-foreground/30 bg-muted/40 px-2 py-0.5 text-xs font-mono uppercase text-muted-foreground"
        title={tip(["已禁用 — 不接受新流量", frozen])}
      >
        disabled
      </span>
    );
  }
  if (cred.hard_failure) {
    return (
      <span
        className="rounded border border-destructive/30 bg-destructive/15 px-2 py-0.5 text-xs font-mono uppercase text-destructive"
        title={tip([
          "硬失败 — 需点击「标记为健康」才会重新参与调度",
          cred.failure_reason && `原因: ${cred.failure_reason}`,
          frozen,
        ])}
      >
        hard fail
      </span>
    );
  }
  if (cred.quota_exceeded) {
    return (
      <span
        className="rounded border border-warning/30 bg-warning/15 px-2 py-0.5 text-xs font-mono uppercase text-warning"
        title={tip([
          "配额已耗尽",
          cred.quota_reset_at && `重置于 ${new Date(cred.quota_reset_at).toLocaleString()}`,
        ])}
      >
        quota
      </span>
    );
  }
  if (cred.healthy) {
    return (
      <span
        className="rounded border border-success/30 bg-success/15 px-2 py-0.5 text-xs font-mono uppercase text-success"
        title={tip(["健康", cred.failure_reason && `最近一次错误: ${cred.failure_reason}`])}
      >
        ok
      </span>
    );
  }
  return (
    <span
      className="rounded border border-warning/30 bg-warning/15 px-2 py-0.5 text-xs font-mono uppercase text-warning"
      title={tip([
        "短时冷却中 — 累计失败但未到硬失败阈值",
        cred.failure_reason && `原因: ${cred.failure_reason}`,
      ])}
    >
      cooldown
    </span>
  );
}

function Expiry({ cred }: { cred: Credential }) {
  const at: string | undefined = cred?.expires_at;
  if (!at || at.startsWith("0001-")) return <span className="text-muted-foreground">—</span>;
  const d = new Date(at);
  const now = Date.now();
  const dt = d.getTime() - now;
  const days = Math.round(dt / 86400000);
  const absolute = d.toLocaleString();
  const rel = dt < 0 ? `${-days}d ago` : days < 1 ? `${Math.round(dt / 3600000)}h` : `${days}d`;

  // Background refresher in cc-core/auth.Pool.RefreshExpiring skips disabled
  // and hard-failed creds. When that's the case, the "expired Xd ago" text
  // is misleading — refresh isn't being attempted. Show "frozen" instead and
  // route the actual reason into the tooltip.
  if (cred?.refresh_suspended) {
    const suspendReason: string =
      cred.refresh_suspended_reason || (cred.disabled ? "credential disabled" : "hard failure");
    const tip = `Token exp: ${absolute}\n刷新已冻结 · ${suspendReason}`;
    return (
      <span className="inline-flex items-center gap-1 text-muted-foreground" title={tip}>
        <ShieldOff className="size-3 text-destructive/70" />
        <span className="font-mono text-xs">冻结</span>
        <span className="text-[10px] opacity-60">({dt < 0 ? `${rel} 已过` : `还剩 ${rel}`})</span>
      </span>
    );
  }

  const cls = dt < 0 ? "text-destructive" : days < 7 ? "text-warning" : "text-muted-foreground";
  const text = dt < 0 ? `expired ${rel}` : rel;
  return (
    <span className={cls} title={absolute}>
      {text}
    </span>
  );
}

interface CredentialDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onCreated: () => void;
}

function AddAPIKeyDialog({
  open,
  onOpenChange,
  provider,
  onCreated,
}: CredentialDialogProps & { provider: string }) {
  const { t } = useTranslation();
  const [key, setKey] = useState("");
  const [label, setLabel] = useState("");
  const [base, setBase] = useState("");
  const [proxy, setProxy] = useState("");
  const [group, setGroup] = useState("");
  const [modelMap, setModelMap] = useState("");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>
            {t("admin.creds.newApiTitle")} ·{" "}
            {provider === "openai" ? "Codex (OpenAI)" : "Claude (Anthropic)"}
          </DialogTitle>
        </DialogHeader>
        <div className="grid gap-3 py-2">
          <div className="space-y-2">
            <Label>API key</Label>
            <Input
              type="password"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder={t("admin.creds.newApiPlaceholder")}
              className="font-mono"
            />
          </div>
          <div className="space-y-2">
            <Label>{t("admin.creds.cols.label")}</Label>
            <Input
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder={t("admin.creds.newApiLabelPlaceholder")}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label>Base URL</Label>
              <Input
                value={base}
                onChange={(e) => setBase(e.target.value)}
                placeholder={t("admin.creds.newApiBaseUrlPlaceholder")}
              />
            </div>
            <div className="space-y-2">
              <Label>Proxy URL</Label>
              <Input value={proxy} onChange={(e) => setProxy(e.target.value)} />
            </div>
          </div>
          <div className="space-y-2">
            <Label>{t("admin.creds.cols.group")}</Label>
            <Input value={group} onChange={(e) => setGroup(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>{t("admin.creds.modelMapLabel")}</Label>
            <Textarea
              value={modelMap}
              onChange={(e) => setModelMap(e.target.value)}
              placeholder={'{\n  "claude-opus-4-6": "claude-opus-4-8"\n}'}
              className="font-mono text-xs min-h-[72px]"
            />
            <p className="text-[11px] text-muted-foreground">{t("admin.creds.modelMapHint")}</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            onClick={async () => {
              if (!key) {
                toast.error(t("common.error"));
                return;
              }
              let model_map: Record<string, string> = {};
              if (modelMap.trim() !== "") {
                try {
                  model_map = JSON.parse(modelMap);
                } catch (e) {
                  toast.error(t("admin.creds.modelMapParseError", { msg: errMsg(e) }));
                  return;
                }
              }
              try {
                await apiPost("/admin/credentials/apikey", {
                  provider,
                  key,
                  label,
                  base_url: base,
                  proxy_url: proxy,
                  group,
                  model_map,
                });
                toast.success(t("admin.creds.newApiCreated"));
                onCreated();
                onOpenChange(false);
                setKey("");
                setLabel("");
                setBase("");
                setProxy("");
                setGroup("");
                setModelMap("");
              } catch (e) {
                toast.error(errMsg(e));
              }
            }}
          >
            {t("common.add")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// UploadJSONDialog persists a raw credential JSON (exported from another
// instance) into the pool. Works for both OAuth and API-key shapes — the
// backend infers the kind from the JSON's `type` field.
function UploadJSONDialog({
  open,
  onOpenChange,
  provider,
  onCreated,
}: CredentialDialogProps & { provider: string }) {
  const { t } = useTranslation();
  const [content, setContent] = useState("");
  const [label, setLabel] = useState("");
  const [proxy, setProxy] = useState("");
  const [group, setGroup] = useState("");
  const [busy, setBusy] = useState(false);
  const reset = () => {
    setContent("");
    setLabel("");
    setProxy("");
    setGroup("");
  };
  const submit = async () => {
    if (!content.trim()) {
      toast.error(t("admin.creds.uploadEmpty"));
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(content);
    } catch (e) {
      toast.error(t("admin.creds.uploadParseError", { msg: errMsg(e) }));
      return;
    }
    setBusy(true);
    try {
      const r = await apiPost<{ label?: string; id?: string }>("/admin/credentials/upload", {
        content: parsed,
        provider,
        label: label.trim(),
        proxy_url: proxy.trim(),
        group: group.trim(),
      });
      toast.success(t("admin.creds.uploaded", { label: r.label || r.id || "" }));
      onCreated();
      onOpenChange(false);
      reset();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>
            {t("admin.creds.uploadTitle", { provider: provider === "openai" ? "Codex" : "Claude" })}
          </DialogTitle>
        </DialogHeader>
        <div className="grid gap-3 py-2">
          <p className="text-xs text-muted-foreground">{t("admin.creds.uploadDesc")}</p>
          <div className="space-y-2">
            <Label>{t("admin.creds.uploadContentLabel")}</Label>
            <Textarea
              value={content}
              onChange={(e) => setContent(e.target.value)}
              placeholder={'{\n  "type": "oauth",\n  "provider": "anthropic",\n  …\n}'}
              className="font-mono text-xs min-h-[160px]"
            />
          </div>
          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-2">
              <Label>{t("admin.creds.cols.label")}</Label>
              <Input value={label} onChange={(e) => setLabel(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label>Proxy URL</Label>
              <Input value={proxy} onChange={(e) => setProxy(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label>{t("admin.creds.cols.group")}</Label>
              <Input value={group} onChange={(e) => setGroup(e.target.value)} />
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button disabled={busy} onClick={submit}>
            {busy ? t("admin.creds.uploadBusy") : t("admin.creds.uploadConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// KiroCredentialsPanel renders the Kiro side-channel credentials with
// live `getUsageLimits` balance lookups. Add-credential flow runs as a
// PKCE login: the admin starts /login/start, opens the returned signin
// URL in a new tab, manually pastes the code+state back to /login/finish.
// (Browser-side OAuth callback would need to live on :3128 which is the
// hardcoded kiro redirect_uri; outside scope for now.)
function KiroCredentialsPanel() {
  type Entry = {
    id: string;
    label: string;
    group?: string;
    disabled: boolean;
    max_concurrent?: number;
    active?: number;
    created_at: string;
    profile_arn?: string;
    email?: string;
    plan?: string;
    masked_token?: string;
    expires_at?: string;
  };
  type Balance = {
    plan?: string;
    used?: number;
    limit?: number;
    remaining?: number;
    reset_at?: string;
  };
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [creds, setCreds] = useState<Entry[]>([]);
  const [balances, setBalances] = useState<Record<string, Balance>>({});
  const [adding, setAdding] = useState(false);
  const [busyID, setBusyID] = useState("");
  const reload = async () => {
    const r = await apiGet<{ credentials: Entry[] }>("/admin/kiro/credentials");
    setCreds(r.credentials || []);
  };
  // biome-ignore lint/correctness/useExhaustiveDependencies: load once on mount; reload runs imperatively after mutations.
  useEffect(() => {
    reload();
  }, []);
  const loadBalance = async (id: string) => {
    try {
      const r = await apiGet<Balance>(`/admin/kiro/credentials/${id}/credits`);
      setBalances((b) => ({ ...b, [id]: r }));
    } catch (e) {
      toast.error(`balance: ${errMsg(e)}`);
    }
  };
  return (
    <div className="space-y-4">
      <GlassPanel
        title="Kiro 凭证"
        description="Amazon Q / Kiro 账户。请求经由 kirobridge 转换为 Smithy + event-stream 上送 q.us-east-1。仅在 token_groups 中的 kiro-anthropic 分组(默认 5% 折扣) 使用。"
        action={<Button onClick={() => setAdding(true)}>添加凭证 (PKCE)</Button>}
        bodyClassName="p-0"
      >
        {creds.length === 0 ? (
          <div className="p-12 text-center text-sm text-muted-foreground">
            暂无 Kiro 凭证。点击「添加凭证」走 PKCE 登录,或使用 standalone CLI 工具
            (`/tmp/kiro-roundtrip/kirortrip login` → 将生成的 credentials.json 拷贝到服务器的
            kiro_auth_dir/&lt;id&gt;.json)。
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>标签</TableHead>
                <TableHead>ID</TableHead>
                <TableHead>计划</TableHead>
                <TableHead className="text-right">并发</TableHead>
                <TableHead className="text-right">使用 / 配额</TableHead>
                <TableHead>过期</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {creds.map((c) => {
                const bal = balances[c.id];
                const busy = busyID === c.id;
                return (
                  <TableRow key={c.id} className={c.disabled ? "opacity-50" : ""}>
                    <TableCell>
                      <div className="font-medium">{c.label || "(unnamed)"}</div>
                      {c.email && <div className="text-xs text-muted-foreground">{c.email}</div>}
                      {c.masked_token && (
                        <code className="text-[11px] text-muted-foreground">{c.masked_token}</code>
                      )}
                    </TableCell>
                    <TableCell>
                      <code className="text-xs">{c.id}</code>
                    </TableCell>
                    <TableCell>
                      <span className="rounded bg-muted px-1.5 py-0.5 text-xs font-mono">
                        {bal?.plan ?? c.plan ?? "—"}
                      </span>
                    </TableCell>
                    <TableCell className="text-right tabular-nums font-mono text-xs">
                      {c.active ?? 0} /{" "}
                      {c.max_concurrent && c.max_concurrent > 0 ? c.max_concurrent : "∞"}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {bal ? (
                        <div>
                          <div>
                            {bal.used?.toFixed(2)} / {bal.limit?.toFixed(2)}
                          </div>
                          {typeof bal.remaining === "number" && (
                            <div className="text-xs text-muted-foreground">
                              剩余 {bal.remaining.toFixed(2)}
                            </div>
                          )}
                        </div>
                      ) : (
                        <Button size="sm" variant="ghost" onClick={() => loadBalance(c.id)}>
                          查询
                        </Button>
                      )}
                    </TableCell>
                    <TableCell>
                      <span className="text-xs text-muted-foreground">{c.expires_at || "—"}</span>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="icon"
                        variant="ghost"
                        className="h-8 w-8"
                        title={c.disabled ? "启用" : "禁用"}
                        disabled={busy}
                        onClick={async () => {
                          setBusyID(c.id);
                          try {
                            await apiPatch(`/admin/kiro/credentials/${c.id}`, {
                              disabled: !c.disabled,
                            });
                            toast.success(c.disabled ? "已启用" : "已禁用");
                            await reload();
                          } catch (e) {
                            toast.error(errMsg(e));
                          } finally {
                            setBusyID("");
                          }
                        }}
                      >
                        {c.disabled ? (
                          <CheckCircle2 className="h-3.5 w-3.5" />
                        ) : (
                          <Ban className="h-3.5 w-3.5" />
                        )}
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        className="h-8 w-8 text-destructive hover:bg-destructive/10"
                        disabled={busy}
                        onClick={async () => {
                          if (
                            !(await confirm({
                              title: t("common.delete"),
                              description: t("admin.creds.kiroConfirmDelete", {
                                name: c.label || c.id,
                              }),
                              confirmLabel: t("common.delete"),
                              destructive: true,
                            }))
                          )
                            return;
                          setBusyID(c.id);
                          try {
                            await apiDelete(`/admin/kiro/credentials/${c.id}`);
                            toast.success("已删除");
                            await reload();
                          } catch (e) {
                            toast.error(errMsg(e));
                          } finally {
                            setBusyID("");
                          }
                        }}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </GlassPanel>
      <KiroAddDialog open={adding} onOpenChange={setAdding} onAdded={reload} />
    </div>
  );
}

function KiroAddDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean;
  onOpenChange: (b: boolean) => void;
  onAdded: () => void;
}) {
  const [step, setStep] = useState<1 | 2>(1);
  const [label, setLabel] = useState("");
  const [proxy, setProxy] = useState("");
  const [group, setGroup] = useState("");
  const [maxC, setMaxC] = useState("5");
  const [sess, setSess] = useState<{
    signin_url: string;
    state: string;
    redirect_uri: string;
  } | null>(null);
  const [callback, setCallback] = useState("");
  const [busy, setBusy] = useState(false);
  const reset = () => {
    setStep(1);
    setLabel("");
    setProxy("");
    setGroup("");
    setMaxC("5");
    setSess(null);
    setCallback("");
  };
  // biome-ignore lint/correctness/useExhaustiveDependencies: reset the wizard only when the dialog transitions open.
  useEffect(() => {
    if (open) reset();
  }, [open]);
  const start = async () => {
    setBusy(true);
    try {
      const r = await apiPost<{ signin_url: string; state: string; redirect_uri: string }>(
        "/admin/kiro/login/start",
        {
          label,
          proxy_url: proxy,
          redirect_uri: "http://localhost:3128",
        },
      );
      setSess({ signin_url: r.signin_url, state: r.state, redirect_uri: r.redirect_uri });
      setStep(2);
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };
  const copyURL = async () => {
    if (!sess) return;
    try {
      await navigator.clipboard.writeText(sess.signin_url);
      toast.success("Login URL copied");
    } catch {
      /* ignore */
    }
  };
  const finish = async () => {
    if (!sess) return;
    setBusy(true);
    try {
      await apiPost("/admin/kiro/login/finish", {
        callback: callback.trim(),
        state: sess.state,
        group,
        max_concurrent: Number(maxC) || 0,
      });
      toast.success("Kiro 凭证已添加");
      onAdded();
      onOpenChange(false);
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(b) => {
        onOpenChange(b);
        if (!b) reset();
      }}
    >
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>添加 Kiro 凭证(PKCE 登录)</DialogTitle>
        </DialogHeader>
        {step === 1 && (
          <div className="grid gap-3 py-2">
            <p className="text-sm text-muted-foreground">
              将生成一个 app.kiro.dev 登录链接。如果服务器无法直连 kiro.dev / amazonaws.com，
              请填代理 — 这条代理同时用于本次令牌交换以及后续刷新该凭证时的出站请求。
            </p>
            <div className="space-y-2">
              <Label>Proxy URL (optional)</Label>
              <Input
                placeholder="http:// or socks5://"
                value={proxy}
                onChange={(e) => setProxy(e.target.value)}
                className="font-mono"
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label>Label</Label>
                <Input
                  value={label}
                  onChange={(e) => setLabel(e.target.value)}
                  placeholder="alice@example.com"
                />
              </div>
              <div className="space-y-2">
                <Label>Max concurrent</Label>
                <Input
                  type="number"
                  min={0}
                  value={maxC}
                  onChange={(e) => setMaxC(e.target.value)}
                />
              </div>
            </div>
            <div className="space-y-2">
              <Label>Group (optional)</Label>
              <Input
                value={group}
                onChange={(e) => setGroup(e.target.value)}
                placeholder="empty = default"
              />
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button disabled={busy} onClick={start}>
                {busy ? "Starting…" : "Generate login URL"}
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === 2 && sess && (
          <div className="grid gap-3 py-2">
            <div className="space-y-2 text-sm text-muted-foreground">
              <p>
                <b>1.</b> 复制下方登录链接，在浏览器中打开并完成授权：
              </p>
            </div>
            <div className="space-y-2">
              <Label>Login URL</Label>
              <div className="flex gap-2">
                <Input
                  readOnly
                  onFocus={(e) => e.currentTarget.select()}
                  className="flex-1 font-mono text-xs bg-muted"
                  value={sess.signin_url}
                />
                <Button variant="outline" onClick={copyURL}>
                  Copy
                </Button>
              </div>
            </div>
            <div className="text-sm text-muted-foreground space-y-2 pt-2">
              <p>
                <b>2.</b> 授权完成后，浏览器会跳转到{" "}
                <code className="font-mono break-all">
                  {sess.redirect_uri}/oauth/callback?code=…&amp;state=…&amp;login_option=…
                </code>
                。 页面通常加载失败 — 没关系。
              </p>
              <p>
                <b>3.</b> 从地址栏复制完整 URL 粘贴到下面，提交后由服务器内部完成 token 交换 +
                刷新链路写入。
              </p>
            </div>
            <div className="space-y-2">
              <Label>Callback URL</Label>
              <Textarea
                className="font-mono text-xs h-28"
                placeholder={`${sess.redirect_uri}/oauth/callback?code=…&state=…&login_option=github`}
                value={callback}
                onChange={(e) => setCallback(e.target.value)}
              />
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setStep(1)}>
                Back
              </Button>
              <Button disabled={busy || !callback.trim()} onClick={finish}>
                {busy ? "Exchanging…" : "Finish"}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

const ORDERS_PAGE = 50;

function PaymentsTab() {
  const { t } = useTranslation();
  const [orders, setOrders] = useState<AdminOrder[]>([]);
  const [offset, setOffset] = useState(0);
  // total is optional until the backend ships it — the title and Pager both
  // degrade gracefully when it's undefined.
  const [total, setTotal] = useState<number | undefined>(undefined);
  const [busy, setBusy] = useState(true);
  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    apiGet<{ orders: AdminOrder[]; total?: number }>(
      `/admin/orders?limit=${ORDERS_PAGE}&offset=${offset}`,
    )
      .then((r) => {
        if (cancelled) return;
        setOrders(r.orders || []);
        setTotal(r.total);
      })
      .catch((e) => {
        if (!cancelled) toast.error(errMsg(e));
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });
    return () => {
      cancelled = true;
    };
  }, [offset]);
  return (
    <div className="space-y-6">
      <Reveal>
        <GlassPanel
          title={t("admin.payments.heading", { n: total ?? orders.length })}
          description={t("admin.payments.sub")}
          bodyClassName="p-0"
        >
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("admin.payments.cols.order")}</TableHead>
                <TableHead>{t("admin.payments.cols.user")}</TableHead>
                <TableHead className="text-right">{t("admin.payments.cols.usd")}</TableHead>
                <TableHead>{t("admin.payments.cols.status")}</TableHead>
                <TableHead>{t("admin.payments.cols.created")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {orders.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="py-12 text-center text-muted-foreground">
                    {busy ? t("common.loading") : t("admin.payments.empty")}
                  </TableCell>
                </TableRow>
              )}
              {orders.map((o) => (
                <TableRow key={o.OutTradeNo}>
                  <TableCell className="font-mono text-xs">{o.OutTradeNo}</TableCell>
                  <TableCell>#{o.UserID}</TableCell>
                  <TableCell className="font-mono tabular-nums text-right">
                    {fmtUSD(o.USDCredit)}
                  </TableCell>
                  <TableCell>
                    <span
                      className={`rounded border px-2 py-0.5 text-xs font-mono uppercase ${o.Status === "paid" ? "border-success/30 bg-success/15 text-success" : "border-warning/30 bg-warning/15 text-warning"}`}
                    >
                      {o.Status === "paid"
                        ? t("common.paid")
                        : o.Status === "pending"
                          ? t("common.pending")
                          : o.Status === "expired"
                            ? t("common.expired")
                            : o.Status}
                    </span>
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {o.CreatedAt && new Date(o.CreatedAt).toLocaleString()}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <Pager
            offset={offset}
            limit={ORDERS_PAGE}
            total={total}
            count={orders.length}
            busy={busy}
            onChange={setOffset}
            className="border-t border-border px-4 py-3"
          />
        </GlassPanel>
      </Reveal>
      <AdjustmentsPanel />
    </div>
  );
}

const ADJUSTMENTS_PAGE = 50;

// AdjustmentsPanel surfaces wallet credits that arrived without a payment
// order — manual operator grants and channel signup bonuses (new-user
// rewards). Both are kind='adjust' server-side; a "signup_bonus:" ref marks
// the bonus, which this view badges distinctly.
function AdjustmentsPanel() {
  const { t } = useTranslation();
  const [rows, setRows] = useState<AdminAdjustment[]>([]);
  const [offset, setOffset] = useState(0);
  const [total, setTotal] = useState<number | undefined>(undefined);
  const [busy, setBusy] = useState(true);
  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    apiGet<{ adjustments: AdminAdjustment[]; total?: number }>(
      `/admin/adjustments?limit=${ADJUSTMENTS_PAGE}&offset=${offset}`,
    )
      .then((r) => {
        if (cancelled) return;
        setRows(r.adjustments || []);
        setTotal(r.total);
      })
      .catch((e) => {
        if (!cancelled) toast.error(errMsg(e));
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });
    return () => {
      cancelled = true;
    };
  }, [offset]);
  return (
    <Reveal>
      <GlassPanel
        title={t("admin.adjustments.heading", { n: total ?? rows.length })}
        description={t("admin.adjustments.sub")}
        bodyClassName="p-0"
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("admin.adjustments.cols.user")}</TableHead>
              <TableHead className="text-right">{t("admin.adjustments.cols.amount")}</TableHead>
              <TableHead>{t("admin.adjustments.cols.source")}</TableHead>
              <TableHead>{t("admin.adjustments.cols.note")}</TableHead>
              <TableHead>{t("admin.adjustments.cols.created")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-12 text-center text-muted-foreground">
                  {busy ? t("common.loading") : t("admin.adjustments.empty")}
                </TableCell>
              </TableRow>
            )}
            {rows.map((a) => {
              const isBonus = a.ref.startsWith("signup_bonus:");
              return (
                <TableRow key={a.id}>
                  <TableCell className="text-xs">{a.email || `#${a.user_id}`}</TableCell>
                  <TableCell
                    className={`font-mono tabular-nums text-right ${a.amount_usd >= 0 ? "text-success" : ""}`}
                  >
                    {a.amount_usd >= 0 ? "+" : ""}
                    {fmtUSD(a.amount_usd)}
                  </TableCell>
                  <TableCell>
                    <span
                      className={`rounded border px-2 py-0.5 text-xs font-medium ${isBonus ? "border-primary/30 bg-primary/15 text-primary" : "border-border bg-muted/40 text-muted-foreground"}`}
                    >
                      {isBonus ? t("admin.adjustments.bonus") : t("admin.adjustments.manual")}
                    </span>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">{a.note || "—"}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {new Date(a.created_at * 1000).toLocaleString()}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
        <Pager
          offset={offset}
          limit={ADJUSTMENTS_PAGE}
          total={total}
          count={rows.length}
          busy={busy}
          onChange={setOffset}
          className="border-t border-border px-4 py-3"
        />
      </GlassPanel>
    </Reveal>
  );
}
