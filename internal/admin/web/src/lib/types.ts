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
