// Package mail sends transactional email. SMTPMailer talks to a real SMTP
// server; LogMailer just prints to stderr (used when SMTP isn't configured —
// the verification code shows up in server logs so dev can copy-paste it).
package mail

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"

	log "github.com/sirupsen/logrus"
)

// SMTPConfig is a local copy of SMTPConfig (mirrored to avoid an import
// cycle). Callers in the top-level saas package construct it from saas.Config.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	UseTLS   bool
}

type Mailer interface {
	Send(to, subject, body string) error
}

func New(cfg SMTPConfig, siteName string) Mailer {
	if strings.TrimSpace(cfg.Host) == "" {
		log.Warn("SMTP not configured; mailer running in log-only mode (verification codes will appear in server logs)")
		return &LogMailer{site: siteName}
	}
	return &SMTPMailer{cfg: cfg, site: siteName}
}

type LogMailer struct {
	site string
}

func (m *LogMailer) Send(to, subject, body string) error {
	log.Infof("[mail-stub site=%s] to=%s subject=%q\n%s", m.site, to, subject, body)
	return nil
}

type SMTPMailer struct {
	cfg  SMTPConfig //nolint:revive
	site string
}

func (m *SMTPMailer) Send(to, subject, body string) error {
	from := m.cfg.From
	if from == "" {
		from = m.cfg.Username
	}
	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)

	headers := map[string]string{
		"From":         fmt.Sprintf("%s <%s>", m.site, from),
		"To":           to,
		"Subject":      subject,
		"MIME-Version": "1.0",
		"Content-Type": "text/html; charset=UTF-8",
	}
	var msg strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&msg, "%s: %s\r\n", k, v)
	}
	msg.WriteString("\r\n")
	msg.WriteString(body)

	if m.cfg.UseTLS {
		return m.sendTLS(addr, auth, from, []string{to}, []byte(msg.String()))
	}
	return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg.String()))
}

func (m *SMTPMailer) sendTLS(addr string, auth smtp.Auth, from string, to []string, body []byte) error {
	tlsCfg := &tls.Config{ServerName: m.cfg.Host}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return err
	}
	defer c.Quit()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return w.Close()
}

// VerificationEmail builds an HTML body for the email verification code.
func VerificationEmail(siteName, code string) (string, string) {
	subject := fmt.Sprintf("[%s] Email verification code", siteName)
	body := fmt.Sprintf(`<div style="font-family:system-ui;padding:24px;">
<h2>%s — Email verification</h2>
<p>Your verification code is:</p>
<div style="font-size:32px;font-weight:700;letter-spacing:6px;padding:16px 24px;background:#f4f4f5;border-radius:8px;display:inline-block;">%s</div>
<p style="color:#71717a;margin-top:16px;">The code expires in 10 minutes.</p>
</div>`, siteName, code)
	return subject, body
}

// ResetEmail builds an HTML body for the password reset code.
func ResetEmail(siteName, code string) (string, string) {
	subject := fmt.Sprintf("[%s] Password reset code", siteName)
	body := fmt.Sprintf(`<div style="font-family:system-ui;padding:24px;">
<h2>%s — Password reset</h2>
<p>Your password reset code is:</p>
<div style="font-size:32px;font-weight:700;letter-spacing:6px;padding:16px 24px;background:#f4f4f5;border-radius:8px;display:inline-block;">%s</div>
<p style="color:#71717a;margin-top:16px;">The code expires in 10 minutes.</p>
</div>`, siteName, code)
	return subject, body
}
