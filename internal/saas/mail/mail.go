// Package mail sends transactional email.
//
// Three implementations behind the Mailer interface:
//   - ResendMailer — POSTs https://api.resend.com/emails (a single CDN-edge
//     HTTPS round-trip with a hard timeout). This is the default whenever the
//     configured host points at Resend, because the SMTP path's multi-RTT
//     handshake to AWS SES (Tokyo) is both slow and — without a timeout — prone
//     to hanging the /auth/send-code request, which strands the user mid-signup.
//     It falls back to SMTP if the API call fails.
//   - SMTPMailer — a plain SMTP client (now with dial + I/O deadlines). Used for
//     self-hosted SMTP, and as the Resend fallback.
//   - LogMailer — prints to stderr (used when nothing is configured — the
//     verification code shows up in server logs so dev can copy-paste it).
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// smtpTimeout bounds the whole SMTP exchange (dial + TLS + AUTH + DATA). The
// previous code had no timeout at all, so a flaky HK→Tokyo hop could hang the
// request indefinitely.
const smtpTimeout = 15 * time.Second

// resendAPITimeout bounds a single Resend HTTP API call.
const resendAPITimeout = 12 * time.Second

// resendEndpoint is the transactional-send endpoint.
const resendEndpoint = "https://api.resend.com/emails"

// SMTPConfig is a local copy of SMTPConfig (mirrored to avoid an import
// cycle). Callers in the top-level saas package construct it from saas.Config.
// When Host points at Resend, Password is the Resend API key (re_…), which
// doubles as the HTTP API bearer token.
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
	smtpMailer := &SMTPMailer{cfg: cfg, site: siteName}

	// Resend HTTP API path. Requires the API key (Password) and a real From
	// address — the SMTP username is the literal "resend", not a mailbox, so we
	// can't synthesize a sender from it. Keep SMTP as the fallback.
	if strings.Contains(strings.ToLower(cfg.Host), "resend") &&
		strings.TrimSpace(cfg.Password) != "" && strings.TrimSpace(cfg.From) != "" {
		log.Infof("mailer: using Resend HTTP API (from=%s, smtp fallback enabled)", cfg.From)
		return &ResendMailer{
			apiKey:   cfg.Password,
			from:     fmt.Sprintf("%s <%s>", siteName, cfg.From),
			endpoint: resendEndpoint,
			client:   &http.Client{Timeout: resendAPITimeout},
			fallback: smtpMailer,
		}
	}
	return smtpMailer
}

// ResendMailer sends via the Resend HTTP API, falling back to SMTP on failure.
type ResendMailer struct {
	apiKey   string
	from     string // pre-formatted "Site <addr>"
	endpoint string
	client   *http.Client
	fallback Mailer
}

func (m *ResendMailer) Send(to, subject, body string) error {
	if err := m.sendAPI(to, subject, body); err != nil {
		log.Warnf("mailer: Resend API send to %s failed (%v); falling back to SMTP", to, err)
		if m.fallback != nil {
			return m.fallback.Send(to, subject, body)
		}
		return err
	}
	return nil
}

func (m *ResendMailer) sendAPI(to, subject, body string) error {
	payload, err := json.Marshal(map[string]any{
		"from":    m.from,
		"to":      []string{to},
		"subject": subject,
		"html":    body,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), resendAPITimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var ok struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(respBody, &ok)
		log.Infof("mailer: Resend accepted mail to=%s subject=%q id=%s", to, subject, ok.ID)
		return nil
	}
	// Surface Resend's structured error in logs only — callers map this to a
	// generic user-facing message.
	var apiErr struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(respBody, &apiErr)
	msg := strings.TrimSpace(apiErr.Message)
	if msg == "" {
		msg = strings.TrimSpace(string(respBody))
	}
	return fmt.Errorf("resend api status %d: %s", resp.StatusCode, msg)
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

	if err := m.send(addr, auth, from, to, []byte(msg.String())); err != nil {
		return err
	}
	log.Infof("mailer: SMTP accepted mail to=%s subject=%q", to, subject)
	return nil
}

// send performs the full SMTP exchange with a dial timeout and a connection
// deadline so a stalled HK→SES hop can't hang the request forever. Handles both
// implicit-TLS (465) and plaintext/STARTTLS (587).
func (m *SMTPMailer) send(addr string, auth smtp.Auth, from, to string, body []byte) error {
	dialer := &net.Dialer{Timeout: smtpTimeout}
	var conn net.Conn
	var err error
	if m.cfg.UseTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: m.cfg.Host})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(smtpTimeout))

	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer c.Quit()

	// On a plaintext connection, upgrade to STARTTLS when offered before auth.
	if !m.cfg.UseTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: m.cfg.Host}); err != nil {
				return err
			}
		}
	}
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
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
