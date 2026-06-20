package config

import "strings"

// BackupConfig configures the daily off-host disaster-recovery backup (see
// the `backup` subcommand). The archive is encrypted to RecipientPubKey
// before upload, so the matching private key (kept OFFLINE by the operator)
// is required to restore — a server or bucket compromise can't read it.
type BackupConfig struct {
	Enabled bool `yaml:"enabled"`

	// RetentionDays is the rolling window; older dated objects are pruned
	// after each successful upload. Default 7.
	RetentionDays int `yaml:"retention_days,omitempty"`

	// RecipientPubKey is the base64 X25519 PUBLIC key the backup is sealed
	// to. Generate a keypair with `<binary> backup keygen`; put the public
	// key here and store the private key offline.
	RecipientPubKey string `yaml:"recipient_pubkey"`

	S3 BackupS3 `yaml:"s3"`
}

// BackupS3 is the S3-compatible bucket target (Bitiful: s3.bitiful.net).
// AccessKeyID / SecretAccessKey support the "@/path" file-reference syntax
// (resolved at command time via loadKeyFile), so secrets stay out of the
// committed YAML.
type BackupS3 struct {
	Endpoint        string `yaml:"endpoint,omitempty"`
	Region          string `yaml:"region,omitempty"`
	Bucket          string `yaml:"bucket"`
	Prefix          string `yaml:"prefix,omitempty"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
}

// applyDefaults fills in safe defaults. Called from the package applyDefaults.
func (b *BackupConfig) applyDefaults() {
	if b.RetentionDays == 0 {
		b.RetentionDays = 7
	}
	if strings.TrimSpace(b.S3.Endpoint) == "" {
		b.S3.Endpoint = "s3.bitiful.net"
	}
}
