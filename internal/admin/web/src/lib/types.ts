// Shared TypeScript types mirroring /api/v2/* JSON shapes.

export interface User {
  id: number;
  email: string;
  role: "user" | "admin";
  balance_usd: number;
  group_id: number;
  email_verified: boolean;
  created_at: number;
  disabled?: boolean;
}

export interface PricingGroup {
  ID: number;
  Name: string;
  Description: string;
  // Billing: final_charge_USD = official_USD × multiplier.
  // Defaults: claude=0.3, codex=0.05.
  CodexMultiplier: number;
  ClaudeMultiplier: number;
  CredentialGroup: string;
  IsDefault: boolean;
  CreatedAt: string;
  UpdatedAt: string;
}

export interface UserToken {
  id: number;
  token: string;
  name: string;
  daily_usd_cap: number;
  monthly_usd_cap: number;
  max_concurrent: number;
  rpm: number;
  disabled: boolean;
  last_used_at: number;
  created_at: number;
  // Priority-ordered credential-channel fallthrough list. Empty = use the
  // user's pricing group (legacy routing).
  groups?: string[];
}

// Channel = a credential group with at least one usable (non-disabled,
// non-hard-failed) backing credential, as reported by /api/v2/channels.
// Powers the per-token "渠道" dropdown so users only see options that
// actually have credentials behind them.
export interface Channel {
  name: string; // group filter name ("default", "kiro-anthropic", ...)
  providers: string[]; // distinct providers backing this channel
  count: number; // usable credential count
}

export interface WalletTx {
  id: number;
  kind: "topup" | "charge" | "adjust" | "refund";
  amount_usd: number;
  ref: string;
  note: string;
  created_at: number;
}

export interface AlipayOrder {
  out_trade_no: string;
  cny_amount: number;
  usd_credit: number;
  rate: number;
  status: "pending" | "paid" | "expired" | "failed";
  trade_no: string;
  qr_code: string;
  pay_url?: string;
  img?: string;
  created_at: number;
  paid_at: number;
}

export interface ModelHealth {
  id: number;
  auth_id: string;
  provider: string;
  model: string;
  status: "ok" | "fail";
  latency_ms: number;
  error: string;
  checked_at: number;
}

export interface ExchangeRate {
  cny_per_usd: number;
  as_of: number;
}

// UsageCounts mirrors cc-core usage.Counts (token + request tallies).
export interface UsageCounts {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  errors: number;
}

// UsageDay is one entry in the 14-day per-credential daily series.
export interface UsageDay {
  day: string;
  input_tokens: number;
  output_tokens: number;
}

// CredentialUsage mirrors the Go usageSummary struct attached to each
// credential row (total / rolling windows / daily series / lifetime cost).
export interface CredentialUsage {
  total: UsageCounts;
  sum_24h: UsageCounts;
  sum_5h: UsageCounts;
  last_used?: string;
  daily: UsageDay[];
  total_cost_usd: number;
}

// Credential mirrors the Go authRow struct served by
// /api/v2/admin/credentials. Time fields arrive as RFC3339 strings.
export interface Credential {
  id: string;
  kind: string; // "oauth" | "apikey"
  provider: string; // "anthropic" | "openai"
  plan_type?: string;
  label: string;
  email?: string;
  proxy_url: string;
  base_url?: string;
  group?: string;
  max_concurrent: number;
  active_clients: number;
  client_tokens: string[];
  disabled: boolean;
  quota_exceeded: boolean;
  quota_reset_at?: string;
  expires_at?: string;
  file_backed: boolean;
  healthy: boolean;
  hard_failure: boolean;
  failure_reason?: string;
  refresh_suspended?: boolean;
  refresh_suspended_reason?: string;
  last_client_cancel?: string;
  client_cancel_reason?: string;
  model_map?: Record<string, string>;
  usage?: CredentialUsage;
  codex_rate_limits?: Record<string, string>;
  codex_rate_limits_at?: string;
  // Kiro-specific extras surfaced on the same row.
  active?: number;
  plan?: string;
}

// AdminOrder mirrors the Go db.AlipayOrder struct (no json tags → PascalCase)
// served by /admin/orders. Time fields arrive as RFC3339 strings.
export interface AdminOrder {
  OutTradeNo: string;
  UserID: number;
  CNYAmount: number;
  USDCredit: number;
  Rate: number;
  Status: string;
  TradeNo: string;
  QRCode: string;
  CreatedAt: string;
  PaidAt: string;
}

// AdminAdjustment is one fleet-wide wallet adjustment (kind='adjust') served
// by /admin/adjustments — a manual operator grant or a channel signup bonus.
// A `ref` of "signup_bonus:<slug>" marks the latter.
export interface AdminAdjustment {
  id: number;
  user_id: number;
  email: string;
  amount_usd: number;
  ref: string;
  note: string;
  created_at: number;
}
