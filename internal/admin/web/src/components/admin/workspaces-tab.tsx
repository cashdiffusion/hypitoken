import { Building2, Plus, Users, Wallet } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { GlassPanel } from "@/components/app/page-primitives";
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
import { apiDelete, apiGet, apiPost } from "@/lib/api";
import { errMsg, fmtUSD } from "@/lib/utils";

interface AdminWorkspace {
  id: number;
  name: string;
  type: string;
  balance_usd: number;
  daily_usd_cap: number;
  monthly_usd_cap: number;
  member_count: number;
  created_at: number;
}

interface Member {
  user_id: number;
  email: string;
  role: string;
  monthly_usd_cap: number;
}

export function WorkspacesTab() {
  const { t } = useTranslation();
  const [list, setList] = useState<AdminWorkspace[]>([]);
  const [creating, setCreating] = useState(false);
  const [membersOf, setMembersOf] = useState<AdminWorkspace | null>(null);
  const [adjustOf, setAdjustOf] = useState<AdminWorkspace | null>(null);

  const load = () => {
    apiGet<{ workspaces: AdminWorkspace[] }>("/admin/workspaces?type=enterprise&limit=200")
      .then((r) => setList(r.workspaces || []))
      .catch((e) => toast.error(errMsg(e)));
  };
  useEffect(load, []);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="flex items-center gap-2 text-lg font-semibold">
          <Building2 className="h-5 w-5" />
          {t("admin.workspaces.title")}
        </h2>
        <Button size="sm" onClick={() => setCreating(true)}>
          <Plus className="mr-1.5 h-4 w-4" />
          {t("admin.workspaces.create")}
        </Button>
      </div>

      <GlassPanel className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("admin.workspaces.name")}</TableHead>
              <TableHead>{t("admin.workspaces.balance")}</TableHead>
              <TableHead>{t("admin.workspaces.monthlyCap")}</TableHead>
              <TableHead>{t("admin.workspaces.members")}</TableHead>
              <TableHead className="text-right">{t("common.actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.map((w) => (
              <TableRow key={w.id}>
                <TableCell className="font-medium">{w.name}</TableCell>
                <TableCell>{fmtUSD(w.balance_usd)}</TableCell>
                <TableCell>{w.monthly_usd_cap > 0 ? fmtUSD(w.monthly_usd_cap) : "—"}</TableCell>
                <TableCell>{w.member_count}</TableCell>
                <TableCell className="space-x-2 text-right">
                  <Button size="sm" variant="outline" onClick={() => setAdjustOf(w)}>
                    <Wallet className="mr-1 h-3.5 w-3.5" />
                    {t("admin.workspaces.adjust")}
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => setMembersOf(w)}>
                    <Users className="mr-1 h-3.5 w-3.5" />
                    {t("admin.workspaces.manageMembers")}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {list.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                  {t("admin.workspaces.empty")}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </GlassPanel>

      {creating && (
        <CreateDialog
          onClose={() => setCreating(false)}
          onDone={() => {
            setCreating(false);
            load();
          }}
        />
      )}
      {adjustOf && (
        <AdjustDialog
          ws={adjustOf}
          onClose={() => setAdjustOf(null)}
          onDone={() => {
            setAdjustOf(null);
            load();
          }}
        />
      )}
      {membersOf && (
        <MembersDialog ws={membersOf} onClose={() => setMembersOf(null)} onChange={load} />
      )}
    </div>
  );
}

function CreateDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [balance, setBalance] = useState("");
  const [monthlyCap, setMonthlyCap] = useState("");
  const [adminEmail, setAdminEmail] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    if (!name.trim()) return;
    setBusy(true);
    try {
      await apiPost("/admin/workspaces", {
        name: name.trim(),
        balance_usd: Number(balance) || 0,
        monthly_usd_cap: Number(monthlyCap) || 0,
        admin_email: adminEmail.trim(),
      });
      toast.success(t("admin.workspaces.created"));
      onDone();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("admin.workspaces.create")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div>
            <Label>{t("admin.workspaces.name")}</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label>{t("admin.workspaces.initialBalance")}</Label>
              <Input type="number" value={balance} onChange={(e) => setBalance(e.target.value)} />
            </div>
            <div>
              <Label>{t("admin.workspaces.monthlyCap")}</Label>
              <Input
                type="number"
                value={monthlyCap}
                onChange={(e) => setMonthlyCap(e.target.value)}
              />
            </div>
          </div>
          <div>
            <Label>{t("admin.workspaces.adminEmail")}</Label>
            <Input
              type="email"
              placeholder={t("admin.workspaces.adminEmailHint")}
              value={adminEmail}
              onChange={(e) => setAdminEmail(e.target.value)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} disabled={busy || !name.trim()}>
            {t("common.create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function AdjustDialog({
  ws,
  onClose,
  onDone,
}: {
  ws: AdminWorkspace;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const [delta, setDelta] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    setBusy(true);
    try {
      await apiPost(`/admin/workspaces/${ws.id}/balance`, { delta_usd: Number(delta) || 0, note });
      toast.success(t("admin.workspaces.balanceUpdated"));
      onDone();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {t("admin.workspaces.adjust")} — {ws.name}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="text-sm text-muted-foreground">
            {t("admin.workspaces.balance")}: {fmtUSD(ws.balance_usd)}
          </div>
          <div>
            <Label>{t("admin.workspaces.delta")}</Label>
            <Input type="number" value={delta} onChange={(e) => setDelta(e.target.value)} />
          </div>
          <div>
            <Label>{t("admin.workspaces.note")}</Label>
            <Input value={note} onChange={(e) => setNote(e.target.value)} />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} disabled={busy}>
            {t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function MembersDialog({
  ws,
  onClose,
  onChange,
}: {
  ws: AdminWorkspace;
  onClose: () => void;
  onChange: () => void;
}) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [members, setMembers] = useState<Member[]>([]);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("admin");

  const load = () => {
    apiGet<{ members: Member[] }>(`/admin/workspaces/${ws.id}/members`)
      .then((r) => setMembers(r.members || []))
      .catch((e) => toast.error(errMsg(e)));
  };
  useEffect(load, [ws.id]);

  const add = async () => {
    if (!email.trim()) return;
    try {
      await apiPost(`/admin/workspaces/${ws.id}/members`, { email: email.trim(), role });
      setEmail("");
      load();
      onChange();
    } catch (e) {
      toast.error(errMsg(e));
    }
  };
  const remove = async (m: Member) => {
    if (!(await confirm({ title: t("admin.workspaces.removeMember"), description: m.email }))) return;
    try {
      await apiDelete(`/admin/workspaces/${ws.id}/members/${m.user_id}`);
      load();
      onChange();
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {t("admin.workspaces.manageMembers")} — {ws.name}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="flex items-end gap-2">
            <div className="flex-1">
              <Label>{t("admin.workspaces.assignEmail")}</Label>
              <Input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="name@company.com"
              />
            </div>
            <select
              className="h-9 rounded-md border bg-background px-2 text-sm"
              value={role}
              onChange={(e) => setRole(e.target.value)}
            >
              <option value="admin">{t("workspace.roleAdmin")}</option>
              <option value="member">{t("workspace.roleMember")}</option>
            </select>
            <Button size="sm" onClick={add}>
              {t("common.add")}
            </Button>
          </div>
          <div className="max-h-64 space-y-1 overflow-y-auto">
            {members.map((m) => (
              <div
                key={m.user_id}
                className="flex items-center justify-between rounded-md border px-3 py-2 text-sm"
              >
                <span>
                  {m.email}{" "}
                  <span className="text-xs text-muted-foreground">
                    ({m.role === "admin" ? t("workspace.roleAdmin") : t("workspace.roleMember")})
                  </span>
                </span>
                <Button size="sm" variant="ghost" onClick={() => remove(m)}>
                  {t("common.remove")}
                </Button>
              </div>
            ))}
            {members.length === 0 && (
              <p className="py-4 text-center text-sm text-muted-foreground">
                {t("admin.workspaces.noMembers")}
              </p>
            )}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("common.close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
