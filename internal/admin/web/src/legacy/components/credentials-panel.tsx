import { useState } from "react";
import { KeyRound, Plus, FileJson, Cookie } from "lucide-react";
import type { AuthRow, Provider, Summary } from "@/legacy/lib/types";
import { AuthCard } from "./auth-card";
import { Button } from "@/components/ui/button";

type Action = "toggle" | "refresh" | "clear-quota" | "clear-failure" | "delete";

interface Props {
  summary: Summary | null;
  onAction: (a: AuthRow, act: Action) => void;
  onEdit: (a: AuthRow) => void;
  onAddOAuth: (provider: Provider) => void;
  onAddAPIKey: (provider: Provider) => void;
  onUpload: (provider: Provider) => void;
  onAddSessionCookie: () => void;
}

// Sub-tab per upstream provider. The user explicitly asked for strict
// separation — a single credential's auth flow, pricing, and upstream
// are provider-specific, so mixing them in one card list makes the UI
// unclear. Tokens / logs / stats / status stay unified at their own
// top-level tabs.
const TABS: { id: Provider; label: string; signInCta: string }[] = [
  { id: "anthropic", label: "Claude", signInCta: "Sign in with Claude" },
  { id: "openai", label: "Codex (ChatGPT)", signInCta: "Sign in with ChatGPT" },
];

function normProvider(p: string | undefined): Provider {
  return p === "openai" ? "openai" : "anthropic";
}

export function CredentialsPanel({
  summary,
  onAction,
  onEdit,
  onAddOAuth,
  onAddAPIKey,
  onUpload,
  onAddSessionCookie,
}: Props) {
  const [provider, setProvider] = useState<Provider>("anthropic");
  const auths = summary?.auths || [];
  const scoped = auths.filter((a) => normProvider(a.provider) === provider);
  const oauths = scoped.filter((a) => a.kind === "oauth");
  const apikeys = scoped.filter((a) => a.kind === "apikey");
  const healthy = scoped.filter((a) => a.healthy).length;
  const quota = scoped.filter((a) => a.quota_exceeded).length;
  const unhealthy = scoped.filter((a) => a.hard_failure).length;

  const current = TABS.find((t) => t.id === provider)!;

  // Per-provider counts so operators can see at a glance which tab has
  // credentials. Rendered inline on the tab buttons.
  const countFor = (p: Provider) =>
    auths.filter((a) => normProvider(a.provider) === p).length;

  return (
    <div className="space-y-6">
      <header className="flex items-end justify-between gap-4 flex-wrap">
        <div>
          <div className="eyebrow mb-1.5">§ Credentials management</div>
          <h2 className="font-display text-3xl md:text-4xl tracking-tight">
            Auth <span className="text-muted-foreground">pool</span>
          </h2>
          <p className="text-sm text-muted-foreground mt-1.5 mono tabular">
            <span className="text-[color:var(--success)] font-medium">{healthy}</span> healthy ·{" "}
            <span className="text-[color:var(--warning)] font-medium">{quota}</span> quota ·{" "}
            <span className="text-destructive font-medium">{unhealthy}</span> unhealthy ·{" "}
            {oauths.length} OAuth · {apikeys.length} API key(s)
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => onAddOAuth(provider)} className="gap-2">
            <Plus className="h-4 w-4" />
            {current.signInCta}
          </Button>
          {provider === "anthropic" && (
            <Button variant="outline" onClick={onAddSessionCookie} className="gap-2">
              <Cookie className="h-4 w-4" />
              Session cookie
            </Button>
          )}
          <Button variant="outline" onClick={() => onAddAPIKey(provider)} className="gap-2">
            <KeyRound className="h-4 w-4" />
            API key
          </Button>
          <Button variant="outline" onClick={() => onUpload(provider)} className="gap-2">
            <FileJson className="h-4 w-4" />
            Upload {current.label === "Claude" ? "Claude" : "Codex"} JSON
          </Button>
        </div>
      </header>

      {/* Provider sub-tabs. Strict separation — Claude and Codex credentials
          never mix in the same list. */}
      <nav className="flex gap-1 border-b border-border-strong">
        {TABS.map((t) => {
          const active = t.id === provider;
          const n = countFor(t.id);
          return (
            <button
              key={t.id}
              onClick={() => setProvider(t.id)}
              className={
                "px-4 py-2 text-sm font-medium transition-colors -mb-px border-b-2 " +
                (active
                  ? "border-foreground text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground")
              }
            >
              {t.label}
              <span className="ml-2 mono text-xs opacity-70 tabular">{n}</span>
            </button>
          );
        })}
      </nav>

      {!summary ? (
        <div className="py-16 text-center eyebrow animate-pulse bg-card border border-border-strong rounded-md">
          <span className="opacity-60">Loading credentials…</span>
        </div>
      ) : scoped.length === 0 ? (
        <div className="py-14 px-6 text-center text-sm text-muted-foreground font-mono bg-card border border-border-strong rounded-md">
          No {current.label} credentials yet — use the buttons above to add one.
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3 md:gap-4">
          {scoped.map((a) => (
            <AuthCard key={a.id} a={a} onAction={onAction} onEdit={onEdit} />
          ))}
        </div>
      )}
    </div>
  );
}
