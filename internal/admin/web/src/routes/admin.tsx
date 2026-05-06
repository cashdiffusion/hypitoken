import { useEffect, useState } from "react";
import { NavLink, Routes, Route } from "react-router-dom";
import { Users, Tag, KeyRound, ShoppingCart, LayoutDashboard, ScrollText, Gauge, Sparkles } from "lucide-react";
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
import { fmtUSD } from "@/lib/utils";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import type { PricingGroup } from "@/lib/types";

const TABS = [
  { to: "dashboard", label: "Dashboard", icon: Sparkles },
  { to: "fleet", label: "Fleet", icon: LayoutDashboard },
  { to: "users", label: "Users", icon: Users },
  { to: "groups", label: "Pricing groups", icon: Tag },
  { to: "credentials", label: "Credentials", icon: KeyRound },
  { to: "requests", label: "Requests", icon: ScrollText },
  { to: "payments", label: "Payments", icon: ShoppingCart },
];

export default function AdminPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Operator panel</h1>
        <p className="text-muted-foreground">Manage users, pricing groups, upstream credentials and payments.</p>
      </div>

      <div className="flex gap-1 border-b border-border">
        {TABS.map((t) => (
          <NavLink
            key={t.to}
            to={t.to}
            className={({ isActive }) =>
              cn(
                "inline-flex items-center gap-2 border-b-2 px-4 py-2 text-sm transition-colors",
                isActive
                  ? "border-primary font-medium text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              )
            }
          >
            <t.icon className="h-3.5 w-3.5" />
            {t.label}
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
          <CardTitle>Users ({users.length})</CardTitle>
          <Input placeholder="Search email…" value={q} onChange={(e) => setQ(e.target.value)} className="max-w-xs" />
        </div>
      </CardHeader>
      <CardContent className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Email</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>Group</TableHead>
              <TableHead className="text-right">Balance</TableHead>
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
                        toast.success("Group updated");
                        reload();
                      }}
                    >
                      {groups.map((g) => (
                        <option key={g.ID} value={g.ID}>{g.Name}</option>
                      ))}
                    </select>
                    <Button size="sm" variant="ghost" onClick={async () => {
                      await apiPatch(`/admin/users/${u.id}`, { role: u.role === "admin" ? "user" : "admin" });
                      toast.success("Role updated");
                      reload();
                    }}>{u.role === "admin" ? "→ user" : "→ admin"}</Button>
                    <Button size="sm" variant="ghost" className="text-destructive" onClick={async () => {
                      await apiPatch(`/admin/users/${u.id}`, { disabled: !u.disabled });
                      toast.success(u.disabled ? "Enabled" : "Disabled");
                      reload();
                    }}>{u.disabled ? "Enable" : "Disable"}</Button>
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
  const [open, setOpen] = useState(false);
  const [delta, setDelta] = useState("");
  const [note, setNote] = useState("");
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>Adjust</Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-[400px]">
          <DialogHeader><DialogTitle>Adjust balance</DialogTitle></DialogHeader>
          <div className="space-y-3 py-2">
            <div className="space-y-2">
              <Label>Delta (USD)</Label>
              <Input type="number" step="0.01" placeholder="+5 or -2" value={delta} onChange={(e) => setDelta(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label>Note</Label>
              <Input placeholder="reason…" value={note} onChange={(e) => setNote(e.target.value)} />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
            <Button onClick={async () => {
              await apiPost(`/admin/users/${userID}/balance`, { delta_usd: parseFloat(delta), note });
              toast.success("Balance updated");
              onDone();
              setOpen(false);
              setDelta(""); setNote("");
            }}>Apply</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function GroupsTab() {
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
          <CardTitle>Pricing groups</CardTitle>
          <CreateGroupButton onDone={reload} />
        </div>
      </CardHeader>
      <CardContent className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead className="text-right">Claude ×</TableHead>
              <TableHead className="text-right">Codex ×</TableHead>
              <TableHead>Cred. group</TableHead>
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
                      if (!confirm(`Delete group "${g.Name}"? Users will be moved to default.`)) return;
                      await apiDelete(`/admin/groups/${g.ID}`);
                      toast.success("Deleted");
                      reload();
                    }}>Delete</Button>
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
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  // Multiplier defaults match the migration v3 seed: claude=0.3, codex=0.05.
  // Final user-facing charge is `official_USD × multiplier`.
  const [claudeMult, setClaudeMult] = useState("0.3");
  const [codexMult, setCodexMult] = useState("0.05");
  const [credGroup, setCredGroup] = useState("");
  return (
    <>
      <Button onClick={() => setOpen(true)}>+ New group</Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>New pricing group</DialogTitle></DialogHeader>
          <p className="text-xs text-muted-foreground">
            Final charge per request = upstream USD × multiplier. Lower = cheaper for the user.
          </p>
          <div className="grid gap-3 py-2">
            <div className="space-y-2"><Label>Name</Label><Input value={name} onChange={(e) => setName(e.target.value)} /></div>
            <div className="space-y-2"><Label>Description</Label><Input value={desc} onChange={(e) => setDesc(e.target.value)} /></div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2"><Label>Claude multiplier</Label><Input value={claudeMult} onChange={(e) => setClaudeMult(e.target.value)} /></div>
              <div className="space-y-2"><Label>Codex multiplier</Label><Input value={codexMult} onChange={(e) => setCodexMult(e.target.value)} /></div>
            </div>
            <div className="space-y-2"><Label>Credential group (auth.Pool filter)</Label><Input value={credGroup} onChange={(e) => setCredGroup(e.target.value)} placeholder="empty = public" /></div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
            <Button onClick={async () => {
              await apiPost("/admin/groups", {
                name, description: desc,
                claude_multiplier: parseFloat(claudeMult),
                codex_multiplier: parseFloat(codexMult),
                credential_group: credGroup,
              });
              toast.success("Group created");
              onDone();
              setOpen(false);
              setName(""); setDesc("");
            }}>Create</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function CredentialsTab() {
  const [creds, setCreds] = useState<any[]>([]);
  const [openKey, setOpenKey] = useState(false);
  const [openOAuth, setOpenOAuth] = useState<null | "anthropic" | "openai">(null);
  const [usageFor, setUsageFor] = useState<{ id: string; label: string } | null>(null);
  const [providerTab, setProviderTab] = useState<"anthropic" | "openai">("anthropic");
  const reload = async () => {
    const r = await apiGet<any>("/admin/credentials");
    setCreds(r.credentials || []);
  };
  useEffect(() => { reload(); }, []);

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
              {p === "anthropic" ? "Claude (Anthropic)" : "Codex (OpenAI)"}
              <span className="rounded-full bg-muted px-1.5 py-0.5 text-xs font-mono">{count}</span>
            </button>
          );
        })}
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-start justify-between gap-4">
            <div>
              <CardTitle>{providerTab === "anthropic" ? "Claude credentials" : "Codex credentials"}</CardTitle>
              <CardDescription>OAuth + API-key credentials in the live pool.</CardDescription>
            </div>
            <div className="flex gap-2 flex-wrap justify-end">
              <Button onClick={() => setOpenKey(true)}>+ Add API key</Button>
              <Button variant="outline" onClick={() => setOpenOAuth("anthropic")}>+ OAuth (Claude)</Button>
              <Button variant="outline" onClick={() => setOpenOAuth("openai")}>+ OAuth (Codex)</Button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {visible.length === 0 ? (
            <div className="p-12 text-center text-sm text-muted-foreground">No credentials loaded.</div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Label</TableHead>
                  <TableHead>Kind</TableHead>
                  <TableHead>Group</TableHead>
                  <TableHead className="text-right">Active</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead>Health</TableHead>
                  <TableHead></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((c) => (
                  <TableRow key={c.id} className={c.disabled ? "opacity-50" : ""}>
                    <TableCell>
                      <div className="font-medium">{c.label}</div>
                      <code className="text-xs text-muted-foreground">{c.id}</code>
                    </TableCell>
                    <TableCell><span className="rounded border border-border bg-muted px-2 py-0.5 text-xs font-mono uppercase">{c.kind}</span></TableCell>
                    <TableCell className="font-mono text-xs">{c.group || "—"}</TableCell>
                    <TableCell className="font-mono tabular-nums text-right">{c.active_clients}</TableCell>
                    <TableCell className="font-mono text-xs"><Expiry at={c.expires_at} /></TableCell>
                    <TableCell><HealthPill cred={c} /></TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        {c.kind === "oauth" && c.provider === "anthropic" && (
                          <Button
                            size="sm"
                            variant="ghost"
                            title="Probe upstream Anthropic usage windows"
                            onClick={() => setUsageFor({ id: c.id, label: c.label })}
                          >
                            <Gauge className="size-3.5" />
                          </Button>
                        )}
                        <Button size="sm" variant="ghost" className="text-destructive" onClick={async () => {
                          if (!confirm(`Remove credential "${c.label}"?`)) return;
                          await apiDelete(`/admin/credentials/${encodeURIComponent(c.id)}`);
                          toast.success("Removed");
                          reload();
                        }}>Remove</Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
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
        onClose={() => setUsageFor(null)}
      />
    </div>
  );
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
  if (cred.disabled) return <span className="rounded border border-muted-foreground/30 bg-muted/40 px-2 py-0.5 text-xs font-mono uppercase text-muted-foreground">disabled</span>;
  if (cred.hard_failure) return <span className="rounded border border-destructive/30 bg-destructive/15 px-2 py-0.5 text-xs font-mono uppercase text-destructive" title={cred.failure_reason}>hard fail</span>;
  if (cred.quota_exceeded) return <span className="rounded border border-warning/30 bg-warning/15 px-2 py-0.5 text-xs font-mono uppercase text-warning" title={cred.quota_reset_at ? `Resets ${new Date(cred.quota_reset_at).toLocaleString()}` : ""}>quota</span>;
  if (cred.healthy) return <span className="rounded border border-success/30 bg-success/15 px-2 py-0.5 text-xs font-mono uppercase text-success" title={cred.failure_reason || ""}>ok</span>;
  return <span className="rounded border border-warning/30 bg-warning/15 px-2 py-0.5 text-xs font-mono uppercase text-warning" title={cred.failure_reason || ""}>cooldown</span>;
}

function Expiry({ at }: { at?: string }) {
  if (!at || at.startsWith("0001-")) return <span className="text-muted-foreground">—</span>;
  const d = new Date(at);
  const now = Date.now();
  const dt = d.getTime() - now;
  const days = Math.round(dt / 86400000);
  const cls =
    dt < 0
      ? "text-destructive"
      : days < 7
      ? "text-warning"
      : "text-muted-foreground";
  const rel = dt < 0
    ? `expired ${-days}d ago`
    : days < 1
    ? `${Math.round(dt / 3600000)}h`
    : `${days}d`;
  return <span className={cls} title={d.toLocaleString()}>{rel}</span>;
}

function AddAPIKeyDialog({ open, onOpenChange, onCreated }: any) {
  const [provider, setProvider] = useState("anthropic");
  const [key, setKey] = useState("");
  const [label, setLabel] = useState("");
  const [base, setBase] = useState("");
  const [proxy, setProxy] = useState("");
  const [group, setGroup] = useState("");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader><DialogTitle>Add API key</DialogTitle></DialogHeader>
        <div className="grid gap-3 py-2">
          <div className="space-y-2">
            <Label>Provider</Label>
            <select className="h-10 w-full rounded-md border border-border bg-card px-3 text-sm" value={provider} onChange={(e) => setProvider(e.target.value)}>
              <option value="anthropic">Anthropic</option>
              <option value="openai">OpenAI</option>
            </select>
          </div>
          <div className="space-y-2"><Label>API key</Label><Input type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder="sk-..." className="font-mono" /></div>
          <div className="space-y-2"><Label>Label</Label><Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="primary" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2"><Label>Base URL (optional)</Label><Input value={base} onChange={(e) => setBase(e.target.value)} placeholder="https://api.anthropic.com" /></div>
            <div className="space-y-2"><Label>Proxy URL (optional)</Label><Input value={proxy} onChange={(e) => setProxy(e.target.value)} /></div>
          </div>
          <div className="space-y-2"><Label>Group (optional)</Label><Input value={group} onChange={(e) => setGroup(e.target.value)} placeholder="empty = public" /></div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button onClick={async () => {
            if (!key) { toast.error("API key required"); return; }
            try {
              await apiPost("/admin/credentials/apikey", { provider, key, label, base_url: base, proxy_url: proxy, group });
              toast.success("API key added");
              onCreated();
              onOpenChange(false);
              setKey(""); setLabel(""); setBase(""); setProxy(""); setGroup("");
            } catch (e: any) { toast.error(e.message); }
          }}>Add</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PaymentsTab() {
  const [orders, setOrders] = useState<any[]>([]);
  useEffect(() => {
    apiGet<{ orders: any[] }>("/admin/orders").then((r) => setOrders(r.orders || []));
  }, []);
  return (
    <Card>
      <CardHeader>
        <CardTitle>All payments ({orders.length})</CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Order</TableHead>
              <TableHead>User</TableHead>
              <TableHead className="text-right">USD</TableHead>
              <TableHead className="text-right">CNY</TableHead>
              <TableHead className="text-right">Rate</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Created</TableHead>
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
                <TableCell><span className={`rounded border px-2 py-0.5 text-xs font-mono uppercase ${o.Status === "paid" ? "border-success/30 bg-success/15 text-success" : "border-warning/30 bg-warning/15 text-warning"}`}>{o.Status}</span></TableCell>
                <TableCell className="text-muted-foreground">{o.CreatedAt && new Date(o.CreatedAt).toLocaleString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}
