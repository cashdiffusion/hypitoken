package mail

import (
	"fmt"
	"strings"
)

// Brand styling pulled out so VerificationEmail and ResetEmail render the
// same shell. Email clients (especially Gmail / Outlook) ignore <style> blocks
// and class selectors, so every visual rule is inline. Layout uses tables
// because Outlook still ships an MS-Word-derived HTML renderer.

// HTML-safe brand colors. The accent is the same emerald the SaaS dashboard
// uses for "operational" / primary CTAs, kept dark enough to read on white.
const (
	colorAccent     = "#10b981" // emerald-500
	colorAccentDark = "#047857" // emerald-700
	colorBg         = "#f6f8fa"
	colorCard       = "#ffffff"
	colorBorder     = "#e4e6ea"
	colorText       = "#0f172a"
	colorMuted      = "#64748b"
	colorCodeBg     = "#0f172a"
	colorCodeText   = "#f8fafc"
)

// renderShell wraps content in the standard responsive email layout.
//
// preheader is the hidden 60-char snippet email clients show after the
// subject in the inbox list. Always keep it short and informational.
func renderShell(siteName, preheader, heading, intro, code, expiryNote, ctaText string) string {
	year := "2026"
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta name="color-scheme" content="light only">
<meta name="supported-color-schemes" content="light only">
<title>%[2]s</title>
</head>
<body style="margin:0;padding:0;background-color:%[3]s;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:%[4]s;-webkit-font-smoothing:antialiased;">
<!-- preheader: shown next to subject in inbox previews; hidden in body -->
<div style="display:none;max-height:0;overflow:hidden;mso-hide:all;font-size:1px;line-height:1px;color:%[3]s;">%[5]s</div>

<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:%[3]s;padding:32px 16px;">
  <tr>
    <td align="center">
      <table role="presentation" width="560" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;width:100%%;background-color:%[6]s;border:1px solid %[7]s;border-radius:14px;overflow:hidden;">
        <!-- Brand band -->
        <tr>
          <td style="background:linear-gradient(135deg,%[8]s 0%%,%[9]s 100%%);padding:28px 32px;color:#ffffff;">
            <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0">
              <tr>
                <td style="font-family:Georgia,'Times New Roman',serif;font-size:22px;font-weight:600;letter-spacing:-0.01em;color:#ffffff;">%[1]s</td>
                <td align="right" style="font-size:11px;font-weight:500;letter-spacing:0.12em;text-transform:uppercase;color:rgba(255,255,255,0.78);">%[10]s</td>
              </tr>
            </table>
          </td>
        </tr>

        <!-- Body -->
        <tr>
          <td style="padding:36px 32px 8px 32px;">
            <h1 style="margin:0 0 12px 0;font-size:22px;font-weight:600;letter-spacing:-0.01em;color:%[4]s;">%[2]s</h1>
            <p style="margin:0;font-size:15px;line-height:1.6;color:%[11]s;">%[12]s</p>
          </td>
        </tr>

        <!-- Code -->
        <tr>
          <td style="padding:24px 32px 8px 32px;">
            <div style="background-color:%[13]s;border-radius:10px;padding:24px 16px;text-align:center;font-family:'SF Mono','Menlo','Monaco','Consolas',monospace;font-size:34px;font-weight:600;letter-spacing:0.42em;color:%[14]s;">%[15]s</div>
          </td>
        </tr>

        <!-- Expiry note -->
        <tr>
          <td style="padding:8px 32px 32px 32px;">
            <p style="margin:0;font-size:13px;color:%[11]s;line-height:1.6;">%[16]s</p>
          </td>
        </tr>

        <!-- Divider -->
        <tr><td style="padding:0 32px;"><div style="border-top:1px solid %[7]s;"></div></td></tr>

        <!-- Footer -->
        <tr>
          <td style="padding:20px 32px 28px 32px;font-size:12px;line-height:1.6;color:%[11]s;">
            <p style="margin:0 0 6px 0;">If you didn't request this email, you can safely ignore it — no action will be taken on your account.</p>
            <p style="margin:0;">© %[17]s %[1]s · Sent automatically, please do not reply.</p>
          </td>
        </tr>
      </table>
    </td>
  </tr>
</table>
</body>
</html>`,
		htmlEsc(siteName),   // 1
		htmlEsc(heading),    // 2
		colorBg,             // 3
		colorText,           // 4
		htmlEsc(preheader),  // 5
		colorCard,           // 6
		colorBorder,         // 7
		colorAccent,         // 8
		colorAccentDark,     // 9
		htmlEsc(ctaText),    // 10 — top-right pill text
		colorMuted,          // 11
		htmlEsc(intro),      // 12
		colorCodeBg,         // 13
		colorCodeText,       // 14
		htmlEsc(code),       // 15
		htmlEsc(expiryNote), // 16
		year,                // 17
	)
}

// htmlEsc is a minimal HTML escaper for inline injection. The values we
// inject are all server-controlled (codes, site name, fixed strings), but
// escape anyway so a future caller can't smuggle markup.
func htmlEsc(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}
