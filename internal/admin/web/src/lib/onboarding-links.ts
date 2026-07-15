// ─────────────────────────────────────────────────────────────────────────
// Shared client-onboarding logic. Pure TypeScript, no React / i18n / UI deps
// so it can be kept BYTE-IDENTICAL across the sibling gateways
// (hypitoken + CPA-Claude). If you edit one copy, sync the other:
//   hypitoken: internal/admin/web/src/lib/onboarding-links.ts
//   CPA-Claude: internal/admin/web/src/lib/onboarding-links.ts
//
// It builds the one-click deep links (CC Switch, Cherry Studio) and the manual
// config snippets (Claude Code settings.json, Codex config.toml + auth.json)
// for a given gateway base URL + user token.
// ─────────────────────────────────────────────────────────────────────────

export type OnboardingOS = "macOS" | "Windows" | "Linux";
export type OnboardingTool = "claude-code" | "codex";

export interface OnboardingConfig {
  /** User's gateway token, used verbatim (already carries its sk- / sk-cpa- prefix). */
  token: string;
  /**
   * Gateway root URL WITHOUT a trailing /v1, e.g. "https://api.novadiffusion.com".
   * Anthropic (Claude Code) uses this as-is; Codex derives `${base}/v1`.
   */
  baseUrl: string;
  /** Display name for the provider inside client apps. Defaults to "New API". */
  providerName?: string;
}

const DEFAULT_PROVIDER = "New API";

/** Strip any trailing slash(es) so we never emit "https://host//v1". */
function trimBase(baseUrl: string): string {
  return baseUrl.replace(/\/+$/, "");
}

/** Anthropic base URL for Claude Code — the gateway root, no /v1 suffix. */
export function claudeBaseUrl(cfg: OnboardingConfig): string {
  return trimBase(cfg.baseUrl);
}

/** OpenAI/Codex base URL — gateway root + /v1. */
export function codexBaseUrl(cfg: OnboardingConfig): string {
  return `${trimBase(cfg.baseUrl)}/v1`;
}

// ── Deep links ───────────────────────────────────────────────────────────

/**
 * CC Switch import link. CC Switch is a desktop tool that writes the client's
 * config file (~/.claude/settings.json or ~/.codex/config.toml) on the user's
 * behalf, so a single click provisions Claude Code / Codex with zero manual
 * editing.
 *
 * `app` selects which client to configure. Codex needs the /v1 endpoint;
 * Claude uses the bare gateway root.
 */
export function buildCCSwitchURL(
  cfg: OnboardingConfig,
  app: "claude" | "codex",
  opts?: { name?: string; model?: string },
): string {
  const base = trimBase(cfg.baseUrl);
  const endpoint = app === "codex" ? `${base}/v1` : base;
  const params = new URLSearchParams();
  params.set("resource", "provider");
  params.set("app", app);
  params.set("name", opts?.name || cfg.providerName || DEFAULT_PROVIDER);
  params.set("endpoint", endpoint);
  params.set("apiKey", cfg.token);
  if (opts?.model) params.set("model", opts.model);
  params.set("homepage", base);
  params.set("enabled", "true");
  return `ccswitch://v1/import?${params.toString()}`;
}

/** UTF-8-safe base64 for the Cherry Studio payload (falls back for old runtimes). */
function toBase64(value: string): string {
  if (typeof window !== "undefined" && typeof window.btoa === "function") {
    // Payload is a URL + token (ASCII), but guard against any UTF-8 anyway.
    return window.btoa(unescape(encodeURIComponent(value)));
  }
  return "";
}

/**
 * Cherry Studio import link. Cherry Studio is a chat GUI; importing registers
 * this gateway as an OpenAI-compatible provider pointed at the /v1 endpoint.
 */
