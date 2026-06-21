package referral

import (
	"fmt"
	"html"
	"strings"
)

// giftEmailHTML renders a branded gift-card delivery email. Self-contained
// inline styles (no external assets) so it renders in every mail client.
func giftEmailHTML(site, intro, message, redeem, redeemURL, action string) string {
	var msgBlock string
	if strings.TrimSpace(message) != "" {
		msgBlock = `<p style="margin:0 0 18px;padding:14px 16px;background:#0f1518;border-left:3px solid #34d399;border-radius:8px;color:#cbd5d1;font-style:italic;">` +
			html.EscapeString(message) + `</p>`
	}
	return fmt.Sprintf(`<!doctype html><html><body style="margin:0;background:#0b0f10;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
<div style="max-width:520px;margin:0 auto;padding:40px 24px;">
  <div style="text-align:center;margin-bottom:28px;">
    <span style="font-size:13px;letter-spacing:.22em;text-transform:uppercase;color:#34d399;font-weight:600;">%s</span>
  </div>
  <div style="background:#11181b;border:1px solid #1f2c2e;border-radius:18px;padding:32px 28px;">
    <p style="margin:0 0 18px;color:#e7efed;font-size:16px;line-height:1.7;">%s</p>
    %s
    <div style="margin:24px 0;padding:18px;border:1px dashed #2c3c3e;border-radius:12px;text-align:center;background:#0d1416;">
      <div style="font-size:12px;color:#8aa;letter-spacing:.12em;text-transform:uppercase;margin-bottom:8px;">兑换码 / Redeem Code</div>
      <div style="font-family:'SF Mono',ui-monospace,monospace;font-size:22px;font-weight:700;color:#34d399;letter-spacing:.08em;">%s</div>
    </div>
    <div style="text-align:center;margin-top:26px;">
      <a href="%s" style="display:inline-block;background:#34d399;color:#08110d;text-decoration:none;font-weight:700;padding:13px 30px;border-radius:10px;font-size:15px;">%s</a>
    </div>
  </div>
  <p style="margin:22px 0 0;text-align:center;color:#5b6b6b;font-size:12px;line-height:1.6;">
    若按钮无法点击，请复制链接：<br><span style="color:#7e8e8e;">%s</span>
  </p>
</div></body></html>`,
		html.EscapeString(site), intro, msgBlock, html.EscapeString(redeem),
		html.EscapeString(redeemURL), html.EscapeString(action), html.EscapeString(redeemURL))
}

// giftEmailText is the plain-text fallback.
func giftEmailText(site, intro, message, redeem, redeemURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n%s\n\n", site, intro)
	if strings.TrimSpace(message) != "" {
		fmt.Fprintf(&b, "留言: %s\n\n", message)
	}
	fmt.Fprintf(&b, "兑换码: %s\n\n领取链接: %s\n", redeem, redeemURL)
	return b.String()
}
