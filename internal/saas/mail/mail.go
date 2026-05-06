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

// VerificationEmail builds the subject + HTML body for the email-verification
// code that the user types during sign-up. Wraps the polished email shell
// in templates.go so layout/branding stays consistent with the reset flow.
func VerificationEmail(siteName, code string) (string, string) {
	subject := fmt.Sprintf("Your %s verification code: %s", siteName, code)
	body := renderShell(
		siteName,
		fmt.Sprintf("Your verification code is %s. It expires in 10 minutes.", code),
		"Confirm your email address",
		"Welcome aboard! To finish creating your account, enter the 6-digit code below in the verification screen.",
		code,
		"This code expires in 10 minutes. For your security, never share it with anyone.",
		"Verify Email",
	)
	return subject, body
}

// ResetEmail builds the subject + HTML body for the password-reset code that
// the user types on the forgot-password screen.
func ResetEmail(siteName, code string) (string, string) {
	subject := fmt.Sprintf("%s password reset — code: %s", siteName, code)
	body := renderShell(
		siteName,
		fmt.Sprintf("Your password reset code is %s. It expires in 10 minutes.", code),
		"Reset your password",
		"We received a request to reset the password on your account. Enter the 6-digit code below to choose a new one.",
		code,
		"This code expires in 10 minutes. If you didn't request a reset, you can ignore this email — your password will stay the same.",
		"Reset Password",
	)
	return subject, body
}
