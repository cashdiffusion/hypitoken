import { Copy, KeyRound, Mail, Trash2, Users, Wallet } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Navigate } from "react-router-dom";
import { toast } from "sonner";
import { GlassPanel, PageHeader, StatTile } from "@/components/app/page-primitives";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
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
import { useAuth } from "@/hooks/use-auth";
import { apiDelete, apiGet, apiPatch, apiPost } from "@/lib/api";
import type { UserWorkspace } from "@/lib/types";
import { errMsg, fmtUSD } from "@/lib/utils";

interface MemberUsage {
  user_id: number;
  email: string;
  role: string;
  monthly_usd_cap: number;
  spent_usd: number;
}
interface Usage {
  balance_usd: number;
  monthly_usd_cap: number;
  spent_month_usd: number;
  members: MemberUsage[];
}
interface Invite {
  id: number;
  email: string;
  role: string;
  status: string;
  expires_at: number;
  link: string;
}

// /app/workspace — the space-admin team console. Visible only to users who are
// admins of an enterprise workspace. Shows ONLY this space's member usage; never
// upstream credentials, fleet, or other workspaces (enforced server-side too).
export default function WorkspacePage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const adminSpaces = useMemo(
    () => (user?.workspaces || []).filter((w) => w.type === "enterprise" && w.role === "admin"),
    [user],
  );
  const [activeId, setActiveId] = useState<number | null>(null);
  const active = activeId ?? adminSpaces[0]?.id ?? null;

  if (!user) return null;
  if (adminSpaces.length === 0) return <Navigate to="/app" replace />;

  return (
    <div className="space-y-6">
      <PageHeader icon={Users} title={t("workspace.title")} sub={t("workspace.sub")} />
      {adminSpaces.length > 1 && (
        <div className="flex gap-2">
          {adminSpaces.map((w: UserWorkspace) => (
            <Button
              key={w.id}
              size="sm"
              variant={active === w.id ? "default" : "outline"}
              onClick={() => setActiveId(w.id)}
            >
              {w.name}
            </Button>
          ))}
        </div>
      )}
      {active && <WorkspaceConsole key={active} id={active} />}
    </div>
  );
}

