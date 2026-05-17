import React, { useEffect, useState } from "react";
import { NavLink, Routes, Route } from "react-router-dom";
import { Users, Tag, KeyRound, ShoppingCart, LayoutDashboard, ScrollText, Gauge, Sparkles, ChevronRight, Pencil, RefreshCw, CheckCircle2, Trash2, AlertTriangle, ShieldOff, Ban } from "lucide-react";
import { useTranslation } from "react-i18next";
import { OverviewPanel } from "@/components/admin/overview-panel";
import { AdminDashboard } from "@/components/admin/admin-dashboard";
import { RequestsExplorer } from "@/components/admin/requests-explorer";
import { UpstreamUsageDialog } from "@/components/admin/upstream-usage-dialog";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { apiDelete, apiGet, apiPatch, apiPost } from "@/lib/api";
import { fmtUSD, fmtInt } from "@/lib/utils";
import { Sparkline as MiniSpark } from "@/components/admin/sparkline";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import type { PricingGroup } from "@/lib/types";

const TABS = [
  { to: "dashboard", labelKey: "admin.tabs.dashboard", icon: Sparkles },
  { to: "fleet", labelKey: "admin.tabs.fleet", icon: LayoutDashboard },
  { to: "users", labelKey: "admin.tabs.users", icon: Users },
  { to: "groups", labelKey: "admin.tabs.groups", icon: Tag },
  { to: "credentials", labelKey: "admin.tabs.credentials", icon: KeyRound },
  { to: "requests", labelKey: "admin.tabs.requests", icon: ScrollText },
  { to: "payments", labelKey: "admin.tabs.payments", icon: ShoppingCart },
];

export default function AdminPage() {
  const { t } = useTranslation();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">{t("admin.panelTitle")}</h1>
        <p className="text-muted-foreground">{t("admin.panelSub")}</p>
      </div>

      <div className="flex gap-1 border-b border-border">
        {TABS.map((tab) => (
          <NavLink
            key={tab.to}
            to={`/app/admin/${tab.to}`}
            end={tab.to === "dashboard"}
            className={({ isActive }) =>
              cn(
                "inline-flex items-center gap-2 border-b-2 px-4 py-2 text-sm transition-colors",
                isActive
                  ? "border-primary font-medium text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              )
            }
          >
            <tab.icon className="h-3.5 w-3.5" />
            {t(tab.labelKey)}
          </NavLink>
        ))}
      </div>

      <Routes>
        <Route index element={<AdminDashboard />} />
        <Route path="dashboard" element={<AdminDashboard />} />
        <Route path="fleet" element={<OverviewPanel refreshTick={0} />} />
        {/* legacy alias — earlier deeplinks pointed at /overview */}
        <Route path="overview" element={<OverviewPanel refreshTick={0} />} />
        <Route path="users" element={<UsersTab />} />
        <Route path="groups" element={<GroupsTab />} />
        <Route path="credentials" element={<CredentialsTab />} />
        <Route path="requests" element={<RequestsExplorer refreshTick={0} />} />
        <Route path="payments" element={<PaymentsTab />} />
      </Routes>
    </div>
  );
}

