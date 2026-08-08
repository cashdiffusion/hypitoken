package growth

import (
	"context"
	"net"
	"strings"
	"time"
)

// FraudConfig is the signup anti-abuse policy. Defaults are applied by
// defaultFraudConfig / ConfigureFraud, so a nil or partial operator config is
// safe.
type FraudConfig struct {
	// Enabled toggles the whole anti-abuse check. When false, RecordSignupDevice
	// still records the device row (for later analysis) but never flags fraud.
	Enabled bool
	// SubnetThreshold is how many distinct prior signups from the same /24 (or
	// /48 for IPv6) within Window trip the soft IP rule. Default 3.
	SubnetThreshold int
	// Window bounds how far back the IP-subnet and email-domain rules look.
	// Default 30 days.
	Window time.Duration
	// RequireFingerprint flags any signup that carries no browser fingerprint.
	// The 2026-08-08 farm hit /api/v2/auth/register directly, so ThumbmarkJS
	// never ran and 93 of 168 signups arrived with an empty fp — against which
	// the fingerprint rule is structurally blind. Treating "no fingerprint" as
	// suspicious costs a real user whose ThumbmarkJS failed to load their
	// welcome credit (registration itself still succeeds), and costs a scripted
	// signup its bonus. Default true.
	RequireFingerprint bool
	// EmailDomainThreshold is how many distinct prior signups sharing one email
	// domain within Window trip the domain-burst rule. Mainstream mailbox
	// providers are exempt (see freeMailDomains) — the rule targets the
	// attacker-owned throwaway domain, which is the signal that actually
	// separated the 2026-08-08 farm from organic traffic (26 signups on one
	// .web.id domain in a day, against a baseline of 1–6 signups total).
	// Default 3.
	EmailDomainThreshold int
	// DisposableDomains extends the built-in throwaway-mailbox blocklist with
	// operator-supplied domains (exact match, or a leading "." for a suffix
	// match such as ".web.id"). Case-insensitive.
	DisposableDomains []string
}

func defaultFraudConfig() FraudConfig {
	return FraudConfig{
		Enabled:              true,
		SubnetThreshold:      3,
		Window:               30 * 24 * time.Hour,
		RequireFingerprint:   true,
		EmailDomainThreshold: 3,
	}
}

// freeMailDomains are the mainstream providers exempt from the domain-burst
// rule. Signups genuinely cluster here (most of our real users are on gmail /
// qq / outlook), so counting them would flag honest traffic while doing nothing
// to an attacker, who controls their own domain and can mint addresses on it
// without limit.
var freeMailDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true,
	"outlook.com": true, "hotmail.com": true, "live.com": true, "msn.com": true,
	"qq.com": true, "foxmail.com": true, "163.com": true, "126.com": true, "yeah.net": true,
	"sina.com": true, "sina.cn": true, "aliyun.com": true, "139.com": true, "189.cn": true,
	"icloud.com": true, "me.com": true, "mac.com": true,
	"yahoo.com": true, "yahoo.co.jp": true,
	"protonmail.com": true, "proton.me": true, "pm.me": true,
	"gmx.com": true, "gmx.de": true, "web.de": true, "mail.com": true,
	"zoho.com": true, "yandex.com": true, "yandex.ru": true,
	"hey.com": true, "fastmail.com": true, "tutanota.com": true, "tuta.io": true,
}

// disposableDomains is the built-in throwaway-mailbox blocklist. A leading "."
// entry matches by suffix. This list is deliberately short and high-confidence:
// the domain-burst rule is what catches the long tail (an attacker's own newly
// registered domain can never be enumerated in advance), and every entry here
// is a service whose entire purpose is single-use addresses.
//
// The five domains that carried the 2026-08-08 farm are included by name; the
// free-subdomain suffixes they were minted under (.web.id resellers, DigitalPlat
// dpdns.org, and friends) are included by suffix, since that farm rotated
// through four sibling domains in one day and would simply mint a fifth.
var disposableDomains = map[string]bool{
	"yopmail.com": true, "mailinator.com": true, "guerrillamail.com": true,
	"10minutemail.com": true, "temp-mail.org": true, "tempmail.com": true,
	"sharklasers.com": true, "grr.la": true, "throwawaymail.com": true,
	"trashmail.com": true, "getnada.com": true, "dispostable.com": true,
	"maildrop.cc": true, "fakemailgenerator.com": true, "mohmal.com": true,
	"emailondeck.com": true, "moakt.com": true, "linshiyouxiang.net": true,
	// Seen carrying the 2026-08-08 invite farm.
	"emalupe.com": true, "web-library.net": true, "lanvos.com": true,
	"zendyhost.web.id": true, "brihost.web.id": true, "zendyxidk.web.id": true,
	"brillianweb.dpdns.org": true,
}

var disposableSuffixes = []string{
	".dpdns.org",   // DigitalPlat free domains
	".web.id",      // free / near-free .id resellers, the farm's main vehicle
	".is-a.dev",    // free dev subdomains
	".us.kg",       // free subdomain registrar
	".sbs",         // bulk-registered spam TLD
	".cfd",         //
	".tempmail.uk", //
}