function WorkspaceConsole({ id }: { id: number }) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [usage, setUsage] = useState<Usage | null>(null);
  const [invites, setInvites] = useState<Invite[]>([]);
  const [inviteEmail, setInviteEmail] = useState("");
  const [inviteRole, setInviteRole] = useState("member");
  const [keysFor, setKeysFor] = useState<MemberUsage | null>(null);

  const loadUsage = () =>
    apiGet<Usage>(`/workspaces/${id}/usage`)
      .then(setUsage)
      .catch((e) => toast.error(errMsg(e)));
  const loadInvites = () =>
    apiGet<{ invites: Invite[] }>(`/workspaces/${id}/invites`)
      .then((r) => setInvites(r.invites || []))
      .catch((e) => toast.error(errMsg(e)));
  // biome-ignore lint/correctness/useExhaustiveDependencies: loaders are stable closures; re-fetch only when the workspace id changes
  useEffect(() => {
    loadUsage();
    loadInvites();
  }, [id]);

  const invite = async () => {
    if (!inviteEmail.trim()) return;
    try {
      await apiPost(`/workspaces/${id}/invites`, { email: inviteEmail.trim(), role: inviteRole });
      setInviteEmail("");
      toast.success(t("workspace.inviteSent"));
      loadInvites();
    } catch (e) {
      toast.error(errMsg(e));
    }
  };
  const revoke = async (iv: Invite) => {
    await apiDelete(`/workspaces/${id}/invites/${iv.id}`).catch((e) => toast.error(errMsg(e)));
    loadInvites();
  };
  const resend = async (iv: Invite) => {
    await apiPost(`/workspaces/${id}/invites/${iv.id}/resend`, {}).catch((e) =>
      toast.error(errMsg(e)),
    );
    toast.success(t("workspace.inviteResent"));
    loadInvites();
  };
  const setMemberCap = async (m: MemberUsage, cap: number) => {
    await apiPatch(`/workspaces/${id}/members/${m.user_id}`, { monthly_usd_cap: cap }).catch((e) =>
      toast.error(errMsg(e)),
    );
    loadUsage();
  };
  const removeMember = async (m: MemberUsage) => {
    if (!(await confirm({ title: t("workspace.removeMember"), description: m.email }))) return;
    await apiDelete(`/workspaces/${id}/members/${m.user_id}`).catch((e) => toast.error(errMsg(e)));
    loadUsage();
  };

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatTile
          label={t("workspace.balance")}
          value={fmtUSD(usage?.balance_usd ?? 0)}
          icon={Wallet}
        />
        <StatTile
          label={t("workspace.spentMonth")}
          value={fmtUSD(usage?.spent_month_usd ?? 0)}
          icon={Wallet}
        />
        <StatTile
          label={t("workspace.monthlyCap")}
          value={usage && usage.monthly_usd_cap > 0 ? fmtUSD(usage.monthly_usd_cap) : "—"}
          icon={Wallet}
        />
      </div>

      {/* Invite */}
      <GlassPanel className="space-y-3 p-5">
        <h3 className="flex items-center gap-2 font-semibold">
          <Mail className="h-4 w-4" />
          {t("workspace.invite")}
        </h3>
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-56 flex-1">
            <Label>{t("workspace.inviteEmail")}</Label>
            <Input
              type="email"
              placeholder="name@company.com"
              value={inviteEmail}
              onChange={(e) => setInviteEmail(e.target.value)}
            />
          </div>
          <select
            className="h-9 rounded-md border bg-background px-2 text-sm"
            value={inviteRole}
            onChange={(e) => setInviteRole(e.target.value)}
          >
            <option value="member">{t("workspace.roleMember")}</option>
            <option value="admin">{t("workspace.roleAdmin")}</option>
          </select>
          <Button onClick={invite}>{t("workspace.sendInvite")}</Button>
        </div>
        {invites.length > 0 && (
          <div className="space-y-1 pt-2">
            {invites.map((iv) => (
              <div
                key={iv.id}
                className="flex items-center justify-between rounded-md border px-3 py-2 text-sm"
              >
                <span>
                  {iv.email} <span className="text-xs text-muted-foreground">· {iv.status}</span>
                </span>
                <div className="flex items-center gap-1">
                  <Button
                    size="icon"
                    variant="ghost"
                    title={t("workspace.copyLink")}
                    onClick={() => {
                      navigator.clipboard.writeText(iv.link);
                      toast.success(t("workspace.linkCopied"));
                    }}
                  >
                    <Copy className="h-3.5 w-3.5" />
                  </Button>
                  {iv.status === "pending" && (
                    <>
                      <Button size="sm" variant="ghost" onClick={() => resend(iv)}>
                        {t("workspace.resend")}
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => revoke(iv)}>
                        {t("workspace.revoke")}
                      </Button>
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </GlassPanel>

      {/* Members */}
      <GlassPanel className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("workspace.member")}</TableHead>
              <TableHead>{t("workspace.role")}</TableHead>
              <TableHead>{t("workspace.spentMonth")}</TableHead>
              <TableHead>{t("workspace.memberCap")}</TableHead>
              <TableHead className="text-right">{t("common.actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(usage?.members || []).map((m) => (
              <TableRow key={m.user_id}>
                <TableCell className="font-medium">{m.email}</TableCell>
                <TableCell>
                  {m.role === "admin" ? t("workspace.roleAdmin") : t("workspace.roleMember")}
                </TableCell>
                <TableCell>{fmtUSD(m.spent_usd)}</TableCell>
                <TableCell>
                  <CapEditor value={m.monthly_usd_cap} onSave={(v) => setMemberCap(m, v)} />
                </TableCell>
                <TableCell className="space-x-2 text-right">
                  <Button size="sm" variant="outline" onClick={() => setKeysFor(m)}>
                    <KeyRound className="mr-1 h-3.5 w-3.5" />
                    {t("workspace.keys")}
                  </Button>
                  {m.role !== "admin" && (
                    <Button size="icon" variant="ghost" onClick={() => removeMember(m)}>
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </GlassPanel>

      {keysFor && <MemberKeysDialog wsId={id} member={keysFor} onClose={() => setKeysFor(null)} />}
    </div>
  );
}

// CapEditor — inline number field with a save-on-blur/enter, showing "—" for 0.
function CapEditor({ value, onSave }: { value: number; onSave: (v: number) => void }) {
  const [v, setV] = useState(String(value || ""));
  useEffect(() => setV(String(value || "")), [value]);
  return (
    <Input
      type="number"
      className="h-8 w-24"
      value={v}
      placeholder="—"
      onChange={(e) => setV(e.target.value)}
      onBlur={() => Number(v || 0) !== value && onSave(Number(v) || 0)}
      onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
    />
  );
}

interface MemberToken {
  id: number;
  name: string;
  token_masked: string;
  monthly_usd_cap: number;
  admin_monthly_cap: number;
  disabled: boolean;
}

function MemberKeysDialog({
  wsId,
  member,
  onClose,
}: {
  wsId: number;
  member: MemberUsage;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [tokens, setTokens] = useState<MemberToken[]>([]);
  const load = () =>
    apiGet<{ tokens: MemberToken[] }>(`/workspaces/${wsId}/members/${member.user_id}/tokens`)
      .then((r) => setTokens(r.tokens || []))
      .catch((e) => toast.error(errMsg(e)));
  // biome-ignore lint/correctness/useExhaustiveDependencies: load is a stable closure; re-fetch on (workspace, member) change
  useEffect(() => {
    load();
  }, [wsId, member.user_id]);

  const setCap = async (tid: number, cap: number) => {
    await apiPatch(`/workspaces/${wsId}/members/${member.user_id}/tokens/${tid}`, {
      admin_monthly_cap: cap,
    }).catch((e) => toast.error(errMsg(e)));
    load();
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {t("workspace.keys")} — {member.email}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-2">
          {tokens.map((tk) => (
            <div
              key={tk.id}
              className="flex items-center justify-between rounded-md border px-3 py-2 text-sm"
            >
              <div>
                <div className="font-medium">{tk.name || t("workspace.unnamedKey")}</div>
                <div className="font-mono text-xs text-muted-foreground">{tk.token_masked}</div>
              </div>
              <div className="flex items-center gap-2">
                <Label className="text-xs text-muted-foreground">{t("workspace.keyCap")}</Label>
                <CapEditor value={tk.admin_monthly_cap} onSave={(v) => setCap(tk.id, v)} />
              </div>
            </div>
          ))}
          {tokens.length === 0 && (
            <p className="py-4 text-center text-sm text-muted-foreground">
              {t("workspace.noKeys")}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
