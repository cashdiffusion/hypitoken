// ─────────────────────────────────────────────────────────────────────────
// Shared client-onboarding panel. Self-contained: depends only on
// @/components/ui/{button,tabs}, @/lib/utils, ./onboarding-links and
// lucide-react — all present in BOTH sibling gateways — so it can be kept
// BYTE-IDENTICAL across hypitoken + CPA-Claude. All user-facing copy comes
// through `labels` (defaults to English); hypitoken passes i18n strings,
// CPA-Claude uses the defaults. If you edit one copy, sync the other.
// ─────────────────────────────────────────────────────────────────────────
import { ArrowRightLeft, Check, Copy, MessageSquare, Terminal } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  buildCCSwitchURL,
  buildCherryStudioURL,
  claudeCodeConfig,
  claudeCodeEnv,
  claudeCodeInstall,
  codexConfig,
  codexInstall,
  type OnboardingConfig,
  type OnboardingOS,
} from "@/lib/onboarding-links";
import { cn, copyToClipboard } from "@/lib/utils";

export interface OnboardingLabels {
  oneClick: string;
  oneClickHint: string;
  openInCCSwitch: string;
  orManual: string;
  step1Install: string;
  step2Config: string;
  step2Env: string;
  step2EnvHint: string;
  step2File: string;
  step2FileHint: string;
  step3Run: string;
  cherryTitle: string;
  cherryHint: string;
  importToCherry: string;
  yourToken: string;
  copy: string;
  copied: string;
}

const DEFAULT_LABELS: OnboardingLabels = {
  oneClick: "One-click setup",
  oneClickHint:
    "CC Switch writes the client config for you — no manual file editing. Requires the CC Switch desktop app.",
  openInCCSwitch: "Open in CC Switch",
  orManual: "or configure manually",
  step1Install: "1. Install the CLI",
  step2Config: "2. Configure",
  step2Env: "Environment variables (this shell)",
  step2EnvHint: "Paste into your terminal, then run claude in the same session.",
  step2File: "Or persist to the config file",
  step2FileHint: "Survives new terminals.",
  step3Run: "3. Run it",
  cherryTitle: "Chat client",
  cherryHint: "Import this gateway into Cherry Studio as an OpenAI-compatible provider.",
  importToCherry: "Import to Cherry Studio",
  yourToken: "Your token",
  copy: "Copy",
  copied: "Copied",
};

function CodeBlock({
  code,
  copyLabel,
  copiedLabel,
}: {
  code: string;
  copyLabel: string;
  copiedLabel: string;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="relative mt-2 w-full min-w-0 rounded-lg border border-border bg-[#0d1117]">
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
        {copied ? copiedLabel : copyLabel}
      </button>
      <pre className="overflow-x-auto px-4 py-4 pr-16 font-mono text-xs leading-relaxed text-zinc-200">
        {code}
      </pre>
    </div>
  );
}

function OSPills({ os, setOS }: { os: OnboardingOS; setOS: (o: OnboardingOS) => void }) {
  return (
    <div className="flex gap-2 pt-1">
      {(["macOS", "Windows", "Linux"] as const).map((s) => (
        <button
          key={s}
          type="button"
          onClick={() => setOS(s)}
          className={cn(
            "rounded-full border px-3 py-1 text-xs font-medium transition-colors",
            os === s
              ? "border-primary bg-primary/10 text-primary"
              : "border-border text-muted-foreground hover:border-foreground/30 hover:text-foreground",
          )}
        >
          {s}
        </button>
      ))}
    </div>
  );
}

export interface ClientOnboardingProps {
  config: OnboardingConfig;
  labels?: Partial<OnboardingLabels>;
  /** Optional initial OS; auto-detected by the caller when omitted. */
  initialOS?: OnboardingOS;
}

