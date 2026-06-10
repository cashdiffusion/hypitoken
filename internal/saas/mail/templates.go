package mail

import (
	"fmt"
	"strings"
)

// Verification / reset emails are deliberately plain and transactional.
//
// A previous richly-branded template (gradient brand band, hidden preheader,
// big dark code block, "Sent automatically, please do not reply" footer) was
// silently filtered out of Gmail inboxes even though Resend reported it
// Delivered — Gmail treats marketing-shaped OTP mail as promotional and buries
// it. A minimal, text-forward layout from the SAME sender lands in the inbox.
// Keep this simple; resist re-adding decoration, hidden text, or "do not
// reply" wording.

// renderShell builds the HTML body: brand name, one line of context, the code,
// an expiry note, and a short neutral footer.
func renderShell(siteName, intro, code, expiryNote string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin:0;padding:0;background-color:#ffffff;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1a1a1a;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0">
  <tr>
    <td style="padding:24px;">
      <p style="margin:0 0 20px 0;font-size:16px;font-weight:600;">%[1]s</p>
      <p style="margin:0 0 16px 0;font-size:15px;line-height:1.6;">%[2]s</p>
      <p style="margin:0 0 16px 0;font-size:28px;font-weight:700;letter-spacing:0.18em;font-family:'SF Mono',Menlo,Consolas,monospace;">%[3]s</p>
      <p style="margin:0 0 20px 0;font-size:14px;line-height:1.6;color:#555555;">%[4]s</p>
      <p style="margin:0;font-size:13px;line-height:1.6;color:#888888;">If you didn't request this email, you can safely ignore it.</p>
    </td>
  </tr>
</table>
</body>
</html>`,
		htmlEsc(siteName),   // 1
		htmlEsc(intro),      // 2
		htmlEsc(code),       // 3
		htmlEsc(expiryNote), // 4
	)
}

// renderText is the plain-text alternative sent alongside the HTML part.
// Well-behaved OTP mail ships both; it also improves deliverability.
func renderText(siteName, intro, code, expiryNote string) string {
	return fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s\n\nIf you didn't request this email, you can safely ignore it.\n",
		siteName, intro, code, expiryNote)
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
