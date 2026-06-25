import {
  Check,
  ChevronDown,
  ChevronUp,
  Copy,
  Eye,
  EyeOff,
  Pencil,
  Plus,
  RefreshCw,
  Terminal,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { GlassPanel, PageHeader } from "@/components/app/page-primitives";
import { Reveal } from "@/components/landing/reveal";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm-dialog";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useAuth } from "@/hooks/use-auth";
import { apiDelete, apiGet, apiPatch, apiPost } from "@/lib/api";
import type { Channel, UserToken } from "@/lib/types";
import { copyToClipboard, errMsg, fmtUSD } from "@/lib/utils";

function detectOS(): "Windows" | "macOS" | "Linux" {
  const ua = navigator.userAgent;
  if (/Windows/i.test(ua)) return "Windows";
  if (/Mac OS X/i.test(ua)) return "macOS";
  return "Linux";
}

export default function TokensPage() {
  const { t: tt } = useTranslation();
  const confirm = useConfirm();
  const [tokens, setTokens] = useState<UserToken[]>([]);
  const [open, setOpen] = useState(false);
  const [reveal, setReveal] = useState<Record<number, boolean>>({});
  const [useToken, setUseToken] = useState<UserToken | null>(null);
  const [editToken, setEditToken] = useState<UserToken | null>(null);

  const refresh = async () => {
    const r = await apiGet<{ tokens: UserToken[] }>("/tokens");
    setTokens(r.tokens || []);
  };
  // biome-ignore lint/correctness/useExhaustiveDependencies: mount-only fetch; refresh is a stable inline async fn intentionally not re-run on every render
  useEffect(() => {
    refresh();
  }, []);

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow={tt("nav.tokens")}
        title={tt("tokens.title")}
        sub={tt("tokens.sub")}
        action={
          <Button onClick={() => setOpen(true)} className="gap-2">
            <Plus className="h-4 w-4" /> {tt("tokens.newToken")}
          </Button>
        }
      />

      <Reveal>
        <GlassPanel bodyClassName="p-0">
          {tokens.length === 0 ? (
            <div className="p-12 text-center text-sm text-muted-foreground">
              {tt("tokens.none")}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{tt("common.name")}</TableHead>
                  <TableHead>Token</TableHead>
                  <TableHead>渠道</TableHead>
                  <TableHead className="text-right">{tt("tokens.spendingCap")}</TableHead>
                  <TableHead></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tokens.map((t) => (
                  <TableRow key={t.id} className={t.disabled ? "opacity-50" : ""}>
                    <TableCell className="font-medium">
                      {t.name || <span className="text-muted-foreground">(unnamed)</span>}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <code className="rounded bg-muted px-2 py-0.5 font-mono text-xs">
                          {reveal[t.id] ? t.token : `${t.token.slice(0, 12)}…${t.token.slice(-4)}`}
                        </code>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-7 w-7"
                          onClick={() => setReveal((r) => ({ ...r, [t.id]: !r[t.id] }))}
                        >
                          {reveal[t.id] ? (
                            <EyeOff className="h-3.5 w-3.5" />
                          ) : (
                            <Eye className="h-3.5 w-3.5" />
                          )}
                        </Button>
                        <CopyBtn text={t.token} />
                      </div>
                    </TableCell>
                    <TableCell>
                      {t.groups && t.groups.length > 0 ? (
                        <div className="flex flex-wrap gap-1">
                          {t.groups.map((g, i) => (
                            <span
                              key={g}
                              className="rounded bg-muted px-1.5 py-0.5 text-[11px] font-mono"
                            >
                              {i > 0 && <span className="mr-0.5 text-muted-foreground">→</span>}
                              {g}
                            </span>
                          ))}
                        </div>
                      ) : (
                        <span className="text-xs text-muted-foreground">默认</span>
                      )}
                    </TableCell>
                    <TableCell className="font-mono tabular-nums text-right">
                      {t.monthly_usd_cap > 0 ? fmtUSD(t.monthly_usd_cap) : tt("common.unlimited")}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          size="sm"
                          variant="outline"
                          className="h-8 gap-1.5 text-xs"
                          onClick={() => setUseToken(t)}
                        >
                          <Terminal className="h-3.5 w-3.5" />
                          {tt("tokens.useToken")}
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-8 w-8"
                          title="编辑"
                          onClick={() => setEditToken(t)}
                        >
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-8 w-8"
                          title={tt("tokens.rotate")}
                          onClick={async () => {
                            await apiPost(`/tokens/${t.id}/rotate`);
                            toast.success(tt("tokens.rotated"));
                            refresh();
                          }}
                        >
                          <RefreshCw className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-8 w-8 text-destructive hover:bg-destructive/10"
                          onClick={async () => {
                            if (
                              !(await confirm({
                                title: tt("common.delete"),
                                description: tt("tokens.confirmDelete"),
                                confirmLabel: tt("common.delete"),
                                destructive: true,
                              }))
                            )
                              return;
                            await apiDelete(`/tokens/${t.id}`);
                            toast.success(tt("tokens.deleted"));
                            refresh();
                          }}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </GlassPanel>
      </Reveal>

      <CreateTokenDialog open={open} onOpenChange={setOpen} onCreated={refresh} />
      <EditTokenDialog token={editToken} onClose={() => setEditToken(null)} onSaved={refresh} />
      <UseTokenDialog token={useToken} onClose={() => setUseToken(null)} />
    </div>
  );
}

// ChannelPicker — priority-ordered chips. First chip is preferred upstream;
// when its credentials are exhausted (quota / cooldown / etc.) the pool falls
// through to the next. Options are sourced live from /api/v2/channels so
// users never see a channel that has no usable credentials behind it.
function ChannelPicker({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    apiGet<{ channels: Channel[] }>("/channels")
      .then((r) => setChannels(r.channels || []))
      .finally(() => setLoaded(true));
  }, []);

  const available = useMemo(
    () => channels.filter((c) => !value.includes(c.name)),
    [channels, value],
  );
  const move = (i: number, delta: number) => {
    const j = i + delta;
    if (j < 0 || j >= value.length) return;
    const next = value.slice();
    [next[i], next[j]] = [next[j], next[i]];
    onChange(next);
  };

  return (
    <div className="space-y-2">
      {value.length === 0 ? (
        <p className="text-xs text-muted-foreground">未选择 — 将按账户默认分组路由。</p>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {value.map((g, i) => (
            <span
              key={g}
              className="inline-flex items-center gap-1 rounded-md border border-border bg-muted/40 py-0.5 pl-2 pr-0.5 text-xs"
            >
              <span className="font-mono text-muted-foreground">{i + 1}.</span>
              <span className="font-mono">{g}</span>
              <button
                type="button"
                disabled={i === 0}
                title="上移（提升优先级）"
                className="rounded p-0.5 text-muted-foreground hover:bg-background hover:text-foreground disabled:cursor-not-allowed disabled:opacity-30"
                onClick={() => move(i, -1)}
              >
                <ChevronUp className="h-3 w-3" />
              </button>
              <button
                type="button"
                disabled={i === value.length - 1}
                title="下移（降低优先级）"
                className="rounded p-0.5 text-muted-foreground hover:bg-background hover:text-foreground disabled:cursor-not-allowed disabled:opacity-30"
                onClick={() => move(i, 1)}
              >
                <ChevronDown className="h-3 w-3" />
              </button>
              <button
                type="button"
                title="移除"
                className="rounded p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                onClick={() => onChange(value.filter((x) => x !== g))}
              >
                <X className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      )}
      {loaded && available.length > 0 && (
        <Select value="" onValueChange={(v) => v && onChange([...value, v])}>
          <SelectTrigger className="h-8 w-full text-xs">
            <SelectValue placeholder="+ 添加渠道" />
          </SelectTrigger>
          <SelectContent>
            {available.map((c) => (
              <SelectItem key={c.name} value={c.name}>
                <span className="font-mono text-xs">{c.name}</span>
                <span className="ml-2 text-[10px] text-muted-foreground">
                  {c.providers.join(", ")} · {c.count}
                </span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
      <p className="text-[11px] text-muted-foreground">
        从左到右按优先级使用；前面的渠道不可用（配额耗尽 / 凭证全部冷却）时自动 fallthrough
        到后面的。
      </p>
    </div>
  );
}

function EditTokenDialog({
  token,
  onClose,
  onSaved,
}: {
  token: UserToken | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [cap, setCap] = useState("");
  const [groups, setGroups] = useState<string[]>([]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: intentionally keyed on token.id only — re-syncs form when a different token is opened, not on every upstream property update
  useEffect(() => {
    if (!token) return;
    setName(token.name || "");
    setCap(token.monthly_usd_cap > 0 ? String(token.monthly_usd_cap) : "");
    setGroups(token.groups ? token.groups.slice() : []);
  }, [token?.id]);

  if (!token) return null;

  const submit = async () => {
    try {
      await apiPatch(`/tokens/${token.id}`, {
        name,
        monthly_usd_cap: parseFloat(cap) || 0,
        // Preserve existing per-token caps the dialog doesn't expose.
        daily_usd_cap: token.daily_usd_cap,
        max_concurrent: token.max_concurrent,
        rpm: token.rpm,
        groups,
      });
      toast.success("已更新");
      onSaved();
      onClose();
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  return (
    <Dialog open={!!token} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>编辑令牌</DialogTitle>
          <DialogDescription>
            修改名称、月消费上限和渠道优先级。Token 本身不会变；如需更换 Token 请用「轮换」。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="space-y-2">
            <Label htmlFor="edit-name">{t("common.name")}</Label>
            <Input id="edit-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-cap">{t("tokens.dialog.capLabel")}</Label>
            <Input
              id="edit-cap"
              type="number"
              step="0.01"
              value={cap}
              onChange={(e) => setCap(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label>渠道（按优先级排列）</Label>
            <ChannelPicker value={groups} onChange={setGroups} />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit}>保存</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CopyBtn({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button
      size="icon"
      variant="ghost"
      className="h-7 w-7"
      onClick={async () => {
        await copyToClipboard(text);
        setDone(true);
        setTimeout(() => setDone(false), 1500);
      }}
    >
      {done ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
    </Button>
  );
}

function CodeBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  // min-w-0 + w-full lets this shrink inside CSS grid/flex parents instead of
  // sizing to its widest line and pushing the parent dialog past max-width.
  return (
    <div className="relative mt-3 w-full min-w-0 rounded-lg border border-border bg-[#0d1117]">
      <button
        type="button"
        onClick={async () => {
          await copyToClipboard(code);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
        className="absolute right-2 top-2 z-10 inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs text-zinc-400 transition-colors hover:bg-white/10 hover:text-white"
      >
        {copied ? <Check className="h-3 w-3 text-green-400" /> : <Copy className="h-3 w-3" />}
        {copied ? "Copied" : "Copy"}
      </button>
      <pre className="overflow-x-auto px-4 py-4 pr-16 font-mono text-xs leading-relaxed text-zinc-200">
        {code}
      </pre>
    </div>
  );
}

function UseTokenDialog({ token, onClose }: { token: UserToken | null; onClose: () => void }) {
  const { t } = useTranslation();
  const defaultOS = detectOS();
  const [os, setOS] = useState<"macOS" | "Windows" | "Linux">(defaultOS);

  const tk = token?.token ?? "";

  const claudeCodeConfig = {
    macOS: `mkdir -p ~/.claude
cat > ~/.claude/settings.json <<'EOF'
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "${tk}"
  }
}
EOF`,
    Windows: `New-Item -ItemType Directory -Force "$env:USERPROFILE\\.claude"
@'
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "${tk}"
  }
}
'@ | Set-Content "$env:USERPROFILE\\.claude\\settings.json"`,
    Linux: `mkdir -p ~/.claude
cat > ~/.claude/settings.json <<'EOF'
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.novadiffusion.com",
    "ANTHROPIC_AUTH_TOKEN": "${tk}"
  }
}
EOF`,
  };

  const claudeCodeInstall = {
    macOS: `curl -fsSL https://claude.ai/install.sh | bash`,
    Windows: `irm https://claude.ai/install.ps1 | iex`,
    Linux: `curl -fsSL https://claude.ai/install.sh | bash`,
  };

  const codexConfigToml = `model_provider = "hypitoken"

[model_providers.hypitoken]
name = "HypiToken"
base_url = "https://api.novadiffusion.com/v1"
wire_api = "responses"
requires_openai_auth = true`;

  const codexAuthJson = {
    macOS: `mkdir -p ~/.codex
cat > ~/.codex/config.toml <<'EOF'
${codexConfigToml}
EOF
cat > ~/.codex/auth.json <<'EOF'
{ "OPENAI_API_KEY": "${tk}" }
EOF
chmod 600 ~/.codex/auth.json`,
    Windows: `# In WSL2 (recommended):
mkdir -p ~/.codex
cat > ~/.codex/config.toml <<'EOF'
${codexConfigToml}
EOF
cat > ~/.codex/auth.json <<'EOF'
{ "OPENAI_API_KEY": "${tk}" }
EOF
chmod 600 ~/.codex/auth.json

# Or in native PowerShell:
New-Item -ItemType Directory -Force "$env:USERPROFILE\\.codex"
Set-Content "$env:USERPROFILE\\.codex\\config.toml" @'
${codexConfigToml}
'@
Set-Content "$env:USERPROFILE\\.codex\\auth.json" '{ "OPENAI_API_KEY": "${tk}" }'`,
    Linux: `mkdir -p ~/.codex
cat > ~/.codex/config.toml <<'EOF'
${codexConfigToml}
EOF
cat > ~/.codex/auth.json <<'EOF'
{ "OPENAI_API_KEY": "${tk}" }
EOF
chmod 600 ~/.codex/auth.json`,
  };

  const codexInstall = {
    macOS: `npm install -g @openai/codex`,
    Windows: `# WSL2 (recommended):
wsl --install
# Then inside Ubuntu:
npm install -g @openai/codex`,
    Linux: `npm install -g @openai/codex`,
  };

  return (
    <Dialog open={!!token} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[680px] max-h-[85vh] overflow-y-auto [&>*]:min-w-0">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Terminal className="h-4 w-4" />
            {t("tokens.useTokenDialog.title")}
            {token?.name && (
              <span className="font-mono text-sm font-normal text-muted-foreground">
                · {token.name}
              </span>
            )}
          </DialogTitle>
          <DialogDescription>{t("tokens.useTokenDialog.sub")}</DialogDescription>
        </DialogHeader>

        {/* OS selector */}
        <div className="flex gap-2 pt-1">
          {(["macOS", "Windows", "Linux"] as const).map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => setOS(s)}
              className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                os === s
                  ? "border-primary bg-primary/10 text-primary"
                  : "border-border text-muted-foreground hover:border-foreground/30 hover:text-foreground"
              }`}
            >
              {s}
            </button>
          ))}
        </div>

        {/* Tool selector */}
        <Tabs defaultValue="claude-code" className="mt-2 min-w-0">
          <TabsList className="w-full">
            <TabsTrigger value="claude-code" className="flex-1">
              Claude Code
            </TabsTrigger>
            <TabsTrigger value="codex" className="flex-1">
              Codex CLI
            </TabsTrigger>
          </TabsList>

          {/* Claude Code */}
          <TabsContent value="claude-code" className="space-y-4 pt-2 min-w-0">
            <div className="min-w-0">
              <p className="text-sm font-medium text-muted-foreground">
                {t("tokens.useTokenDialog.step1Install")}
              </p>
              <CodeBlock code={claudeCodeInstall[os]} />
            </div>
            <div className="min-w-0">
              <p className="text-sm font-medium text-muted-foreground">
                {t("tokens.useTokenDialog.step2Config")}
              </p>
              <CodeBlock code={claudeCodeConfig[os]} />
            </div>
            <div className="min-w-0">
              <p className="text-sm font-medium text-muted-foreground">
                {t("tokens.useTokenDialog.step3Run")}
              </p>
              <CodeBlock code="claude" />
            </div>
          </TabsContent>

          {/* Codex CLI */}
          <TabsContent value="codex" className="space-y-4 pt-2 min-w-0">
            <div className="min-w-0">
              <p className="text-sm font-medium text-muted-foreground">
                {t("tokens.useTokenDialog.step1Install")}
              </p>
              <CodeBlock code={codexInstall[os]} />
            </div>
            <div className="min-w-0">
              <p className="text-sm font-medium text-muted-foreground">
                {t("tokens.useTokenDialog.step2Config")}
              </p>
              <CodeBlock code={codexAuthJson[os]} />
            </div>
            <div className="min-w-0">
              <p className="text-sm font-medium text-muted-foreground">
                {t("tokens.useTokenDialog.step3Run")}
              </p>
              <CodeBlock code="codex" />
            </div>
          </TabsContent>
        </Tabs>

        <div className="min-w-0 rounded-md border border-border bg-muted/30 p-3 text-xs text-muted-foreground">
          <div className="font-medium text-foreground">{t("tokens.useTokenDialog.yourToken")}</div>
          <code className="mt-1 block w-full overflow-x-auto rounded bg-muted px-1.5 py-0.5 font-mono">
            {tk}
          </code>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function CreateTokenDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onCreated: () => void;
}) {
  const { t } = useTranslation();
  const { user } = useAuth();
  const spaces = user?.workspaces || [];
  const [name, setName] = useState("");
  const [cap, setCap] = useState("");
  const [groups, setGroups] = useState<string[]>([]);
  const [workspaceId, setWorkspaceId] = useState("0"); // "0" = personal

  const submit = async () => {
    try {
      await apiPost("/tokens", {
        name,
        monthly_usd_cap: parseFloat(cap) || 0,
        groups,
        workspace_id: Number(workspaceId) || 0,
      });
      toast.success(t("tokens.dialog.created"));
      onCreated();
      onOpenChange(false);
      setName("");
      setCap("");
      setGroups([]);
      setWorkspaceId("0");
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>{t("tokens.dialog.title")}</DialogTitle>
          <DialogDescription>{t("tokens.dialog.sub")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="space-y-2">
            <Label htmlFor="name">{t("common.name")}</Label>
            <Input
              id="name"
              placeholder={t("tokens.dialog.namePlaceholder")}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="cap">{t("tokens.dialog.capLabel")}</Label>
            <Input
              id="cap"
              type="number"
              step="0.01"
              placeholder={t("tokens.dialog.capPlaceholder")}
              value={cap}
              onChange={(e) => setCap(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">{t("tokens.dialog.capHint")}</p>
          </div>
          {spaces.length > 1 && (
            <div className="space-y-2">
              <Label htmlFor="ws">{t("tokens.dialog.billingSpace")}</Label>
              <select
                id="ws"
                className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                value={workspaceId}
                onChange={(e) => setWorkspaceId(e.target.value)}
              >
                <option value="0">{t("tokens.dialog.personalSpace")}</option>
                {spaces
                  .filter((w) => w.type === "enterprise")
                  .map((w) => (
                    <option key={w.id} value={String(w.id)}>
                      {w.name}
                    </option>
                  ))}
              </select>
              <p className="text-xs text-muted-foreground">{t("tokens.dialog.billingSpaceHint")}</p>
            </div>
          )}
          <div className="space-y-2">
            <Label>渠道（可选，按优先级排列）</Label>
            <ChannelPicker value={groups} onChange={setGroups} />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit}>{t("common.create")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