export function ClientOnboarding({ config, labels, initialOS }: ClientOnboardingProps) {
  const L = { ...DEFAULT_LABELS, ...labels };
  const [os, setOS] = useState<OnboardingOS>(initialOS ?? "macOS");

  const install = {
    install: claudeCodeInstall(os),
    env: claudeCodeEnv(config, os),
    config: claudeCodeConfig(config, os),
  };
  const codex = { install: codexInstall(os), config: codexConfig(config, os) };
  const cb = (code: string) => <CodeBlock code={code} copyLabel={L.copy} copiedLabel={L.copied} />;

  const ccSwitchButton = (app: "claude" | "codex") => (
    <div className="rounded-md border border-primary/30 bg-primary/5 p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium text-foreground">{L.oneClick}</div>
          <div className="mt-0.5 text-xs text-muted-foreground">{L.oneClickHint}</div>
        </div>
        <Button
          type="button"
          size="sm"
          className="shrink-0 gap-1.5"
          onClick={() => window.open(buildCCSwitchURL(config, app), "_blank")}
        >
          <ArrowRightLeft className="h-3.5 w-3.5" />
          {L.openInCCSwitch}
        </Button>
      </div>
    </div>
  );

  const manualDivider = (
    <div className="flex items-center gap-3 pt-1 text-[11px] uppercase tracking-wider text-muted-foreground">
      <span className="h-px flex-1 bg-border" />
      {L.orManual}
      <span className="h-px flex-1 bg-border" />
    </div>
  );

  return (
    <div className="space-y-3">
      <OSPills os={os} setOS={setOS} />

      <Tabs defaultValue="claude-code" className="mt-1 min-w-0">
        <TabsList className="w-full">
          <TabsTrigger value="claude-code" className="flex-1 gap-1.5">
            <Terminal className="h-3.5 w-3.5" /> Claude Code
          </TabsTrigger>
          <TabsTrigger value="codex" className="flex-1 gap-1.5">
            <Terminal className="h-3.5 w-3.5" /> Codex CLI
          </TabsTrigger>
        </TabsList>

        <TabsContent value="claude-code" className="space-y-3 pt-2 min-w-0">
          {ccSwitchButton("claude")}
          {manualDivider}
          <div className="min-w-0">
            <p className="text-sm font-medium text-muted-foreground">{L.step1Install}</p>
            {cb(install.install)}
          </div>
          <div className="min-w-0">
            <p className="text-sm font-medium text-muted-foreground">{L.step2Config}</p>
            <p className="mt-2 text-xs font-medium text-foreground">{L.step2Env}</p>
            <p className="text-[11px] text-muted-foreground">{L.step2EnvHint}</p>
            {cb(install.env)}
            <p className="mt-3 text-xs font-medium text-foreground">{L.step2File}</p>
            <p className="text-[11px] text-muted-foreground">{L.step2FileHint}</p>
            {cb(install.config)}
          </div>
          <div className="min-w-0">
            <p className="text-sm font-medium text-muted-foreground">{L.step3Run}</p>
            {cb("claude")}
          </div>
        </TabsContent>

        <TabsContent value="codex" className="space-y-3 pt-2 min-w-0">
          {ccSwitchButton("codex")}
          {manualDivider}
          <div className="min-w-0">
            <p className="text-sm font-medium text-muted-foreground">{L.step1Install}</p>
            {cb(codex.install)}
          </div>
          <div className="min-w-0">
            <p className="text-sm font-medium text-muted-foreground">{L.step2Config}</p>
            {cb(codex.config)}
          </div>
          <div className="min-w-0">
            <p className="text-sm font-medium text-muted-foreground">{L.step3Run}</p>
            {cb("codex")}
          </div>
        </TabsContent>
      </Tabs>

      <div className="rounded-md border border-border bg-muted/30 p-3">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-1.5 text-sm font-medium text-foreground">
              <MessageSquare className="h-3.5 w-3.5" /> {L.cherryTitle}
            </div>
            <div className="mt-0.5 text-xs text-muted-foreground">{L.cherryHint}</div>
          </div>
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="shrink-0 gap-1.5"
            onClick={() => window.open(buildCherryStudioURL(config), "_blank")}
          >
            <ArrowRightLeft className="h-3.5 w-3.5" />
            {L.importToCherry}
          </Button>
        </div>
      </div>

      <div className="min-w-0 rounded-md border border-border bg-muted/30 p-3 text-xs text-muted-foreground">
        <div className="font-medium text-foreground">{L.yourToken}</div>
        <code className="mt-1 block w-full overflow-x-auto rounded bg-muted px-1.5 py-0.5 font-mono">
          {config.token}
        </code>
      </div>
    </div>
  );
}