export function buildCherryStudioURL(cfg: OnboardingConfig): string {
  const payload = {
    id: "new-api",
    baseUrl: codexBaseUrl(cfg),
    apiKey: cfg.token,
  };
  const encoded = encodeURIComponent(toBase64(JSON.stringify(payload)));
  return `cherrystudio://providers/api-keys?v=1&data=${encoded}`;
}

// ── Manual snippets ──────────────────────────────────────────────────────

export function claudeCodeInstall(os: OnboardingOS): string {
  switch (os) {
    case "Windows":
      return `irm https://claude.ai/install.ps1 | iex`;
    default:
      return `curl -fsSL https://claude.ai/install.sh | bash`;
  }
}

/**
 * Shell environment variables — the fastest path: paste into a terminal, then
 * run `claude` in the same session. Ephemeral (only that shell) but zero files
 * touched. bash/zsh use `export`; PowerShell uses `$env:`.
 */
export function claudeCodeEnv(cfg: OnboardingConfig, os: OnboardingOS): string {
  const base = claudeBaseUrl(cfg);
  if (os === "Windows") {
    return `$env:ANTHROPIC_BASE_URL="${base}"
$env:ANTHROPIC_AUTH_TOKEN="${cfg.token}"`;
  }
  return `export ANTHROPIC_BASE_URL="${base}"
export ANTHROPIC_AUTH_TOKEN="${cfg.token}"`;
}

/** ~/.claude/settings.json writer, per OS shell. */
export function claudeCodeConfig(cfg: OnboardingConfig, os: OnboardingOS): string {
  const base = claudeBaseUrl(cfg);
  const json = `{
  "env": {
    "ANTHROPIC_BASE_URL": "${base}",
    "ANTHROPIC_AUTH_TOKEN": "${cfg.token}"
  }
}`;
  if (os === "Windows") {
    return `New-Item -ItemType Directory -Force "$env:USERPROFILE\\.claude"
@'
${json}
'@ | Set-Content "$env:USERPROFILE\\.claude\\settings.json"`;
  }
  return `mkdir -p ~/.claude
cat > ~/.claude/settings.json <<'EOF'
${json}
EOF`;
}

export function codexInstall(os: OnboardingOS): string {
  if (os === "Windows") {
    return `# WSL2 (recommended):
wsl --install
# Then inside Ubuntu:
npm install -g @openai/codex`;
  }
  return `npm install -g @openai/codex`;
}

/** ~/.codex/config.toml body pointing at the gateway's /v1 responses API. */
export function codexConfigToml(cfg: OnboardingConfig): string {
  const name = cfg.providerName || DEFAULT_PROVIDER;
  const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, "");
  return `model_provider = "${slug}"

[model_providers.${slug}]
name = "${name}"
base_url = "${codexBaseUrl(cfg)}"
wire_api = "responses"
requires_openai_auth = true`;
}

/** Full ~/.codex/{config.toml,auth.json} writer, per OS shell. */
export function codexConfig(cfg: OnboardingConfig, os: OnboardingOS): string {
  const toml = codexConfigToml(cfg);
  const auth = `{ "OPENAI_API_KEY": "${cfg.token}" }`;
  if (os === "Windows") {
    return `# In WSL2 (recommended):
mkdir -p ~/.codex
cat > ~/.codex/config.toml <<'EOF'
${toml}
EOF
cat > ~/.codex/auth.json <<'EOF'
${auth}
EOF
chmod 600 ~/.codex/auth.json

# Or in native PowerShell:
New-Item -ItemType Directory -Force "$env:USERPROFILE\\.codex"
Set-Content "$env:USERPROFILE\\.codex\\config.toml" @'
${toml}
'@
Set-Content "$env:USERPROFILE\\.codex\\auth.json" '${auth}'`;
  }
  return `mkdir -p ~/.codex
cat > ~/.codex/config.toml <<'EOF'
${toml}
EOF
cat > ~/.codex/auth.json <<'EOF'
${auth}
EOF
chmod 600 ~/.codex/auth.json`;
}
