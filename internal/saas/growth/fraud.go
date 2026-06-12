package growth

import (
	"context"
	"net"
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
	// Window bounds how far back the IP-subnet rule looks. Default 30 days.
	Window time.Duration
}

func defaultFraudConfig() FraudConfig {
	return FraudConfig{Enabled: true, SubnetThreshold: 3, Window: 30 * 24 * time.Hour}
}

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

// RecordSignupDevice records one device row per signup and decides whether the
// signup looks like abuse of the welcome bonus. It is called once per
// registration, BEFORE any bonus is credited.
//
// Two rules, checked in order (fingerprint first because it is the strong,
// device-level signal; IP subnet second because it is broader and risks
// false-positives on shared NAT / campus networks):
//
//  1. fingerprint — the same browser fingerprint already belongs to a different
//     user. Near-certain repeat; reason "fingerprint".
//  2. ip_subnet — at least SubnetThreshold distinct prior users signed up from
//     the same /24 (or /48) within Window. Soft signal; reason "ip_subnet".
//
// The device row is ALWAYS inserted (even when fraud is off or nothing matched)
// so future signups have history to match against. Any DB error is returned for
// the caller to log but must never block registration — on error the caller
// treats the signup as clean.
func (s *Service) RecordSignupDevice(ctx context.Context, userID int64, fp, ip string) (fraud bool, reason string, err error) {
	fp = sanitizeVisitorID(fp)
	prefix := ipPrefix(ip)

	if s.fraud.Enabled {
		if fp != "" {
			var exists int
			if e := s.db.QueryRowContext(ctx,
				`SELECT 1 FROM signup_devices WHERE fingerprint = ? AND user_id <> ? LIMIT 1`,
				fp, userID).Scan(&exists); e == nil {
				fraud, reason = true, "fingerprint"
			}
		}
		if !fraud && prefix != "" {
			since := time.Now().Add(-s.fraud.Window).Unix()
			var n int
			if e := s.db.QueryRowContext(ctx,
				`SELECT COUNT(DISTINCT user_id) FROM signup_devices WHERE ip_prefix = ? AND user_id <> ? AND created_at >= ?`,
				prefix, userID, since).Scan(&n); e == nil && n >= s.fraud.SubnetThreshold {
				fraud, reason = true, "ip_subnet"
			}
		}
	}

	fraudInt := 0
	if fraud {
		fraudInt = 1
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO signup_devices (user_id, fingerprint, ip, ip_prefix, fraud, reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, fp, ip, prefix, fraudInt, reason, time.Now().Unix())
	return fraud, reason, err
}
