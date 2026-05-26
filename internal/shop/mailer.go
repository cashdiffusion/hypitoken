package shop

import (
	"fmt"
	"html"
	"strings"

	"github.com/wjsoj/CPA-Claude/internal/saas/mail"
)

// NewMailer constructs a mailer from the shop's SMTP config. Reuses the
// SaaS package's Mailer interface so the implementation tail (SMTP +
// LogMailer fallback) is shared. Pointing the host at smtp.resend.com
// effectively makes this a Resend-backed setup.
func NewMailer(cfg SMTPConfig, siteName string) mail.Mailer {
	return mail.New(mail.SMTPConfig{
		Host:     cfg.Host,
		Port:     cfg.Port,
		Username: cfg.Username,
		Password: cfg.Password,
		From:     cfg.From,
		UseTLS:   cfg.UseTLS,
	}, siteName)
}

// OrderEmail builds the (subject, html) pair for an order-delivery email.
// status determines the tone:
//
//   - "paid": fulfilment text included, friendly thank-you
//   - "await_manual": payment received but operator must dispatch by hand;
//     no fulfilment shown
//
// The query link is built by the caller (so the shop doesn't need to know
// about ReturnURLPrefix here).
func OrderEmail(siteName, productName, status, fulfilment, queryLink string) (string, string) {
	var subject, headline, intro string
	switch status {
	case OrderPaid:
		subject = fmt.Sprintf("%s — 订单已发货", siteName)
		headline = "已收款，下面是你的卡密 / 兑换信息"
		intro = "感谢购买。"
	case OrderAwaitManual:
		subject = fmt.Sprintf("%s — 订单已支付，正在人工处理", siteName)
		headline = "已收到付款，库存暂时不足"
		intro = "我们已经在尽快补货并人工发货；如果 24 小时内仍未收到，请通过订单查询页联系我们。"
	default:
		subject = fmt.Sprintf("%s — 订单状态变更", siteName)
		headline = "订单状态已更新"
		intro = "你可以通过下方链接查看订单详情。"
	}
	return subject, renderOrderHTML(siteName, productName, headline, intro, fulfilment, queryLink)
}

// renderOrderHTML produces the inline-styled HTML body. Email clients vary
// wildly on CSS support, so all styling is inline and structure uses
// tables to satisfy Outlook's word-renderer.
func renderOrderHTML(siteName, productName, headline, intro, fulfilment, queryLink string) string {
	const (
		colorAccent = "#10b981"
		colorBg     = "#f6f8fa"
		colorCard   = "#ffffff"
		colorBorder = "#e4e6ea"
		colorText   = "#0f172a"
		colorMuted  = "#64748b"
		colorCodeBg = "#0f172a"
		colorCodeFg = "#f8fafc"
	)

	fulfilmentBlock := ""
	if strings.TrimSpace(fulfilment) != "" {
		// Render as a fixed-width preformatted block so multi-line card
		// secrets don't lose their structure.
		fulfilmentBlock = fmt.Sprintf(`
        <tr>
          <td style="padding:20px 32px 4px 32px;">
            <div style="font-size:13px;color:%[1]s;text-transform:uppercase;letter-spacing:0.08em;">兑换信息</div>
          </td>
        </tr>
        <tr>
          <td style="padding:8px 32px 24px 32px;">
            <pre style="margin:0;padding:18px 20px;background-color:%[2]s;color:%[3]s;border-radius:10px;font-family:'SFMono-Regular',Consolas,Menlo,monospace;font-size:13px;line-height:1.6;white-space:pre-wrap;word-break:break-all;">%[4]s</pre>
          </td>
        </tr>`,
			colorMuted, colorCodeBg, colorCodeFg, html.EscapeString(fulfilment))
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%[1]s</title>
</head>
<body style="margin:0;padding:0;background-color:%[2]s;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;color:%[3]s;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:%[2]s;padding:32px 16px;">
  <tr>
    <td align="center">
      <table role="presentation" width="560" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;width:100%%;background-color:%[4]s;border:1px solid %[5]s;border-radius:14px;overflow:hidden;">
        <tr>
          <td style="padding:24px 32px;background-color:%[6]s;color:#ffffff;">
            <div style="font-size:18px;font-weight:600;">%[1]s</div>
          </td>
        </tr>
        <tr>
          <td style="padding:32px 32px 8px 32px;">
            <div style="font-size:22px;font-weight:700;line-height:1.3;">%[7]s</div>
            <div style="margin-top:8px;color:%[8]s;font-size:14px;">商品：%[9]s</div>
          </td>
        </tr>
        <tr>
          <td style="padding:8px 32px 16px 32px;font-size:15px;line-height:1.6;">
            %[10]s
          </td>
        </tr>
%[11]s
        <tr>
          <td style="padding:8px 32px 28px 32px;font-size:13px;color:%[8]s;line-height:1.6;">
            订单查询：<a href="%[12]s" style="color:%[6]s;">%[12]s</a><br>
            打开链接后输入下单时设置的「查询密码」即可查看完整订单。
          </td>
        </tr>
      </table>
    </td>
  </tr>
</table>
</body>
</html>`,
		html.EscapeString(siteName), // 1
		colorBg,                     // 2
		colorText,                   // 3
		colorCard,                   // 4
		colorBorder,                 // 5
		colorAccent,                 // 6
		html.EscapeString(headline), // 7
		colorMuted,                  // 8
		html.EscapeString(productName),
		html.EscapeString(intro),
		fulfilmentBlock,
		html.EscapeString(queryLink),
	)
}
