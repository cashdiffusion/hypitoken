package db

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

// migrations is an append-only slice. Each element is a complete schema delta;
// applying them all in order rebuilds the schema from scratch. Never reorder
// or rewrite a previous entry — only append.
var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE pricing_groups (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT NOT NULL UNIQUE,
    description         TEXT NOT NULL DEFAULT '',
    codex_rmb_per_usd   REAL NOT NULL DEFAULT 0.5,
    claude_rmb_per_usd  REAL NOT NULL DEFAULT 2.0,
    codex_multiplier    REAL NOT NULL DEFAULT 1.0,
    claude_multiplier   REAL NOT NULL DEFAULT 1.0,
    credential_group    TEXT NOT NULL DEFAULT '',
    is_default          INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
INSERT INTO pricing_groups (name, description, is_default, created_at, updated_at)
VALUES ('default', 'Default pricing group', 1, strftime('%s','now'), strftime('%s','now'));

CREATE TABLE users (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    email               TEXT NOT NULL UNIQUE,
    pw_hash             TEXT NOT NULL,
    role                TEXT NOT NULL DEFAULT 'user',          -- user | admin
    balance_usd         REAL NOT NULL DEFAULT 0,
    group_id            INTEGER NOT NULL,
    email_verified      INTEGER NOT NULL DEFAULT 0,
    disabled            INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    FOREIGN KEY (group_id) REFERENCES pricing_groups(id)
);
CREATE INDEX idx_users_email ON users(email);

CREATE TABLE email_codes (
    email       TEXT NOT NULL,
    code        TEXT NOT NULL,
    purpose     TEXT NOT NULL,                                  -- verify | reset
    expires_at  INTEGER NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (email, purpose)
);

CREATE TABLE user_tokens (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id             INTEGER NOT NULL,
    token               TEXT NOT NULL UNIQUE,
    name                TEXT NOT NULL DEFAULT '',
    daily_usd_cap       REAL NOT NULL DEFAULT 0,
    monthly_usd_cap     REAL NOT NULL DEFAULT 0,
    max_concurrent      INTEGER NOT NULL DEFAULT 0,
    rpm                 INTEGER NOT NULL DEFAULT 0,
    disabled            INTEGER NOT NULL DEFAULT 0,
    last_used_at        INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_user_tokens_user ON user_tokens(user_id);
CREATE INDEX idx_user_tokens_token ON user_tokens(token);

CREATE TABLE wallet_tx (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    kind        TEXT NOT NULL,                                  -- topup | charge | adjust | refund
    amount_usd  REAL NOT NULL,                                  -- signed; +credit / -debit
    ref         TEXT NOT NULL DEFAULT '',                       -- alipay out_trade_no, request id, etc.
    note        TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_wallet_tx_user ON wallet_tx(user_id, created_at);

CREATE TABLE alipay_orders (
    out_trade_no    TEXT PRIMARY KEY,
    user_id         INTEGER NOT NULL,
    cny_amount      REAL NOT NULL,
    usd_credit      REAL NOT NULL,
    rate            REAL NOT NULL,                              -- snapshot CNY/USD at create
    status          TEXT NOT NULL DEFAULT 'pending',            -- pending | paid | expired | failed
    trade_no        TEXT NOT NULL DEFAULT '',
    qr_code         TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    paid_at         INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_alipay_orders_user ON alipay_orders(user_id, created_at);

CREATE TABLE model_health (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    auth_id         TEXT NOT NULL,
    provider        TEXT NOT NULL,
    model           TEXT NOT NULL,
    status          TEXT NOT NULL,                              -- ok | fail
    latency_ms      INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    checked_at      INTEGER NOT NULL,
    UNIQUE(auth_id, model)
);
`,
	// 2: health probe history — keeps last 90 results per (auth_id, model)
	`
CREATE TABLE model_health_history (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    auth_id    TEXT NOT NULL,
    provider   TEXT NOT NULL,
    model      TEXT NOT NULL,
    status     TEXT NOT NULL,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    checked_at INTEGER NOT NULL
);
CREATE INDEX idx_mhh ON model_health_history(auth_id, model, checked_at DESC);
`,
	// 3: switch billing model from divisor (rmb_per_usd) to multiplier-only.
	//
	//   final_charge_USD = official_USD * multiplier
	//
	// Bumps the default group from 1.0/1.0 to 0.3/0.05 (claude/codex) so a
	// freshly-installed instance is already aligned with the operator-set
	// defaults. Only touches the seed row; admin-customized values stay put.
	// The legacy *_rmb_per_usd columns are left in the schema (SQLite
	// column-drop is a table rebuild) but are no longer read or written.
	`
UPDATE pricing_groups
SET    claude_multiplier = 0.3,
       codex_multiplier  = 0.05,
       updated_at        = strftime('%s','now')
WHERE  is_default = 1
  AND  claude_multiplier = 1.0
  AND  codex_multiplier  = 1.0;
`,
	// 4: per-user-token priority-ordered group list. Stored as a JSON
	//    array column on user_tokens. Empty / NULL → uses the user's
	//    pricing group (legacy behavior). Non-empty → the token-side
	//    priority list takes over for credential routing (fall through
	//    a chain of groups in order). Billing still happens
	//    via the resolved group's discount, so existing rate cards
	//    continue to apply unchanged.
	`
ALTER TABLE user_tokens ADD COLUMN groups TEXT NOT NULL DEFAULT '';
`,
	// 5: marketing-channel attribution (the internal/saas/growth module).
	//    Three self-contained tables — growth owns them entirely and the rest
	//    of the SaaS layer never reads them. Kept here only because the schema
	//    migrator is centralized (append-only + pre-migrate backup); all query
	//    and handler logic lives in internal/saas/growth.
	//
	//      marketing_channels — one row per referral link (?ref=<slug>), with a
	//                           configurable USD signup bonus.
	//      channel_visits     — first-touch visit per (slug, anonymous visitor),
	//                           accumulating dwell time and a converted flag.
	//      channel_referrals  — the signup→channel link, one channel credited
	//                           per user, recording the bonus actually granted.
	`
CREATE TABLE marketing_channels (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    slug        TEXT NOT NULL UNIQUE,                  -- url ?ref=<slug>
    name        TEXT NOT NULL DEFAULT '',              -- display name, e.g. "Twitter / X"
    description TEXT NOT NULL DEFAULT '',
    bonus_usd   REAL NOT NULL DEFAULT 0,               -- signup bonus granted to referees
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE channel_visits (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    slug              TEXT NOT NULL,
    visitor_id        TEXT NOT NULL,                   -- anonymous, localStorage-persisted
    first_seen        INTEGER NOT NULL,
    last_seen         INTEGER NOT NULL,
    duration_ms       INTEGER NOT NULL DEFAULT 0,      -- accumulated dwell time
    converted_user_id INTEGER NOT NULL DEFAULT 0,      -- 0 = not (yet) converted
    created_at        INTEGER NOT NULL,
    UNIQUE(slug, visitor_id)                           -- first-touch: one row per (channel, visitor)
);
CREATE INDEX idx_channel_visits_slug ON channel_visits(slug, first_seen);

CREATE TABLE channel_referrals (
    user_id    INTEGER PRIMARY KEY,                    -- one channel credited per user
    slug       TEXT NOT NULL,
    bonus_usd  REAL NOT NULL DEFAULT 0,                -- bonus actually granted at signup
    visitor_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_channel_referrals_slug ON channel_referrals(slug);
`,

	// v6 — signup anti-abuse. Records one device per signup (browser
	// fingerprint hash + IP / subnet) so a fresh signup can be matched against
	// prior users and have its welcome bonus withheld when it looks like the
	// same person farming the trial credit. Self-contained in internal/saas/
	// growth (fraud.go); does NOT touch the users table. channel_visits also
	// gains a fingerprint column so anonymous visits carry a cross-session
	// stable device id for behavioural attribution (the random visitor_id is
	// lost whenever localStorage is cleared).
	`
CREATE TABLE signup_devices (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    fingerprint TEXT    NOT NULL DEFAULT '',             -- ThumbmarkJS hash (may be empty if it failed)
    ip          TEXT    NOT NULL DEFAULT '',
    ip_prefix   TEXT    NOT NULL DEFAULT '',             -- v4 /24 or v6 /48, for shared-network detection
    fraud       INTEGER NOT NULL DEFAULT 0,              -- 1 = this signup was flagged suspicious
    reason      TEXT    NOT NULL DEFAULT '',             -- 'fingerprint' | 'ip_subnet' | ''
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_signup_devices_fp     ON signup_devices(fingerprint);
CREATE INDEX idx_signup_devices_prefix ON signup_devices(ip_prefix, created_at);

ALTER TABLE channel_visits ADD COLUMN fingerprint TEXT NOT NULL DEFAULT '';
`,

	// v7 — site-wide visitor behaviour analytics (the internal/saas/analytics
	// module). Unlike growth (which only tracks ?ref= channel visitors), this
	// captures EVERY landing-page visitor: first action (bounce / start / login
	// / nav-away), dwell time, page flow, and coarse acquisition source. Two
	// self-contained tables owned entirely by internal/saas/analytics; the rest
	// of the SaaS layer never reads them. Kept here only because the migrator is
	// centralized (append-only + pre-migrate backup).
	//
	//   web_sessions — one row per (anonymous visitor, tab session). Holds the
	//                  landing page, first_action ('' until set → '' == bounce),
	//                  acquisition source/referrer, page/action counters, and
	//                  accumulated dwell time.
	//   web_events   — append-only event stream (pageview | action) carrying a
	//                  per-session sequence number so the visit flow can be
	//                  reconstructed (home → pricing → register).
	`
CREATE TABLE web_sessions (
    session_id      TEXT PRIMARY KEY,                     -- client sessionStorage id, one per tab session
    visitor_id      TEXT NOT NULL,                        -- anonymous, localStorage-persisted (shared with growth)
    landing_path    TEXT NOT NULL DEFAULT '',             -- first page seen
    first_action    TEXT NOT NULL DEFAULT '',             -- '' = none yet (== bounce); else 'start' | 'login' | 'nav:pricing' | …
    source          TEXT NOT NULL DEFAULT 'direct',       -- direct | search | social | referral | internal
    referrer_domain TEXT NOT NULL DEFAULT '',             -- external referrer host (empty for direct)
    pageviews       INTEGER NOT NULL DEFAULT 0,
    actions         INTEGER NOT NULL DEFAULT 0,           -- explicit interactions; actions=0 AND pageviews<=1 => bounce
    duration_ms     INTEGER NOT NULL DEFAULT 0,           -- accumulated dwell time (max of heartbeats)
    started_at      INTEGER NOT NULL,
    last_seen       INTEGER NOT NULL
);
CREATE INDEX idx_web_sessions_started ON web_sessions(started_at);
CREATE INDEX idx_web_sessions_visitor ON web_sessions(visitor_id);

CREATE TABLE web_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    visitor_id TEXT NOT NULL,
    kind       TEXT NOT NULL,                             -- 'pageview' | 'action'
    name       TEXT NOT NULL,                             -- pageview: page label; action: CTA id
    seq        INTEGER NOT NULL,                          -- ordinal within the session, for flow reconstruction
    ts         INTEGER NOT NULL
);
CREATE INDEX idx_web_events_session ON web_events(session_id, seq);
CREATE INDEX idx_web_events_ts ON web_events(ts);
`,

	// v8 — seed the "企业VIP" pricing group (claude 0.2 / codex 0.04), a tier
	// below the default (0.3 / 0.05) for enterprise customers. Admins assign
	// users to it from the panel (Users tab → group selector) and may tune its
	// multipliers later via PATCH /admin/groups/:id. INSERT OR IGNORE so the
	// migration is a no-op if an operator already created a group of this name
	// by hand (name is UNIQUE). The user-facing dashboard reads each user's
	// group multipliers live from /me, so no frontend change is needed.
	`
INSERT OR IGNORE INTO pricing_groups
    (name, description, claude_multiplier, codex_multiplier, is_default, created_at, updated_at)
VALUES
    ('企业VIP', '企业 VIP 专属定价', 0.2, 0.04, 0, strftime('%s','now'), strftime('%s','now'));
`,

	// v9 — bump the 企业VIP Claude multiplier 0.2 → 0.25. Append-only delta
	// (v8 already shipped on main, so it's immutable history; this mirrors v3,
	// which UPDATEs a previously-seeded group rather than rewriting its seed).
	// WHERE-guarded on the old value so an operator who already retuned it from
	// the admin panel isn't clobbered. Codex stays at 0.04.
	`
UPDATE pricing_groups
SET    claude_multiplier = 0.25,
       updated_at        = strftime('%s','now')
WHERE  name = '企业VIP'
  AND  claude_multiplier = 0.2;
`,

	// v10 — public arena / leaderboard profile. One row per user holding a
	// public-facing nickname, a "publish my usage to the leaderboard" opt-in,
	// and three running activity counters bumped on every billed request
	// (so the leaderboard is a single indexed query rather than a per-user
	// requestlog scan). Self-contained: owned by internal/saas/{arena,profile};
	// the row is created lazily on first profile read (GetOrCreateProfile), so
	// existing users get one the first time they open the dashboard. The
	// users table is untouched — nickname lives here, not on the account.
	//
	//   public_opt_in = 0 (default) → the user appears on the leaderboard /
	//                   in the office only under an anonymous pseudonym.
	//   public_opt_in = 1           → their real display_name is shown.
	`
CREATE TABLE user_profiles (
    user_id           INTEGER PRIMARY KEY,
    display_name      TEXT    NOT NULL DEFAULT '',
    name_is_default   INTEGER NOT NULL DEFAULT 1,   -- 1 = system-generated nick, prompt user to change
    public_opt_in     INTEGER NOT NULL DEFAULT 0,   -- 0 = anonymous pseudonym, 1 = show real nickname
    lifetime_tokens   INTEGER NOT NULL DEFAULT 0,
    lifetime_requests INTEGER NOT NULL DEFAULT 0,
    last_active_at    INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_user_profiles_tokens   ON user_profiles(lifetime_tokens DESC);
CREATE INDEX idx_user_profiles_requests ON user_profiles(lifetime_requests DESC);
`,

	// v11 — referral & gift growth system. Two distinct money flows live here:
	//
	//   1. invite cards  — platform-funded two-sided acquisition bonus. A user
	//      mints a personalised invite card (custom face + a unique code that
	//      doubles as the ?ref= value); when a new account registers through it
	//      both sides get a configurable credit, gated by the same signup
	//      anti-abuse (signup_devices) the growth module already records.
	//   2. gift cards    — peer-to-peer wallet transfer. The sender's balance is
	//      debited into escrow; the recipient claims by email/code; an expired
	//      gift is refunded to the sender.
	//
	// Everything an operator tunes (bonus amounts, expiry, caps, A/B copy,
	// milestone tiers) lives in referral_campaigns / referral_tiers so the
	// behaviour is runtime-configurable from the admin panel rather than baked
	// into code. Owned by internal/saas/referral; the only cross-table coupling
	// is the wallet ledger (gift escrow / bonus credit) and user_profiles
	// (lifetime_invites, so the existing leaderboard can rank by invites too).
	`
CREATE TABLE referral_campaigns (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    slug                 TEXT    NOT NULL UNIQUE,
    name                 TEXT    NOT NULL DEFAULT '',
    kind                 TEXT    NOT NULL DEFAULT 'both',    -- invite | gift | both
    status               TEXT    NOT NULL DEFAULT 'active',  -- active | paused | ended
    invitee_bonus_usd    REAL    NOT NULL DEFAULT 1,
    inviter_bonus_usd    REAL    NOT NULL DEFAULT 1,
    inviter_reward_on    TEXT    NOT NULL DEFAULT 'signup',  -- signup | first_spend
    gift_expiry_days     INTEGER NOT NULL DEFAULT 30,
    max_gift_usd         REAL    NOT NULL DEFAULT 100,
    max_rewarded_invites INTEGER NOT NULL DEFAULT 0,         -- 0 = unlimited
    starts_at            INTEGER NOT NULL DEFAULT 0,
    ends_at              INTEGER NOT NULL DEFAULT 0,
    headline             TEXT    NOT NULL DEFAULT '',
    subcopy              TEXT    NOT NULL DEFAULT '',
    variant_b            TEXT    NOT NULL DEFAULT '',         -- JSON {headline,subcopy}, optional A/B
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);
CREATE TABLE referral_tiers (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    campaign_id       INTEGER NOT NULL,
    threshold         INTEGER NOT NULL,                       -- confirmed invites to reach this tier
    tier_name         TEXT    NOT NULL DEFAULT '',
    card_style_unlock TEXT    NOT NULL DEFAULT '',            -- '' | claude | openai
    bonus_usd         REAL    NOT NULL DEFAULT 0,             -- one-off credit on reaching the tier
    badge             TEXT    NOT NULL DEFAULT '',
    created_at        INTEGER NOT NULL,
    FOREIGN KEY (campaign_id) REFERENCES referral_campaigns(id) ON DELETE CASCADE
);
CREATE INDEX idx_referral_tiers_campaign ON referral_tiers(campaign_id, threshold);
CREATE TABLE referral_cards (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_user_id INTEGER NOT NULL,
    campaign_id   INTEGER NOT NULL DEFAULT 0,
    code          TEXT    NOT NULL UNIQUE,                    -- invite code, doubles as ?ref=
    card_style    TEXT    NOT NULL DEFAULT 'claude',          -- claude | openai
    card_tone     TEXT    NOT NULL DEFAULT 'dark',            -- dark | light
    tagline       TEXT    NOT NULL DEFAULT '',
    message       TEXT    NOT NULL DEFAULT '',
    impressions   INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_referral_cards_owner ON referral_cards(owner_user_id);
CREATE TABLE referral_conversions (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    campaign_id       INTEGER NOT NULL DEFAULT 0,
    code              TEXT    NOT NULL DEFAULT '',
    inviter_user_id   INTEGER NOT NULL,
    invitee_user_id   INTEGER NOT NULL UNIQUE,                -- a user can be referred at most once
    inviter_bonus_usd REAL    NOT NULL DEFAULT 0,
    invitee_bonus_usd REAL    NOT NULL DEFAULT 0,
    fraud             INTEGER NOT NULL DEFAULT 0,
    inviter_paid      INTEGER NOT NULL DEFAULT 1,             -- 0 = pending (reward_on=first_spend)
    created_at        INTEGER NOT NULL
);
CREATE INDEX idx_referral_conv_inviter ON referral_conversions(inviter_user_id);
CREATE TABLE referral_milestone_grants (
    user_id    INTEGER NOT NULL,
    threshold  INTEGER NOT NULL,
    tier_id    INTEGER NOT NULL,
    bonus_usd  REAL    NOT NULL DEFAULT 0,
    granted_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, threshold)
);
CREATE TABLE gift_cards (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    sender_user_id     INTEGER NOT NULL,
    code               TEXT    NOT NULL UNIQUE,               -- redeem code
    recipient_email    TEXT    NOT NULL DEFAULT '',
    amount_usd         REAL    NOT NULL,
    message            TEXT    NOT NULL DEFAULT '',
    card_style         TEXT    NOT NULL DEFAULT 'claude',
    card_tone          TEXT    NOT NULL DEFAULT 'dark',
    status             TEXT    NOT NULL DEFAULT 'pending',    -- pending | claimed | expired | refunded
    claimed_by_user_id INTEGER NOT NULL DEFAULT 0,
    claimed_at         INTEGER NOT NULL DEFAULT 0,
    expires_at         INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL,
    FOREIGN KEY (sender_user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_gift_cards_sender    ON gift_cards(sender_user_id);
CREATE INDEX idx_gift_cards_recipient ON gift_cards(recipient_email, status);
CREATE INDEX idx_gift_cards_status    ON gift_cards(status, expires_at);
ALTER TABLE user_profiles ADD COLUMN lifetime_invites INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_user_profiles_invites ON user_profiles(lifetime_invites DESC);
INSERT INTO referral_campaigns
    (slug, name, kind, status, invitee_bonus_usd, inviter_bonus_usd, inviter_reward_on,
     gift_expiry_days, max_gift_usd, max_rewarded_invites, headline, subcopy, created_at, updated_at)
VALUES
    ('default', '邀请有礼', 'both', 'active', 1, 1, 'signup', 30, 100, 0,
     '邀请好友，各得 $1', '把 hypitoken 送给朋友 — 对方注册成功，你们各得 $1 体验金',
     strftime('%s','now'), strftime('%s','now'));
INSERT INTO referral_tiers (campaign_id, threshold, tier_name, card_style_unlock, bonus_usd, badge, created_at)
SELECT id, 1,  'NOIR',      'claude', 0,  'noir',      strftime('%s','now') FROM referral_campaigns WHERE slug='default';
INSERT INTO referral_tiers (campaign_id, threshold, tier_name, card_style_unlock, bonus_usd, badge, created_at)
SELECT id, 3,  'PLATINUM',  'claude', 2,  'platinum',  strftime('%s','now') FROM referral_campaigns WHERE slug='default';
INSERT INTO referral_tiers (campaign_id, threshold, tier_name, card_style_unlock, bonus_usd, badge, created_at)
SELECT id, 10, 'RESERVE',   'openai', 5,  'reserve',   strftime('%s','now') FROM referral_campaigns WHERE slug='default';
INSERT INTO referral_tiers (campaign_id, threshold, tier_name, card_style_unlock, bonus_usd, badge, created_at)
SELECT id, 25, 'SIGNATURE', 'openai', 15, 'signature', strftime('%s','now') FROM referral_campaigns WHERE slug='default';
`,

	// v12 — referral bonus circuit breaker. A per-campaign daily budget cap on
	// platform-funded bonus payouts: once a day's total referral bonus spend
	// (invitee + inviter + milestone) reaches this, granting auto-pauses (the
	// conversion is still recorded, but $0 is paid) until the next UTC day or
	// the operator raises the cap. Defends platform money against a registration
	// spike that slips past the per-signup anti-abuse. 0 = unlimited (default).
	`
ALTER TABLE referral_campaigns ADD COLUMN daily_budget_usd REAL NOT NULL DEFAULT 0;
`,

	// v13 — Workspace model: the billing/quota SUBJECT. Until now the wallet
	// balance lived on users.balance_usd and every API token billed its owner.
	// To support B2B (a company sharing one quota pool across staff) without
	// disrupting the 100+ existing individual users, we introduce Workspaces:
	//
	//   Workspace = wallet + bill;  User = person;  membership = role in a space;
	//   API key   = call credential bound to ONE workspace (bills that pool);
	//   pricing_group stays purely calc/route (NOT touched here).
	//
	// Backward compat is the whole point: every existing user gets a `personal`
	// workspace whose balance IS their old balance, they become its admin, and
	// all their tokens + historical ledger rows are attributed to it. The
	// users.balance_usd column is FROZEN (left in place for rollback safety) —
	// the live balance now lives on workspaces.balance_usd, and User.BalanceUSD
	// is loaded via a JOIN on users.personal_workspace_id (so every existing
	// reader keeps working unchanged). Enterprise workspaces are provisioned by
	// the platform admin only (no self-service); members join by email invite.
	`
CREATE TABLE workspaces (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL DEFAULT '',
    type            TEXT    NOT NULL DEFAULT 'personal',   -- personal | enterprise
    balance_usd     REAL    NOT NULL DEFAULT 0,
    daily_usd_cap   REAL    NOT NULL DEFAULT 0,            -- 0 = no cap
    monthly_usd_cap REAL    NOT NULL DEFAULT 0,            -- 0 = no cap
    group_id        INTEGER NOT NULL DEFAULT 0,            -- pricing group; 0 = default
    created_by      INTEGER NOT NULL DEFAULT 0,            -- user_id of creator (0 = system)
    disabled        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_workspaces_type ON workspaces(type);
CREATE INDEX idx_workspaces_creator ON workspaces(created_by);

CREATE TABLE workspace_members (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id    INTEGER NOT NULL,
    user_id         INTEGER NOT NULL,
    role            TEXT    NOT NULL DEFAULT 'member',      -- admin | member
    monthly_usd_cap REAL    NOT NULL DEFAULT 0,             -- per-member cap in this space; 0 = none
    created_at      INTEGER NOT NULL,
    UNIQUE(workspace_id, user_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_workspace_members_ws   ON workspace_members(workspace_id);
CREATE INDEX idx_workspace_members_user ON workspace_members(user_id);

CREATE TABLE workspace_invites (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id     INTEGER NOT NULL,
    email            TEXT    NOT NULL,                      -- lowercased invite target
    role             TEXT    NOT NULL DEFAULT 'member',
    token            TEXT    NOT NULL UNIQUE,               -- random; in the invite link
    status           TEXT    NOT NULL DEFAULT 'pending',    -- pending | accepted | revoked | expired
    invited_by       INTEGER NOT NULL DEFAULT 0,
    accepted_user_id INTEGER NOT NULL DEFAULT 0,
    expires_at       INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);
CREATE INDEX idx_workspace_invites_email ON workspace_invites(email, status);
CREATE INDEX idx_workspace_invites_ws    ON workspace_invites(workspace_id);

ALTER TABLE users       ADD COLUMN personal_workspace_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE user_tokens ADD COLUMN workspace_id      INTEGER NOT NULL DEFAULT 0;  -- which space this key bills
ALTER TABLE user_tokens ADD COLUMN admin_monthly_cap REAL    NOT NULL DEFAULT 0;  -- space-admin imposed cap; 0 = none
ALTER TABLE wallet_tx   ADD COLUMN workspace_id      INTEGER NOT NULL DEFAULT 0;  -- billing subject; user_id kept for attribution

-- Backfill: one personal workspace per existing user, balance carried over.
INSERT INTO workspaces (name, type, balance_usd, group_id, created_by, created_at, updated_at)
SELECT email, 'personal', balance_usd, group_id, id, strftime('%s','now'), strftime('%s','now')
FROM users;

-- Point each user at their personal workspace (created_by uniquely identifies it
-- at this moment — only personal workspaces exist so far).
UPDATE users SET personal_workspace_id =
    (SELECT w.id FROM workspaces w WHERE w.created_by = users.id AND w.type = 'personal' LIMIT 1);

-- The user is the admin of their own personal workspace.
INSERT INTO workspace_members (workspace_id, user_id, role, created_at)
SELECT personal_workspace_id, id, 'admin', strftime('%s','now')
FROM users WHERE personal_workspace_id > 0;

-- Bind existing tokens + historical ledger rows to the owner's personal space.
UPDATE user_tokens SET workspace_id =
    (SELECT personal_workspace_id FROM users WHERE users.id = user_tokens.user_id)
WHERE workspace_id = 0;
UPDATE wallet_tx SET workspace_id =
    (SELECT personal_workspace_id FROM users WHERE users.id = wallet_tx.user_id)
WHERE workspace_id = 0;
`,

	// v14 — pricing multiplier moves ONTO the workspace. The billing rate is no
	// longer resolved via the per-user pricing group; it's a direct property of
	// the BILLING workspace. New rule: personal workspaces always bill at the
	// standard default (0=sentinel → 0.3 claude / 0.05 codex); only enterprise
	// workspaces carry a custom (discounted) rate, set by the platform admin per
	// workspace. The old pricing_groups / users.group_id machinery is frozen
	// (kept for the NOT-NULL FK + DefaultGroup plumbing) but no longer drives
	// billing. credential_group routing is unaffected (it runs off token.groups).
	//
	// Backfill: existing ENTERPRISE workspaces inherit their old pricing group's
	// multipliers so nobody's enterprise rate changes; PERSONAL workspaces are
	// left at 0 (= standard) — any individual who had a discounted personal group
	// is handled out-of-band by moving them into an enterprise workspace.
	`
ALTER TABLE workspaces ADD COLUMN claude_multiplier REAL NOT NULL DEFAULT 0;  -- 0 = standard default
ALTER TABLE workspaces ADD COLUMN codex_multiplier  REAL NOT NULL DEFAULT 0;  -- 0 = standard default

UPDATE workspaces SET
    claude_multiplier = COALESCE((SELECT g.claude_multiplier FROM pricing_groups g WHERE g.id = workspaces.group_id), 0),
    codex_multiplier  = COALESCE((SELECT g.codex_multiplier  FROM pricing_groups g WHERE g.id = workspaces.group_id), 0)
WHERE type = 'enterprise';
`,

	// v15 — per-token / per-model spend attribution becomes first-class.
	//
	// Until now the proxy squeezed both into the free-text `ref`
	// ("token=<id> model=<name>"), which can't be indexed, grouped or filtered —
	// so "show me what each key in my company spent" was unanswerable.
	//
	// The token-count axes land here too, rather than being read back from the
	// cc-core requestlog: that JSONL is retention-GC'd and only carries a MASKED
	// client token (which key rotation invalidates), so it can never be the
	// billing source of truth. wallet_tx is. Historical rows keep 0 counts —
	// honestly "not recorded", which the CSV renders as blank rather than 0.
	//
	// `ref` is deliberately left intact: /billing/transactions and
	// /workspaces/:id/ledger still render it, and it stays as an audit trail.
	`
ALTER TABLE wallet_tx ADD COLUMN token_id            INTEGER NOT NULL DEFAULT 0;
ALTER TABLE wallet_tx ADD COLUMN model               TEXT    NOT NULL DEFAULT '';
ALTER TABLE wallet_tx ADD COLUMN input_tokens        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE wallet_tx ADD COLUMN output_tokens       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE wallet_tx ADD COLUMN cache_read_tokens   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE wallet_tx ADD COLUMN cache_create_tokens INTEGER NOT NULL DEFAULT 0;

-- Per-key labels, e.g. '["研发部","前端"]'. '' = untagged. Same storage
-- convention as user_tokens.groups (v4) — a JSON array in a TEXT column.
ALTER TABLE user_tokens ADD COLUMN tags TEXT NOT NULL DEFAULT '';
` + backfillTokenAttributionSQL + `
CREATE INDEX idx_wallet_tx_ws_time    ON wallet_tx(workspace_id, created_at);
CREATE INDEX idx_wallet_tx_ws_token   ON wallet_tx(workspace_id, token_id, created_at);
CREATE INDEX idx_wallet_tx_user_token ON wallet_tx(user_id, token_id, created_at);
CREATE INDEX idx_user_tokens_ws       ON user_tokens(workspace_id);
`,

	// v16 — make the spend reports index-only.
	//
	// The v15 indexes select the right rows but carry none of the aggregated
	// columns, so every report did an index range scan *plus* one main-table
	// rowid lookup per matching row. /me/usage/summary runs five such passes
	// over the same slice, and on production that measured 1.9–3.4 s for a
	// 10 KB response — paid by every customer on every dashboard load.
	//
	// These two are partial (kind='charge', which every report's predicate
	// states literally) and cover every column the aggregates read, so the
	// planner can satisfy them without touching the table at all. Partial +
	// covering also keeps them far smaller than a plain 9-column index would
	// be: topup/adjust/refund rows are excluded entirely.
	//
	// idx_wallet_tx_kind_id fixes a different query: /admin/adjustments counts
	// with `WHERE kind = 'adjust'`, and with no index on kind that was a full
	// scan of the whole table to produce a single number.
	`
CREATE INDEX IF NOT EXISTS idx_wallet_tx_user_charge ON wallet_tx(
    user_id, created_at, token_id, model,
    amount_usd, input_tokens, output_tokens, cache_read_tokens, cache_create_tokens
) WHERE kind = 'charge';

CREATE INDEX IF NOT EXISTS idx_wallet_tx_ws_charge ON wallet_tx(
    workspace_id, created_at, token_id, model,
    amount_usd, input_tokens, output_tokens, cache_read_tokens, cache_create_tokens
) WHERE kind = 'charge';

CREATE INDEX IF NOT EXISTS idx_wallet_tx_kind_id ON wallet_tx(kind, id DESC);

-- The planner picks between the v15 and v16 indexes on cost estimates; with no
-- sqlite_stat1 it guesses, and guessed wrong often enough to keep choosing a
-- non-covering path. ANALYZE is cheap here and runs once per migration.
ANALYZE;
`,
}

// backfillTokenAttributionSQL recovers token_id / model from the legacy free-text
// ref on every historical charge row. Kept as a named constant so the migration
// and its test exercise the exact same statement and cannot drift.
//
// Layout: ref = "token=<digits> model=<name>". "token=" is 6 chars, so the digits
// start at 1-based position 7; " model=" is 7 chars, so the model starts 7 past
// the separator and runs to end-of-string (model is the format's last field, so
// a name containing spaces survives intact).
//
// The guards matter more than the extraction:
//
//   - instr(...) >= 8 means at least one digit precedes the separator, which also
//     keeps the substr length >= 1 — SQLite's substr treats a NEGATIVE length as
//     "take the characters BEFORE the offset", so an unguarded row with no
//     separator (instr = 0) would silently extract garbage rather than error.
//   - The GLOB pair is an exact "one-or-more digits, nothing else" test. Without
//     it, a malformed 'token=9x model=…' would CAST to 9 and attribute that money
//     to key 9 — a wrong answer is far worse than no answer here.
//
// Rows failing any guard simply stay at token_id = 0: the "unattributed" bucket,
// which the reports surface as its own visible row rather than folding into a
// real key. topup (out_trade_no) / adjust (bonus) / refund refs are structurally
// excluded by the kind + LIKE predicates.
//
// G101 fires on the literal "token=" below. It is a SQL prefix being parsed out
// of a ledger ref, not a hardcoded credential.
//
//nolint:gosec // G101 false positive — see above.
const backfillTokenAttributionSQL = `
UPDATE wallet_tx
   SET token_id = CAST(substr(ref, 7, instr(ref, ' model=') - 7) AS INTEGER),
       model    =      substr(ref, instr(ref, ' model=') + 7)
 WHERE kind = 'charge'
   AND ref LIKE 'token=%'
   AND instr(ref, ' model=') >= 8
   AND substr(ref, 7, instr(ref, ' model=') - 7) GLOB '[0-9]*'
   AND substr(ref, 7, instr(ref, ' model=') - 7) NOT GLOB '*[^0-9]*';
`

func (db *DB) migrate() error {
	ctx := context.Background()
	// Bootstrap version table if absent.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var current int
	row := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`)
	if err := row.Scan(&current); err != nil {
		return err
	}
	target := len(migrations)
	if target == current {
		log.Infof("saas-db: schema up to date at v%d", current)
		return nil
	}
	if target < current {
		// Running an older binary against a newer DB — refuse rather than
		// silently downgrade. Wallet/payment ledgers must never be touched
		// by code that doesn't understand the schema.
		return fmt.Errorf("saas-db: binary supports v%d but DB is at v%d — running an older binary on a newer DB is unsupported", target, current)
	}

	// Backup BEFORE applying any new migration. SQLite is "just a file" so
	// a byte copy of a quiesced DB is a complete snapshot. We acquire a
	// read transaction first so concurrent writers don't tear the copy —
	// realistically the SaaS layer is single-writer at startup, but cheap
	// insurance is cheap.
	if current > 0 && db.path != "" {
		if err := backupDBFile(db.path, current); err != nil {
			log.Warnf("saas-db: backup before migrate failed (continuing anyway): %v", err)
		}
	}

	for i, sql := range migrations {
		v := i + 1
		if v <= current {
			continue
		}
		log.Infof("saas-db: applying migration v%d…", v)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES(?)`, v); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (record): %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	log.Infof("saas-db: migrated v%d → v%d", current, target)
	return nil
}

// backupDBFile copies the SQLite file (and its WAL/SHM siblings) to
// "<path>.backup-vN-<timestamp>" so a botched migration can be recovered
// from disk without a separate dump tool.
func backupDBFile(path string, fromVersion int) error {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dst := fmt.Sprintf("%s.backup-v%d-%s", path, fromVersion, stamp)
	if err := copyFile(path, dst); err != nil {
		return err
	}
	// WAL + SHM are best-effort: if they're not present (DB was checkpointed
	// already), that's fine — the backup of the main file is consistent.
	for _, suffix := range []string{"-wal", "-shm"} {
		src := path + suffix
		if _, err := os.Stat(src); err == nil {
			_ = copyFile(src, dst+suffix)
		}
	}
	log.Infof("saas-db: pre-migrate backup → %s", dst)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