// ConfigureFraud is in growth.go; defaults for the new fields are applied there
// alongside the existing ones.

// ipPrefix collapses an IP to the network block used for shared-network
// detection: /24 for IPv4, /48 for IPv6. Returns "" for an unparseable or
// empty address (so the caller skips the IP rule rather than matching ""). The
// prefix is stored as the masked IP string, e.g. "203.0.113.0" or
// "2001:db8::".
func ipPrefix(raw string) string {
	ip := net.ParseIP(raw)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return ip.Mask(net.CIDRMask(48, 128)).String()
}

// emailDomain lowercases and extracts the part after the last "@". Returns ""
// for an address with no "@" so callers skip the domain rules rather than
// matching every malformed row against each other.
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[at+1:]))
}

// isDisposable reports whether a domain is a known throwaway-mailbox provider,
// by exact match against the built-in and operator lists or by suffix.
func (s *Service) isDisposable(domain string) bool {
	if domain == "" {
		return false
	}
	if disposableDomains[domain] {
		return true
	}
	for _, suf := range disposableSuffixes {
		if strings.HasSuffix(domain, suf) {
			return true
		}
	}
	for _, entry := range s.fraud.DisposableDomains {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, ".") {
			if strings.HasSuffix(domain, entry) {
				return true
			}
		} else if domain == entry {
			return true
		}
	}
	return false
}

// RecordSignupDevice records one device row per signup and decides whether the
// signup looks like abuse of the welcome bonus. It is called once per
// registration, BEFORE any bonus is credited.
//
// Five rules, checked in order — strongest and cheapest signal first, so the
// recorded reason names the most defensible ground for withholding the bonus:
//
//  1. disposable_email — the address is on a throwaway-mailbox provider. No
//     legitimate customer needs one to hold an account with a wallet.
//  2. fingerprint — the same browser fingerprint already belongs to a different
//     user. Near-certain repeat.
//  3. no_fingerprint — no fingerprint at all (RequireFingerprint). A browser
//     that loaded the register page sends one; a script does not.
//  4. ip_subnet — at least SubnetThreshold distinct prior users signed up from
//     the same /24 (or /48) within Window. Soft signal; false-positives on
//     shared NAT / campus networks are expected and acceptable.
//  5. email_domain — at least EmailDomainThreshold distinct prior users share
//     this (non-mainstream) email domain within Window.
//
// The device row is ALWAYS inserted (even when fraud is off or nothing matched)
// so future signups have history to match against. Any DB error is returned for
// the caller to log but must never block registration — on error the caller
// treats the signup as clean.
func (s *Service) RecordSignupDevice(ctx context.Context, userID int64, fp, ip, email string) (fraud bool, reason string, err error) {
	fp = sanitizeVisitorID(fp)
	prefix := ipPrefix(ip)
	domain := emailDomain(email)

	if s.fraud.Enabled {
		switch {
		case s.isDisposable(domain):
			fraud, reason = true, "disposable_email"
		case fp != "" && s.fingerprintSeen(ctx, fp, userID):
			fraud, reason = true, "fingerprint"
		case fp == "" && s.fraud.RequireFingerprint:
			fraud, reason = true, "no_fingerprint"
		case prefix != "" && s.subnetBurst(ctx, prefix, userID):
			fraud, reason = true, "ip_subnet"
		case domain != "" && !freeMailDomains[domain] && s.domainBurst(ctx, domain, userID):
			fraud, reason = true, "email_domain"
		}
	}

	fraudInt := 0
	if fraud {
		fraudInt = 1
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO signup_devices (user_id, fingerprint, ip, ip_prefix, email_domain, fraud, reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, fp, ip, prefix, domain, fraudInt, reason, time.Now().Unix())
	return fraud, reason, err
}

// fingerprintSeen reports whether this browser fingerprint already belongs to a
// different user.
func (s *Service) fingerprintSeen(ctx context.Context, fp string, userID int64) bool {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM signup_devices WHERE fingerprint = ? AND user_id <> ? LIMIT 1`,
		fp, userID).Scan(&exists)
	return err == nil
}

// subnetBurst reports whether this /24 (or /48) has produced at least
// SubnetThreshold distinct other signups within the window.
func (s *Service) subnetBurst(ctx context.Context, prefix string, userID int64) bool {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT user_id) FROM signup_devices WHERE ip_prefix = ? AND user_id <> ? AND created_at >= ?`,
		prefix, userID, s.windowStart()).Scan(&n)
	return err == nil && n >= s.fraud.SubnetThreshold
}

// domainBurst reports whether this email domain has produced at least
// EmailDomainThreshold distinct other signups within the window.
func (s *Service) domainBurst(ctx context.Context, domain string, userID int64) bool {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT user_id) FROM signup_devices WHERE email_domain = ? AND user_id <> ? AND created_at >= ?`,
		domain, userID, s.windowStart()).Scan(&n)
	return err == nil && n >= s.fraud.EmailDomainThreshold
}

func (s *Service) windowStart() int64 {
	return time.Now().Add(-s.fraud.Window).Unix()
}
