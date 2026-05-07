import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Plus, RefreshCw, Trash2, Copy, Check, Eye, EyeOff, Terminal } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { apiDelete, apiGet, apiPost } from "@/lib/api";
import { copyToClipboard, fmtUSD } from "@/lib/utils";
import type { UserToken } from "@/lib/types";

function detectOS(): "Windows" | "macOS" | "Linux" {
  const ua = navigator.userAgent;
  if (/Windows/i.test(ua)) return "Windows";
  if (/Mac OS X/i.test(ua)) return "macOS";
  return "Linux";
}

export default function TokensPage() {
  const { t: tt } = useTranslation();
  const [tokens, setTokens] = useState<UserToken[]>([]);
  const [open, setOpen] = useState(false);
  const [reveal, setReveal] = useState<Record<number, boolean>>({});
  const [useToken, setUseToken] = useState<UserToken | null>(null);

  const refresh = async () => {
    const r = await apiGet<{ tokens: UserToken[] }>("/tokens");
    setTokens(r.tokens || []);
  };
  useEffect(() => { refresh(); }, []);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="font-display text-3xl font-semibold tracking-tight">{tt("tokens.title")}</h1>
          <p className="text-muted-foreground">{tt("tokens.sub")}</p>
        </div>
        <Button onClick={() => setOpen(true)} className="gap-2"><Plus className="h-4 w-4" /> {tt("tokens.newToken")}</Button>
      </div>

      <Card>
        <CardContent className="p-0">
          {tokens.length === 0 ? (
            <div className="p-12 text-center text-sm text-muted-foreground">{tt("tokens.none")}</div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{tt("common.name")}</TableHead>
                  <TableHead>Token</TableHead>
                  <TableHead className="text-right">{tt("tokens.spendingCap")}</TableHead>
                  <TableHead></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tokens.map((t) => (
                  <TableRow key={t.id} className={t.disabled ? "opacity-50" : ""}>
                    <TableCell className="font-medium">{t.name || <span className="text-muted-foreground">(unnamed)</span>}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <code className="rounded bg-muted px-2 py-0.5 font-mono text-xs">
                          {reveal[t.id] ? t.token : t.token.slice(0, 12) + "…" + t.token.slice(-4)}
                        </code>
                        <Button size="icon" variant="ghost" className="h-7 w-7" onClick={() => setReveal((r) => ({ ...r, [t.id]: !r[t.id] }))}>
                          {reveal[t.id] ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                        </Button>
                        <CopyBtn text={t.token} />
                      </div>
                    </TableCell>
                    <TableCell className="font-mono tabular-nums text-right">{t.monthly_usd_cap > 0 ? fmtUSD(t.monthly_usd_cap) : tt("common.unlimited")}</TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        <Button size="sm" variant="outline" className="h-8 gap-1.5 text-xs" onClick={() => setUseToken(t)}>
                          <Terminal className="h-3.5 w-3.5" />
                          {tt("tokens.useToken")}
                        </Button>
                        <Button size="icon" variant="ghost" className="h-8 w-8" title={tt("tokens.rotate")} onClick={async () => {
                          await apiPost(`/tokens/${t.id}/rotate`);
                          toast.success(tt("tokens.rotated"));
                          refresh();
                        }}>
                          <RefreshCw className="h-3.5 w-3.5" />
                        </Button>
                        <Button size="icon" variant="ghost" className="h-8 w-8 text-destructive hover:bg-destructive/10" onClick={async () => {
                          if (!confirm(tt("tokens.confirmDelete"))) return;
                          await apiDelete(`/tokens/${t.id}`);
                          toast.success(tt("tokens.deleted"));
                          refresh();
                        }}>
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <CreateTokenDialog open={open} onOpenChange={setOpen} onCreated={refresh} />
      <UseTokenDialog token={useToken} onClose={() => setUseToken(null)} />
    </div>
  );
}

function CopyBtn({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button size="icon" variant="ghost" className="h-7 w-7" onClick={async () => {
      await copyToClipboard(text);
      setDone(true);
      setTimeout(() => setDone(false), 1500);
    }}>
      {done ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
    </Button>
  );
}

function CodeBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="relative mt-3 overflow-hidden rounded-lg border border-border bg-[#0d1117]">
      <button
        onClick={async () => {
          await copyToClipboard(code);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
        className="absolute right-2 top-2 inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs text-zinc-400 transition-colors hover:bg-white/10 hover:text-white"
      >
        {copied ? <Check className="h-3 w-3 text-green-400" /> : <Copy className="h-3 w-3" />}
        {copied ? "Copied" : "Copy"}
      </button>
      <pre className="overflow-x-auto px-4 py-4 font-mono text-sm leading-relaxed text-zinc-200">{code}</pre>
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
      <DialogContent className="sm:max-w-[680px] max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Terminal className="h-4 w-4" />
            {t("tokens.useTokenDialog.title")}
            {token?.name && <span className="font-mono text-sm font-normal text-muted-foreground">· {token.name}</span>}
          </DialogTitle>
          <DialogDescription>{t("tokens.useTokenDialog.sub")}</DialogDescription>
        </DialogHeader>

        {/* OS selector */}
        <div className="flex gap-2 pt-1">
          {(["macOS", "Windows", "Linux"] as const).map((s) => (
            <button
              key={s}
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
        <Tabs defaultValue="claude-code" className="mt-2">
          <TabsList className="w-full">
            <TabsTrigger value="claude-code" className="flex-1">Claude Code</TabsTrigger>
            <TabsTrigger value="codex" className="flex-1">Codex CLI</TabsTrigger>
          </TabsList>

          {/* Claude Code */}
          <TabsContent value="claude-code" className="space-y-4 pt-2">
            <div>
              <p className="text-sm font-medium text-muted-foreground">{t("tokens.useTokenDialog.step1Install")}</p>
              <CodeBlock code={claudeCodeInstall[os]} />
            </div>
            <div>
              <p className="text-sm font-medium text-muted-foreground">{t("tokens.useTokenDialog.step2Config")}</p>
              <CodeBlock code={claudeCodeConfig[os]} />
            </div>
            <div>
              <p className="text-sm font-medium text-muted-foreground">{t("tokens.useTokenDialog.step3Run")}</p>
              <CodeBlock code="claude" />
            </div>
          </TabsContent>

          {/* Codex CLI */}
          <TabsContent value="codex" className="space-y-4 pt-2">
            <div>
              <p className="text-sm font-medium text-muted-foreground">{t("tokens.useTokenDialog.step1Install")}</p>
              <CodeBlock code={codexInstall[os]} />
            </div>
            <div>
              <p className="text-sm font-medium text-muted-foreground">{t("tokens.useTokenDialog.step2Config")}</p>
              <CodeBlock code={codexAuthJson[os]} />
            </div>
            <div>
              <p className="text-sm font-medium text-muted-foreground">{t("tokens.useTokenDialog.step3Run")}</p>
              <CodeBlock code="codex" />
            </div>
          </TabsContent>
        </Tabs>

        <div className="rounded-md border border-border bg-muted/30 p-3 text-xs text-muted-foreground">
          <strong className="text-foreground">{t("tokens.useTokenDialog.yourToken")}</strong>{" "}
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{tk}</code>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function CreateTokenDialog({ open, onOpenChange, onCreated }: any) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [cap, setCap] = useState("");

  const submit = async () => {
    try {
      await apiPost("/tokens", {
        name,
        monthly_usd_cap: parseFloat(cap) || 0,
      });
      toast.success(t("tokens.dialog.created"));
      onCreated();
      onOpenChange(false);
      setName(""); setCap("");
    } catch (e: any) {
      toast.error(e.message);
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
            <Input id="name" placeholder={t("tokens.dialog.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="cap">{t("tokens.dialog.capLabel")}</Label>
            <Input id="cap" type="number" step="0.01" placeholder={t("tokens.dialog.capPlaceholder")} value={cap} onChange={(e) => setCap(e.target.value)} />
            <p className="text-xs text-muted-foreground">{t("tokens.dialog.capHint")}</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button onClick={submit}>{t("common.create")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
