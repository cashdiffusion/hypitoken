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
  // Public-arena profile (leaderboard / Agent office). Attached by /me.
  display_name?: string;
  name_is_default?: boolean;
  public_opt_in?: boolean;
}

// Leaderboard / arena shapes (GET /arena/leaderboard, SSE /arena/stream).
export interface LeaderRow {
  rank: number;
  actor: string;
  name: string;
  public: boolean;
  is_you: boolean;
  tokens: number;
  requests: number;
  last_seen: number;
}

export interface LeaderboardResponse {
  metric: "tokens" | "requests";
  rows: LeaderRow[];
  you: LeaderRow | null;
}

export interface ArenaEvent {
  actor: string;
  name: string;
  public: boolean;
  provider: string;
  model: string;
  tokens: number;
  ts: number;
  is_you: boolean;
}

export interface UserProfile {
  display_name: string;
  name_is_default: boolean;
  public_opt_in: boolean;
  lifetime_tokens: number;
  lifetime_requests: number;
  last_active_at: number;
}

export interface Greeting {
  country_code: string;
  city: string;
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

// ── Referral / gift system (internal/saas/referral) ─────────────────────────

export interface ReferralTier {
  id: number;
  campaign_id: number;
  threshold: number;
  tier_name: string;
  card_style_unlock: string;
  bonus_usd: number;
  badge: string;
}

export interface ReferralCampaign {
  id: number;
  slug: string;
  name: string;
  kind: string;
  // `status` is present on the admin campaign list, omitted from the
  // user-facing campaign payload.
  status?: string;
  invitee_bonus_usd: number;
  inviter_bonus_usd: number;
  gift_expiry_days: number;
  max_gift_usd: number;
  daily_budget_usd?: number;
  headline: string;
  subcopy: string;
  variant?: string;
  tiers: ReferralTier[];
}

export interface ReferralCard {
  id: number;
  owner_user_id: number;
  campaign_id: number;
  code: string;
  card_style: "openai" | "claude";
  card_tone: "dark" | "light";
  tagline: string;
  message: string;
  impressions: number;
  created_at: number;
}

export interface ReferralStats {
  invites: number;
  earned_usd: number;
  pending_usd: number;
  rank: number;
  current_tier: ReferralTier | null;
  next_tier: ReferralTier | null;
  next_remaining: number;
  unlocked_styles: string[];
}

export interface ReferralMe {
  stats: ReferralStats;
  card: ReferralCard;
  invite_url: string;
  invite_code: string;
  campaign: ReferralCampaign;
}

export interface GiftCard {
  id: number;
  code: string;
  recipient_email: string;
  amount_usd: number;
  message: string;
  card_style: "openai" | "claude";
  card_tone: "dark" | "light";
  status: "pending" | "claimed" | "expired" | "refunded";
  created_at: number;
  expires_at?: number;
  claimed_at?: number;
}

// Admin referral ops dashboard.
export interface ReferralTopReferrer {
  user_id: number;
  email: string;
  invites: number;
  earned_usd: number;
}

export interface ReferralGiftTotals {
  sent_count: number;
  sent_usd: number;
  claimed_count: number;
  claimed_usd: number;
  pending_count: number;
  pending_usd: number;
  refunded_count: number;
  refunded_usd: number;
}

export interface ReferralOpsStats {
  total_users: number;
  cards_minted: number;
  impressions: number;
  conversions: number;
  fraud_blocked: number;
  inviters: number;
  platform_spend: number;
  k_factor: number;
  gift_totals: ReferralGiftTotals;
  top_referrers: ReferralTopReferrer[];
  today_bonus_usd: number;
  daily_budget_usd: number;
  budget_tripped: boolean;
}
