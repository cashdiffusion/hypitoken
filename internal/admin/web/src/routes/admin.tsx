import { useEffect, useState } from "react";
import { NavLink, Outlet, Routes, Route } from "react-router-dom";
import { Users, Tag, KeyRound, ShoppingCart } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { apiDelete, apiGet, apiPatch, apiPost } from "@/lib/api";
import { fmtUSD } from "@/lib/utils";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import type { PricingGroup } from "@/lib/types";

const TABS = [
  { to: "users", label: "Users", icon: Users },
  { to: "groups", label: "Pricing groups", icon: Tag },
  { to: "credentials", label: "Credentials", icon: KeyRound },
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
        <Route index element={<UsersTab />} />
        <Route path="users" element={<UsersTab />} />
        <Route path="groups" element={<GroupsTab />} />
        <Route path="credentials" element={<CredentialsTab />} />
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
              <TableHead className="text-right">Codex peg</TableHead>
              <TableHead className="text-right">Codex ×</TableHead>
              <TableHead className="text-right">Claude peg</TableHead>
              <TableHead className="text-right">Claude ×</TableHead>
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
                <TableCell className="font-mono tabular-nums text-right">¥{g.CodexRMBPerUSD.toFixed(2)}</TableCell>
                <TableCell className="font-mono tabular-nums text-right">{g.CodexMultiplier.toFixed(2)}</TableCell>
                <TableCell className="font-mono tabular-nums text-right">¥{g.ClaudeRMBPerUSD.toFixed(2)}</TableCell>
                <TableCell className="font-mono tabular-nums text-right">{g.ClaudeMultiplier.toFixed(2)}</TableCell>
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
  const [codexPeg, setCodexPeg] = useState("0.5");
  const [codexMult, setCodexMult] = useState("1.0");
  const [claudePeg, setClaudePeg] = useState("2.0");
  const [claudeMult, setClaudeMult] = useState("1.0");
  const [credGroup, setCredGroup] = useState("");
  return (
    <>
      <Button onClick={() => setOpen(true)}>+ New group</Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>New pricing group</DialogTitle></DialogHeader>
          <div className="grid gap-3 py-2">
            <div className="space-y-2"><Label>Name</Label><Input value={name} onChange={(e) => setName(e.target.value)} /></div>
            <div className="space-y-2"><Label>Description</Label><Input value={desc} onChange={(e) => setDesc(e.target.value)} /></div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2"><Label>Codex ¥/USD</Label><Input value={codexPeg} onChange={(e) => setCodexPeg(e.target.value)} /></div>
              <div className="space-y-2"><Label>Codex multiplier</Label><Input value={codexMult} onChange={(e) => setCodexMult(e.target.value)} /></div>
              <div className="space-y-2"><Label>Claude ¥/USD</Label><Input value={claudePeg} onChange={(e) => setClaudePeg(e.target.value)} /></div>
              <div className="space-y-2"><Label>Claude multiplier</Label><Input value={claudeMult} onChange={(e) => setClaudeMult(e.target.value)} /></div>
            </div>
            <div className="space-y-2"><Label>Credential group (auth.Pool filter)</Label><Input value={credGroup} onChange={(e) => setCredGroup(e.target.value)} placeholder="empty = public" /></div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
            <Button onClick={async () => {
              await apiPost("/admin/groups", {
                name, description: desc,
                codex_rmb_per_usd: parseFloat(codexPeg),
                codex_multiplier: parseFloat(codexMult),
                claude_rmb_per_usd: parseFloat(claudePeg),
                claude_multiplier: parseFloat(claudeMult),
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
  const [open, setOpen] = useState(false);
  const reload = async () => {
    const r = await apiGet<any>("/admin/credentials");
    setCreds(r.credentials || []);
  };
  useEffect(() => { reload(); }, []);

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <div className="flex items-start justify-between gap-4">
            <div>
              <CardTitle>Upstream credentials</CardTitle>
              <CardDescription>OAuth + API-key credentials in the live pool.</CardDescription>
            </div>
            <div className="flex gap-2">
              <Button onClick={() => setOpen(true)}>+ Add API key</Button>
              <Button asChild variant="outline">
                <a href="/mgmt-console/" target="_blank" rel="noreferrer">OAuth uploads ↗</a>
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {creds.length === 0 ? (
            <div className="p-12 text-center text-sm text-muted-foreground">No credentials loaded.</div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Label</TableHead>
                  <TableHead>Kind</TableHead>
                  <TableHead>Provider</TableHead>
                  <TableHead>Group</TableHead>
                  <TableHead className="text-right">Active</TableHead>
                  <TableHead>Health</TableHead>
                  <TableHead></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {creds.map((c) => (
                  <TableRow key={c.id} className={c.disabled ? "opacity-50" : ""}>
                    <TableCell>
                      <div className="font-medium">{c.label}</div>
                      <code className="text-xs text-muted-foreground">{c.id}</code>
                    </TableCell>
                    <TableCell><span className="rounded border border-border bg-muted px-2 py-0.5 text-xs font-mono uppercase">{c.kind}</span></TableCell>
                    <TableCell className="capitalize">{c.provider}</TableCell>
                    <TableCell className="font-mono text-xs">{c.group || "—"}</TableCell>
                    <TableCell className="font-mono tabular-nums text-right">{c.active_clients}</TableCell>
                    <TableCell><HealthPill cred={c} /></TableCell>
                    <TableCell className="text-right">
                      <Button size="sm" variant="ghost" className="text-destructive" onClick={async () => {
                        if (!confirm(`Remove credential "${c.label}"?`)) return;
                        await apiDelete(`/admin/credentials/${encodeURIComponent(c.id)}`);
                        toast.success("Removed");
                        reload();
                      }}>Remove</Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <AddAPIKeyDialog open={open} onOpenChange={setOpen} onCreated={reload} />
    </div>
  );
}

function HealthPill({ cred }: { cred: any }) {
  if (cred.disabled) return <span className="rounded border border-muted-foreground/30 bg-muted/40 px-2 py-0.5 text-xs font-mono uppercase text-muted-foreground">disabled</span>;
  if (cred.hard_failure) return <span className="rounded border border-destructive/30 bg-destructive/15 px-2 py-0.5 text-xs font-mono uppercase text-destructive" title={cred.failure_reason}>hard fail</span>;
  if (cred.quota_exceeded) return <span className="rounded border border-warning/30 bg-warning/15 px-2 py-0.5 text-xs font-mono uppercase text-warning">quota</span>;
  if (cred.healthy) return <span className="rounded border border-success/30 bg-success/15 px-2 py-0.5 text-xs font-mono uppercase text-success">ok</span>;
  return <span className="rounded border border-warning/30 bg-warning/15 px-2 py-0.5 text-xs font-mono uppercase text-warning">cooldown</span>;
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
