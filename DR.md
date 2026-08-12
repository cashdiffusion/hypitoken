# Disaster Recovery — hypitoken

If the server is lost beyond recovery, you can rebuild hypitoken from **the
project code + the newest object in the `apibackup` bucket**. User balances,
wallet ledger, payments, sessions, and upstream credentials all survive.

## What gets backed up (daily, encrypted, off-host)

A daily systemd timer runs `hypitoken backup --config <cfg>`, which uploads
`apibackup/hypitoken/YYYY-MM-DD.tar.gz.enc` (X25519 sealed; rolling 7 days).
Contents:

- `saas.db` — users (balance_usd), wallet_tx ledger, alipay_orders, user_tokens, … **(money)**
- `saas.db.jwt_secret` — session signing key (lose it → all logins invalidated)
- `shop.db` — storefront orders + card secrets (if shop enabled)
- `tokens.json` — client API tokens
- `auths/` — upstream OAuth/API credentials (refresh_tokens)
- `config.yaml`
- `external/` — payment secrets referenced via `@/path` (Stripe/Z-Pay keys, …)
  plus `external/MANIFEST.json` mapping each back to its original absolute path

## Prerequisites for recovery (kept OFFLINE, never on the server)

1. The **age/X25519 private key** (the `private` half from `hypitoken backup keygen`).
2. The **restore S3 key** — a read-only (GetObject + ListBucket) Bitiful key,
   separate from the server's write-only key.

## Recovery steps

```sh
# 1. Install the binary (don't start the service yet).
curl -fsSL https://gh-proxy.com/raw.githubusercontent.com/hypit-ai/hypitoken/main/install.sh \
  | bash -s -- --version vX.Y.Z --force
#    (decline / ignore the service prompt for now)

# 2. Recreate the config dir.
mkdir -p /root/.config/hypitoken

# 3. Restore the newest backup. S3 read creds + private key are passed on the
#    CLI so they never land in a persisted config.
hypitoken restore \
  --config /root/.config/hypitoken/config.yaml \   # only needs backup.s3.{endpoint,region,bucket,prefix}
  --date latest \
  --identity /path/to/offline/age-private-key \
  --s3-access-key-id <RESTORE_KEY_ID> \
  --s3-secret-key <RESTORE_KEY_SECRET> \
  --dest /root/.config/hypitoken/
#    Extracts saas.db, saas.db.jwt_secret, shop.db, tokens.json, auths/,
#    config.yaml, external/ into the config dir.

# 4. Put external payment secrets back where config.yaml expects them.
cat /root/.config/hypitoken/external/MANIFEST.json   # name → original abs path
#    Copy each external/<name> to its mapped path (e.g. /etc/hypitoken/...), mode 0600.

# 5. Switch config back to the WRITE-ONLY server S3 key (+ recipient_pubkey) so
#    the resumed service can keep making backups but can't read history.

# 6. Start it.
sudo systemctl enable --now hypitoken.service hypitoken-backup.timer
```

## Verify

- `systemctl status hypitoken` — active.
- Admin panel shows correct **wallet balances** (proves saas.db restored).
- A user can **log in** (proves jwt_secret restored).
- Upstream OAuth credentials **refresh** without re-auth (proves auths/ restored).
- `systemctl list-timers | grep hypitoken-backup` — next run scheduled.

## After recovery

Rotate every credential that was briefly on the recovered host (the restore S3
key, and per security policy the upstream OAuth/payment creds).

## Operational notes

- Generate the keypair once: `hypitoken backup keygen`. Public → config
  `backup.recipient_pubkey`; private → offline. **Losing the private key makes
  all backups unrecoverable** — store it in ≥2 safe places.
- Manual backup now: `hypitoken backup --config <cfg>`.
- List what's in the bucket: it's `apibackup/hypitoken/*.tar.gz.enc`, one per day.