function UsersTab() {
  const { t } = useTranslation();
  const [users, setUsers] = useState<any[]>([]);
  const [groups, setGroups] = useState<PricingGroup[]>([]);
  const [q, setQ] = useState("");

  const reload = async () => {
    const [u, g] = await Promise.all([
      apiGet<any>(`/admin/users?q=${encodeURIComponent(q)}`),
      apiGet<{ groups: PricingGroup[] }>("/admin/groups"),
    ]);
    setUsers(u.users || []);
    setGroups(g.groups || []);
  };
  useEffect(() => { reload(); }, [q]);

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-4">
          <CardTitle>{t("admin.users.headingCount", { n: users.length })}</CardTitle>
          <Input placeholder={t("admin.users.searchPlaceholder")} value={q} onChange={(e) => setQ(e.target.value)} className="max-w-xs" />
        </div>
      </CardHeader>
      <CardContent className="p-0">
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
            {users.map((u) => (
              <TableRow key={u.id} className={u.disabled ? "opacity-50" : ""}>
                <TableCell className="font-medium">{u.email}</TableCell>
                <TableCell>
                  <span className={`rounded border px-2 py-0.5 text-xs font-mono uppercase tracking-wider ${u.role === "admin" ? "border-primary/40 bg-primary/15 text-primary" : "border-border bg-muted"}`}>{u.role}</span>
                </TableCell>
                <TableCell>{groups.find((g) => g.ID === u.group_id)?.Name || u.group_id}</TableCell>
                <TableCell className="font-mono tabular-nums text-right">{fmtUSD(u.balance_usd)}</TableCell>
                <TableCell>
                  <AdjustBalanceButton userID={u.id} onDone={reload} />
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    <select
                      className="rounded border border-border bg-card px-2 py-1 text-xs"
                      value={u.group_id}
                      onChange={async (e) => {
                        await apiPatch(`/admin/users/${u.id}`, { group_id: parseInt(e.target.value) });
                        toast.success(t("admin.users.groupUpdated"));
                        reload();
                      }}
                    >
                      {groups.map((g) => (
                        <option key={g.ID} value={g.ID}>{g.Name}</option>
                      ))}
                    </select>
                    <Button size="sm" variant="ghost" onClick={async () => {
                      await apiPatch(`/admin/users/${u.id}`, { role: u.role === "admin" ? "user" : "admin" });
                      toast.success(t("admin.users.roleUpdated"));
                      reload();
                    }}>{u.role === "admin" ? t("admin.users.makeUser") : t("admin.users.makeAdmin")}</Button>
                    <Button size="sm" variant="ghost" className="text-destructive" onClick={async () => {
                      await apiPatch(`/admin/users/${u.id}`, { disabled: !u.disabled });
                      toast.success(u.disabled ? t("admin.users.enabled") : t("admin.users.disabled"));
                      reload();
                    }}>{u.disabled ? t("admin.users.enable") : t("admin.users.disable")}</Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function AdjustBalanceButton({ userID, onDone }: { userID: number; onDone: () => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [delta, setDelta] = useState("");
  const [note, setNote] = useState("");
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>{t("admin.users.adjust")}</Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-[400px]">
          <DialogHeader><DialogTitle>{t("admin.users.adjustTitle")}</DialogTitle></DialogHeader>
          <div className="space-y-3 py-2">
            <div className="space-y-2">
              <Label>{t("admin.users.delta")}</Label>
              <Input type="number" step="0.01" placeholder={t("admin.users.deltaPlaceholder")} value={delta} onChange={(e) => setDelta(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label>{t("admin.users.note")}</Label>
              <Input placeholder={t("admin.users.notePlaceholder")} value={note} onChange={(e) => setNote(e.target.value)} />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>{t("common.cancel")}</Button>
            <Button onClick={async () => {
              await apiPost(`/admin/users/${userID}/balance`, { delta_usd: parseFloat(delta), note });
              toast.success(t("admin.users.balanceUpdated"));
              onDone();
              setOpen(false);
              setDelta(""); setNote("");
            }}>{t("admin.users.apply")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function GroupsTab() {
  const { t } = useTranslation();
  const [groups, setGroups] = useState<PricingGroup[]>([]);
  const reload = async () => {
    const r = await apiGet<{ groups: PricingGroup[] }>("/admin/groups");
    setGroups(r.groups || []);
  };
  useEffect(() => { reload(); }, []);
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle>{t("admin.groups.heading")}</CardTitle>
          <CreateGroupButton onDone={reload} />
        </div>
      </CardHeader>
      <CardContent className="p-0">
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
                <TableCell className="font-mono tabular-nums text-right">{g.ClaudeMultiplier.toFixed(2)}×</TableCell>
                <TableCell className="font-mono tabular-nums text-right">{g.CodexMultiplier.toFixed(2)}×</TableCell>
                <TableCell className="font-mono text-xs">{g.CredentialGroup || "—"}</TableCell>
                <TableCell className="text-right">
                  {!g.IsDefault && (
                    <Button size="sm" variant="ghost" className="text-destructive" onClick={async () => {
                      if (!confirm(t("admin.groups.confirmDelete", { name: g.Name }))) return;
                      await apiDelete(`/admin/groups/${g.ID}`);
                      toast.success(t("admin.groups.deleted"));
                      reload();
                    }}>{t("common.delete")}</Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function CreateGroupButton({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [claudeMult, setClaudeMult] = useState("0.3");
  const [codexMult, setCodexMult] = useState("0.05");
  const [credGroup, setCredGroup] = useState("");
  return (
    <>
      <Button onClick={() => setOpen(true)}>{t("admin.groups.newBtn")}</Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t("admin.groups.newTitle")}</DialogTitle></DialogHeader>
          <p className="text-xs text-muted-foreground">{t("admin.groups.newSub")}</p>
          <div className="grid gap-3 py-2">
            <div className="space-y-2"><Label>{t("admin.groups.labels.name")}</Label><Input value={name} onChange={(e) => setName(e.target.value)} /></div>
            <div className="space-y-2"><Label>{t("admin.groups.labels.desc")}</Label><Input value={desc} onChange={(e) => setDesc(e.target.value)} /></div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2"><Label>{t("admin.groups.labels.claudeMult")}</Label><Input value={claudeMult} onChange={(e) => setClaudeMult(e.target.value)} /></div>
              <div className="space-y-2"><Label>{t("admin.groups.labels.codexMult")}</Label><Input value={codexMult} onChange={(e) => setCodexMult(e.target.value)} /></div>
            </div>
            <div className="space-y-2"><Label>{t("admin.groups.labels.credGroup")}</Label><Input value={credGroup} onChange={(e) => setCredGroup(e.target.value)} /></div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>{t("common.cancel")}</Button>
            <Button onClick={async () => {
              await apiPost("/admin/groups", {
                name, description: desc,
                claude_multiplier: parseFloat(claudeMult),
                codex_multiplier: parseFloat(codexMult),
                credential_group: credGroup,
              });
              toast.success(t("admin.groups.created"));
              onDone();
              setOpen(false);
              setName(""); setDesc("");
            }}>{t("common.create")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function CredentialsTab() {
  const { t } = useTranslation();
  const [creds, setCreds] = useState<any[]>([]);
  const [openKey, setOpenKey] = useState(false);
  const [openOAuth, setOpenOAuth] = useState<null | "anthropic" | "openai">(null);
  const [usageFor, setUsageFor] = useState<{ id: string; label: string; provider: string } | null>(null);
  const [providerTab, setProviderTab] = useState<"anthropic" | "openai">("anthropic");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<any | null>(null);
  const [busyId, setBusyId] = useState<string>("");
  const reload = async () => {
    const r = await apiGet<any>("/admin/credentials");
    setCreds(r.credentials || []);
  };
  useEffect(() => { reload(); }, []);

  const toggleExpand = (id: string) => {
    const s = new Set(expanded);
    if (s.has(id)) s.delete(id); else s.add(id);
    setExpanded(s);
  };

  const runAction = async (c: any, kind: "refresh" | "clear-quota" | "clear-failure" | "toggle") => {
    setBusyId(c.id);
    try {
      if (kind === "toggle") {
        await apiPatch(`/admin/credentials/${encodeURIComponent(c.id)}`, { disabled: !c.disabled });
        toast.success(c.disabled ? "已启用" : "已禁用");
      } else {
        await apiPost(`/admin/credentials/${encodeURIComponent(c.id)}/${kind}`);
        toast.success(
          kind === "refresh" ? "已刷新 token" : kind === "clear-quota" ? "已清除配额标记" : "已标记为健康"
        );
      }
      await reload();
    } catch (e: any) {
      toast.error(e?.message || String(e));
    } finally {
      setBusyId("");
    }
  };

  const claudeCreds = creds.filter((c) => c.provider === "anthropic");
  const codexCreds = creds.filter((c) => c.provider === "openai");
  const visible = providerTab === "anthropic" ? claudeCreds : codexCreds;

  return (
    <div className="space-y-4">
      {/* Provider tabs */}
      <div className="flex gap-1 border-b border-border">
        {(["anthropic", "openai"] as const).map((p) => {
          const count = p === "anthropic" ? claudeCreds.length : codexCreds.length;
          return (
            <button
              key={p}
              onClick={() => setProviderTab(p)}
              className={cn(
                "inline-flex items-center gap-2 border-b-2 px-4 py-2 text-sm transition-colors",
                providerTab === p
                  ? "border-primary font-medium text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              )}
            >
              {p === "anthropic" ? t("admin.creds.claudeTab") : t("admin.creds.codexTab")}
              <span className="rounded-full bg-muted px-1.5 py-0.5 text-xs font-mono">{count}</span>
            </button>
          );
        })}
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-start justify-between gap-4">
            <div>
              <CardTitle>{providerTab === "anthropic" ? t("admin.creds.claudeTitle") : t("admin.creds.codexTitle")}</CardTitle>
              <CardDescription>{providerTab === "anthropic" ? t("admin.creds.claudeSub") : t("admin.creds.codexSub")}</CardDescription>
            </div>
            <div className="flex gap-2 flex-wrap justify-end">
              <Button onClick={() => setOpenKey(true)}>{t("admin.creds.addApiKey")}</Button>
              <Button variant="outline" onClick={() => setOpenOAuth("anthropic")}>{t("admin.creds.addOauthClaude")}</Button>
              <Button variant="outline" onClick={() => setOpenOAuth("openai")}>{t("admin.creds.addOauthCodex")}</Button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {visible.length === 0 ? (
            <div className="p-12 text-center text-sm text-muted-foreground">{t("tokens.none")}</div>
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
                  const slotPct = c.max_concurrent > 0
                    ? Math.min(100, Math.round((c.active_clients / c.max_concurrent) * 100))
                    : 0;
                  const u = c.usage;
                  const busy = busyId === c.id;
                  return (
                    <React.Fragment key={c.id}>
                      <TableRow className={c.disabled ? "opacity-50" : ""}>
                        <TableCell>
                          <button
                            onClick={() => toggleExpand(c.id)}
                            className="text-muted-foreground hover:text-foreground transition-transform"
                            aria-label={isOpen ? "收起" : "展开"}
                          >
                            <ChevronRight className={cn("size-4 transition-transform", isOpen && "rotate-90")} />
                          </button>
                        </TableCell>
                        <TableCell>
                          <div className="font-medium">{c.label}</div>
                          <code className="text-xs text-muted-foreground">{c.id}</code>
                          {c.email && <div className="text-xs text-muted-foreground truncate max-w-[260px]">{c.email}</div>}
                          {/* Always-visible failure reason. Mirrors the AlertStrip
                              inside the expanded detail panel, so operators don't have to
                              click to see *why* a credential is in trouble. */}
                          {c.failure_reason && !c.quota_exceeded && (
                            <div
                              className={cn(
                                "mt-1 flex items-start gap-1 text-[11px] font-mono leading-tight max-w-[280px]",
                                c.hard_failure ? "text-destructive" : "text-warning"
                              )}
                              title={c.failure_reason}
                            >
                              {c.hard_failure
                                ? <ShieldOff className="size-3 shrink-0 mt-0.5" />
                                : <AlertTriangle className="size-3 shrink-0 mt-0.5" />}
                              <span className="truncate">{c.failure_reason}</span>
                            </div>
                          )}
                          {c.quota_exceeded && (
                            <div className="mt-1 flex items-start gap-1 text-[11px] font-mono leading-tight text-warning max-w-[280px]"
                              title={c.quota_reset_at ? `Resets ${new Date(c.quota_reset_at).toLocaleString()}` : "no reset time"}>
                              <AlertTriangle className="size-3 shrink-0 mt-0.5" />
                              <span className="truncate">
                                配额耗尽{c.quota_reset_at ? ` · 重置 ${new Date(c.quota_reset_at).toLocaleString()}` : " · 无重置时间"}
                              </span>
                            </div>
                          )}
                          {c.last_client_cancel && Date.now() - new Date(c.last_client_cancel).getTime() < 3600 * 1000 && (
                            <div className="mt-1 flex items-start gap-1 text-[11px] font-mono leading-tight text-muted-foreground max-w-[280px]"
                              title={`${new Date(c.last_client_cancel).toLocaleString()}${c.client_cancel_reason ? ` · ${c.client_cancel_reason}` : ""}`}>
                              <Ban className="size-3 shrink-0 mt-0.5" />
                              <span className="truncate">
                                客户端取消{c.client_cancel_reason ? ` · ${c.client_cancel_reason}` : ""}
                              </span>
                            </div>
                          )}
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-col gap-0.5">
                            <span className="rounded border border-border bg-muted px-2 py-0.5 text-xs font-mono uppercase w-fit">{c.kind}</span>
                            {c.plan_type && <span className="text-[10px] font-mono uppercase text-muted-foreground">{c.plan_type}</span>}
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
                                className={cn("h-full", slotPct > 80 ? "bg-warning" : "bg-success")}
                                style={{ width: `${slotPct}%` }}
                              />
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="font-mono tabular-nums text-right text-xs">
                          {u ? (
                            <>
                              <div>{fmtInt(u.sum_24h.input_tokens + u.sum_24h.output_tokens)}</div>
                              <div className="text-muted-foreground">{fmtInt(u.sum_24h.requests || 0)} req</div>
                            </>
                          ) : <span className="text-muted-foreground">—</span>}
                        </TableCell>
                        <TableCell className="font-mono tabular-nums text-right text-xs">
                          {u ? (
                            <>
                              <div>{fmtUSD(u.total_cost_usd)}</div>
                              <div className="text-muted-foreground">{fmtInt(u.total?.requests || 0)} req</div>
                            </>
                          ) : <span className="text-muted-foreground">—</span>}
                        </TableCell>
                        <TableCell className="font-mono text-xs"><Expiry cred={c} /></TableCell>
                        <TableCell><HealthPill cred={c} /></TableCell>
                        <TableCell className="text-right">
                          <div className="flex items-center justify-end gap-1">
                            <Button size="sm" variant="ghost" title="编辑" onClick={() => setEditing(c)}>
                              <Pencil className="size-3.5" />
                            </Button>
                            {c.kind === "oauth" && (
                              <Button size="sm" variant="ghost" title="刷新 token" disabled={busy} onClick={() => runAction(c, "refresh")}>
                                <RefreshCw className={cn("size-3.5", busy && "animate-spin")} />
                              </Button>
                            )}
                            {c.kind === "oauth" && (c.provider === "anthropic" || c.provider === "openai") && (
                              <Button
                                size="sm"
                                variant="ghost"
                                title={c.provider === "openai" ? "chatgpt.com wham/usage 主动探针" : "后端配额"}
                                onClick={() => setUsageFor({ id: c.id, label: c.label, provider: c.provider })}
                              >
                                <Gauge className="size-3.5" />
                              </Button>
                            )}
                            {c.quota_exceeded && (
                              <Button size="sm" variant="ghost" title="清除配额标记" disabled={busy} onClick={() => runAction(c, "clear-quota")}>
                                <CheckCircle2 className="size-3.5 text-warning" />
                              </Button>
                            )}
                            {(c.hard_failure || (!c.healthy && !c.quota_exceeded && !c.disabled)) && (
                              <Button size="sm" variant="ghost" title="标记为健康" disabled={busy} onClick={() => runAction(c, "clear-failure")}>
                                <CheckCircle2 className="size-3.5 text-success" />
                              </Button>
                            )}
                            <Button size="sm" variant="ghost" className="text-destructive" title="删除" onClick={async () => {
                              if (!confirm(t("admin.creds.confirmRemove", { name: c.label }))) return;
                              await apiDelete(`/admin/credentials/${encodeURIComponent(c.id)}`);
                              toast.success(t("admin.creds.removed"));
                              reload();
                            }}>
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
        </CardContent>
      </Card>

      <AddAPIKeyDialog open={openKey} onOpenChange={setOpenKey} onCreated={reload} />
      <AddOAuthDialog provider={openOAuth} onClose={() => setOpenOAuth(null)} onCreated={reload} />
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

function CredentialDetail({ c }: { c: any }) {
  const u = c.usage;
  const daily = (u?.daily || []) as { day: string; input_tokens?: number; output_tokens?: number; cache_read_tokens?: number; cache_create_tokens?: number }[];
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
            <AlertStrip tone="warning" icon={<AlertTriangle className="size-3.5" />} label="配额已耗尽">
              {c.quota_reset_at ? `重置于 ${new Date(c.quota_reset_at).toLocaleString()}` : "未报告重置时间"}
            </AlertStrip>
          )}
          {failureBanner && (
            <AlertStrip
              tone={c.hard_failure ? "error" : "warning"}
              icon={c.hard_failure ? <ShieldOff className="size-3.5" /> : <AlertTriangle className="size-3.5" />}
              label={c.hard_failure ? "硬失败" : "近期失败"}
            >
              {c.failure_reason}
            </AlertStrip>
          )}
          {c.refresh_suspended && (
            <AlertStrip
              tone="error"
              icon={<ShieldOff className="size-3.5" />}
              label="刷新已冻结"
            >
              {c.refresh_suspended_reason || (c.disabled ? "credential disabled" : "hard failure")}
              {" · "}
              <span className="opacity-70">
                {c.disabled ? "启用后会自动恢复" : "执行「标记为健康」后会自动恢复"}
              </span>
            </AlertStrip>
          )}
          {c.last_client_cancel && Date.now() - new Date(c.last_client_cancel).getTime() < 3600 * 1000 && (
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
            <Row k="24h 输入/输出" v={`${fmtInt(u.sum_24h.input_tokens)} / ${fmtInt(u.sum_24h.output_tokens)}`} />
            {u.sum_24h.cache_read_tokens > 0 && (
              <Row k="24h 缓存读取" v={fmtInt(u.sum_24h.cache_read_tokens)} />
            )}
            <Row k="累计请求" v={`${fmtInt(u.total.requests)}${u.total.errors > 0 ? ` (${fmtInt(u.total.errors)} err)` : ""}`} />
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
        <div className="text-xs font-medium uppercase text-muted-foreground mb-2">近 14 天 token</div>
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
          {c.base_url && <Row k="Base URL" v={<span className="font-mono break-all">{c.base_url}</span>} />}
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
              <li key={t} className="truncate">{t}</li>
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
            {Object.keys(c.model_map).sort().map((k) => (
              <div key={k} className="break-all leading-relaxed">
                <span>{k}</span>
                {c.model_map[k] ? (
                  <>
                    <span className="text-muted-foreground"> → </span>
                    <span>{c.model_map[k]}</span>
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
    <div className={cn("flex items-center gap-2 rounded-md border px-3 py-1.5 text-xs", tones[tone])}>
      <span className="shrink-0">{icon}</span>
      <span className="font-medium uppercase tracking-wider text-[10px]">{label}</span>
      <span className="font-mono truncate ml-auto opacity-90 text-right max-w-[60%]">{children}</span>
    </div>
  );
}

function EditCredentialDialog({
  cred,
  onClose,
  onSaved,
}: {
  cred: any | null;
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
          : ""
      );
    }
  }, [cred]);

  if (!cred) return null;
  const isAPIKey = cred.kind === "apikey";

  const save = async () => {
    setBusy(true);
    try {
      const body: any = {
        label,
        group,
        proxy_url: proxy,
        max_concurrent: Number(maxC) || 0,
        disabled,
      };
      if (isAPIKey) {
        body.base_url = base;
        if (modelMap.trim() === "") {
          body.model_map = {};
        } else {
          try {
            body.model_map = JSON.parse(modelMap);
          } catch (e: any) {
            toast.error("model_map JSON 解析失败：" + e.message);
            setBusy(false);
            return;
          }
        }
      }
      await apiPatch(`/admin/credentials/${encodeURIComponent(cred.id)}`, body);
      toast.success("已保存");
      onSaved();
      onClose();
    } catch (e: any) {
      toast.error(e?.message || String(e));
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
              <Input value={group} onChange={(e) => setGroup(e.target.value)} placeholder="empty = public" />
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
          {isAPIKey && (
            <div className="space-y-2">
              <Label>Model map（JSON，仅 API key）</Label>
              <Textarea
                value={modelMap}
                onChange={(e) => setModelMap(e.target.value)}
                className="font-mono text-xs h-32"
                placeholder={'{\n  "claude-sonnet-4-5": "claude-3-5-sonnet-latest"\n}'}
              />
              <p className="text-[11px] text-muted-foreground">
                空值（如 <code className="font-mono">{`"opus": ""`}</code>）= 不改写直接放行；省略键 = 走默认路由。
              </p>
            </div>
          )}
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
          <Button variant="outline" onClick={onClose}>{t_cancel()}</Button>
          <Button disabled={busy} onClick={save}>{busy ? "保存中…" : "保存"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function t_cancel() { return "取消"; }

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
  const [sess, setSess] = useState<{ session_id: string; auth_url: string; redirect_uri: string } | null>(null);
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
      const r = await apiPost<any>("/admin/credentials/oauth/start", {
        provider,
        proxy_url: proxy,
        label,
      });
      setSess({ session_id: r.session_id, auth_url: r.auth_url, redirect_uri: r.redirect_uri });
      setStep(2);
    } catch (e: any) {
      toast.error(e.message);
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
    } catch (e: any) {
      toast.error(e.message);
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
                <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="team-a" />
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
              <Input value={group} onChange={(e) => setGroup(e.target.value)} placeholder="empty = public" />
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={onClose}>Cancel</Button>
              <Button disabled={busy} onClick={start}>
                {busy ? "Starting…" : copy.primary}
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === 2 && sess && (
          <div className="grid gap-3 py-2">
            <div className="space-y-2 text-sm text-muted-foreground">
              <p><b>1.</b> Copy the login URL and open it in a browser where you can sign in:</p>
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
                <Button variant="outline" onClick={copyURL}>Copy</Button>
              </div>
            </div>
            <div className="text-sm text-muted-foreground space-y-2 pt-2">
              <p>
                <b>2.</b> After you authorize, the browser redirects to{" "}
                <code className="font-mono break-all">{sess.redirect_uri}?code=…&amp;state=…</code>. That page usually fails to load — that's fine.
              </p>
              <p>
                <b>3.</b> Copy the full URL from the address bar (or the <code className="font-mono">code#state</code> string shown on a manual-copy page) and paste it below.
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
              <Button variant="outline" onClick={() => setStep(1)}>Back</Button>
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

function HealthPill({ cred }: { cred: any }) {
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
      >disabled</span>
    );
  }
  if (cred.hard_failure) {
    return (
      <span
        className="rounded border border-destructive/30 bg-destructive/15 px-2 py-0.5 text-xs font-mono uppercase text-destructive"
        title={tip(["硬失败 — 需点击「标记为健康」才会重新参与调度", cred.failure_reason && `原因: ${cred.failure_reason}`, frozen])}
      >hard fail</span>
    );
  }
  if (cred.quota_exceeded) {
    return (
      <span
        className="rounded border border-warning/30 bg-warning/15 px-2 py-0.5 text-xs font-mono uppercase text-warning"
        title={tip(["配额已耗尽", cred.quota_reset_at && `重置于 ${new Date(cred.quota_reset_at).toLocaleString()}`])}
      >quota</span>
    );
  }
  if (cred.healthy) {
    return (
      <span
        className="rounded border border-success/30 bg-success/15 px-2 py-0.5 text-xs font-mono uppercase text-success"
        title={tip(["健康", cred.failure_reason && `最近一次错误: ${cred.failure_reason}`])}
      >ok</span>
    );
  }
  return (
    <span
      className="rounded border border-warning/30 bg-warning/15 px-2 py-0.5 text-xs font-mono uppercase text-warning"
      title={tip(["短时冷却中 — 累计失败但未到硬失败阈值", cred.failure_reason && `原因: ${cred.failure_reason}`])}
    >cooldown</span>
  );
}

function Expiry({ cred }: { cred: any }) {
  const at: string | undefined = cred?.expires_at;
  if (!at || at.startsWith("0001-")) return <span className="text-muted-foreground">—</span>;
  const d = new Date(at);
  const now = Date.now();
  const dt = d.getTime() - now;
  const days = Math.round(dt / 86400000);
  const absolute = d.toLocaleString();
  const rel = dt < 0
    ? `${-days}d ago`
    : days < 1
    ? `${Math.round(dt / 3600000)}h`
    : `${days}d`;

  // Background refresher in cc-core/auth.Pool.RefreshExpiring skips disabled
  // and hard-failed creds. When that's the case, the "expired Xd ago" text
  // is misleading — refresh isn't being attempted. Show "frozen" instead and
  // route the actual reason into the tooltip.
  if (cred?.refresh_suspended) {
    const suspendReason: string = cred.refresh_suspended_reason || (cred.disabled ? "credential disabled" : "hard failure");
    const tip = `Token exp: ${absolute}\n刷新已冻结 · ${suspendReason}`;
    return (
      <span
        className="inline-flex items-center gap-1 text-muted-foreground"
        title={tip}
      >
        <ShieldOff className="size-3 text-destructive/70" />
        <span className="font-mono text-xs">冻结</span>
        <span className="text-[10px] opacity-60">({dt < 0 ? `${rel} 已过` : `还剩 ${rel}`})</span>
      </span>
    );
  }

  const cls =
    dt < 0
      ? "text-destructive"
      : days < 7
      ? "text-warning"
      : "text-muted-foreground";
  const text = dt < 0 ? `expired ${rel}` : rel;
  return <span className={cls} title={absolute}>{text}</span>;
}

function AddAPIKeyDialog({ open, onOpenChange, onCreated }: any) {
  const { t } = useTranslation();
  const [provider, setProvider] = useState("anthropic");
  const [key, setKey] = useState("");
  const [label, setLabel] = useState("");
  const [base, setBase] = useState("");
  const [proxy, setProxy] = useState("");
  const [group, setGroup] = useState("");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader><DialogTitle>{t("admin.creds.newApiTitle")}</DialogTitle></DialogHeader>
        <div className="grid gap-3 py-2">
          <div className="space-y-2">
            <Label>{t("admin.creds.cols.kind")}</Label>
            <select className="h-10 w-full rounded-md border border-border bg-card px-3 text-sm" value={provider} onChange={(e) => setProvider(e.target.value)}>
              <option value="anthropic">Anthropic</option>
              <option value="openai">OpenAI</option>
            </select>
          </div>
          <div className="space-y-2"><Label>API key</Label><Input type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder={t("admin.creds.newApiPlaceholder")} className="font-mono" /></div>
          <div className="space-y-2"><Label>{t("admin.creds.cols.label")}</Label><Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t("admin.creds.newApiLabelPlaceholder")} /></div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2"><Label>Base URL</Label><Input value={base} onChange={(e) => setBase(e.target.value)} placeholder={t("admin.creds.newApiBaseUrlPlaceholder")} /></div>
            <div className="space-y-2"><Label>Proxy URL</Label><Input value={proxy} onChange={(e) => setProxy(e.target.value)} /></div>
          </div>
          <div className="space-y-2"><Label>{t("admin.creds.cols.group")}</Label><Input value={group} onChange={(e) => setGroup(e.target.value)} /></div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button onClick={async () => {
            if (!key) { toast.error(t("common.error")); return; }
            try {
              await apiPost("/admin/credentials/apikey", { provider, key, label, base_url: base, proxy_url: proxy, group });
              toast.success(t("admin.creds.newApiCreated"));
              onCreated();
              onOpenChange(false);
              setKey(""); setLabel(""); setBase(""); setProxy(""); setGroup("");
            } catch (e: any) { toast.error(e.message); }
          }}>{t("common.add")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PaymentsTab() {
  const { t } = useTranslation();
  const [orders, setOrders] = useState<any[]>([]);
  useEffect(() => {
    apiGet<{ orders: any[] }>("/admin/orders").then((r) => setOrders(r.orders || []));
  }, []);
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("admin.payments.heading", { n: orders.length })}</CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("admin.payments.cols.order")}</TableHead>
              <TableHead>{t("admin.payments.cols.user")}</TableHead>
              <TableHead className="text-right">{t("admin.payments.cols.usd")}</TableHead>
              <TableHead className="text-right">{t("admin.payments.cols.cny")}</TableHead>
              <TableHead className="text-right">{t("admin.payments.cols.rate")}</TableHead>
              <TableHead>{t("admin.payments.cols.status")}</TableHead>
              <TableHead>{t("admin.payments.cols.created")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {orders.map((o) => (
              <TableRow key={o.OutTradeNo}>
                <TableCell className="font-mono text-xs">{o.OutTradeNo}</TableCell>
                <TableCell>#{o.UserID}</TableCell>
                <TableCell className="font-mono tabular-nums text-right">{fmtUSD(o.USDCredit)}</TableCell>
                <TableCell className="font-mono tabular-nums text-right">¥{o.CNYAmount?.toFixed(2)}</TableCell>
                <TableCell className="font-mono tabular-nums text-right text-muted-foreground">{o.Rate?.toFixed(4)}</TableCell>
                <TableCell><span className={`rounded border px-2 py-0.5 text-xs font-mono uppercase ${o.Status === "paid" ? "border-success/30 bg-success/15 text-success" : "border-warning/30 bg-warning/15 text-warning"}`}>{o.Status === "paid" ? t("common.paid") : o.Status === "pending" ? t("common.pending") : o.Status === "expired" ? t("common.expired") : o.Status}</span></TableCell>
                <TableCell className="text-muted-foreground">{o.CreatedAt && new Date(o.CreatedAt).toLocaleString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}
