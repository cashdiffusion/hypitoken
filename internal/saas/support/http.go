package support

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// appealCodeTTL bounds how long the emailed OTP stays valid. Longer than the
// registration code's window: an appeal is usually written after the user has
// already spent a while working out what happened to their account.
const appealCodeTTL = 30 * time.Minute

// PublicRoutes mounts the unauthenticated appeal channel on /api/v2. These are
// the ONLY support endpoints a disabled account can reach — RequireUser rejects
// them everywhere else — so they authenticate per-request with an emailed OTP
// and, for reads, the access key handed out at submission.
func (s *Service) PublicRoutes(g *gin.RouterGroup) {
	a := g.Group("/appeal")
	a.POST("/send-code", s.appealSendCode)
	a.POST("", s.appealSubmit)
	a.GET("/:id", s.appealGet)
	a.POST("/:id/reply", s.appealReply)
}

// UserRoutes mounts the signed-in support desk under /api/v2/me/tickets.
func (s *Service) UserRoutes(g *gin.RouterGroup) {
	t := g.Group("/me/tickets")
	t.GET("", s.userList)
	t.POST("", s.userCreate)
	t.GET("/:id", s.userGet)
	t.POST("/:id/reply", s.userReply)
}

// AdminRoutes mounts the operator queue under /api/v2/admin/tickets.
func (s *Service) AdminRoutes(g *gin.RouterGroup) {
	t := g.Group("/tickets")
	t.GET("", s.adminList)
	t.GET("/:id", s.adminGet)
	t.POST("/:id/reply", s.adminReply)
	t.POST("/:id/status", s.adminStatus)
}

// ---- public appeal channel ----

type sendCodeReq struct {
	Email string `json:"email"`
}

func (s *Service) appealSendCode(c *gin.Context) {
	var req sendCodeReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email"})
		return
	}
	if ok, retry := s.codeRL.allowSend(email, c.ClientIP()); !ok {
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试", "retry_after": retry})
		return
	}
	code, err := db.GenerateNumericCode(6)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if err := s.DB.PutEmailCode(c.Request.Context(), email, code, db.PurposeAppeal, appealCodeTTL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	// Always answer "sent", whether or not the address has an account. An appeal
	// form that reveals which emails are registered is a free account-enumeration
	// oracle, and the ban-appeal page is precisely where an attacker would go
	// looking to confirm which of their farm addresses we caught.
	if s.Mailer != nil {
		subject, html, text := appealCodeEmail(s.SiteName, code)
		go func() {
			if err := s.Mailer.Send(email, subject, html, text); err != nil {
				log.Warnf("support: appeal code mail to %s failed: %v", email, err)
			}
		}()
	}
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

type appealReq struct {
	Email   string `json:"email"`
	Code    string `json:"code"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (s *Service) appealSubmit(c *gin.Context) {
	var req appealReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(email) || strings.TrimSpace(req.Body) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写邮箱与申诉内容"})
		return
	}
	if ok, retry := s.codeRL.allowSubmit(email, c.ClientIP()); !ok {
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "提交过于频繁，请稍后再试", "retry_after": retry})
		return
	}
	if err := s.DB.ConsumeEmailCode(c.Request.Context(), email, strings.TrimSpace(req.Code), db.PurposeAppeal); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "验证码无效或已过期"})
		return
	}
	// Link the appeal to the account when one exists, so the operator sees the
	// user's history — but an address with no account can still appeal (the
	// account may have been deleted, or they may be writing about a signup that
	// never completed).
	var userID int64
	if u, err := s.DB.GetUserByEmail(c.Request.Context(), email); err == nil {
		userID = u.ID
	}
	// An anonymous appeal keeps user_id = 0 so it is read back by access key.
	// A linked appeal still gets a key: the whole point is that this user cannot
	// log in, so the key is their only way back to the thread.
	t, err := s.Create(c.Request.Context(), 0, email, KindAppeal, req.Subject, req.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if userID > 0 {
		if _, uerr := s.DB.ExecContext(c.Request.Context(),
			`UPDATE support_tickets SET user_id = ? WHERE id = ?`, userID, t.ID); uerr != nil {
			log.Warnf("support: link appeal %d to user %d: %v", t.ID, userID, uerr)
		}
	}
	log.Infof("support: appeal ticket %d filed by %s (user=%d)", t.ID, email, userID)
	c.JSON(http.StatusOK, gin.H{"ticket": t})
}

func (s *Service) appealGet(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := s.GetForKey(c.Request.Context(), id, c.Query("key"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": t})
}

type replyReq struct {
	Body string `json:"body"`
}

func (s *Service) appealReply(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req replyReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	t, err := s.GetForKey(c.Request.Context(), id, c.Query("key"))
	if err != nil {
		respondErr(c, err)
		return
	}
	if ok, retry := s.codeRL.allowSubmit(t.Email, c.ClientIP()); !ok {
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "提交过于频繁，请稍后再试", "retry_after": retry})
		return
	}
	if err := s.Reply(c.Request.Context(), id, AuthorUser, req.Body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fresh, _ := s.Get(c.Request.Context(), id)
	c.JSON(http.StatusOK, gin.H{"ticket": fresh})
}

// ---- signed-in user desk ----

func (s *Service) userList(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	limit, offset := pageParams(c)
	items, total, err := s.ListForUser(c.Request.Context(), u.ID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tickets": items, "total": total, "limit": limit, "offset": offset})
}

type createReq struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (s *Service) userCreate(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	var req createReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if ok, retry := s.codeRL.allowSubmit(u.Email, c.ClientIP()); !ok {
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "提交过于频繁，请稍后再试", "retry_after": retry})
		return
	}
	t, err := s.Create(c.Request.Context(), u.ID, u.Email, KindSupport, req.Subject, req.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// A signed-in user reaches their thread through the session; the key would
	// be a second, weaker credential for no benefit.
	t.AccessKey = ""
	c.JSON(http.StatusOK, gin.H{"ticket": t})
}

// ownedTicket loads a ticket and verifies it belongs to the caller.
func (s *Service) ownedTicket(c *gin.Context) (*Ticket, bool) {
	id, ok := pathID(c)
	if !ok {
		return nil, false
	}
	t, err := s.Get(c.Request.Context(), id)
	if err != nil {
		respondErr(c, err)
		return nil, false
	}
	u := saasauth.CurrentUser(c)
	if u == nil || t.UserID != u.ID {
		respondErr(c, ErrForbidden)
		return nil, false
	}
	return t, true
}

func (s *Service) userGet(c *gin.Context) {
	t, ok := s.ownedTicket(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": t})
}

func (s *Service) userReply(c *gin.Context) {
	t, ok := s.ownedTicket(c)
	if !ok {
		return
	}
	var req replyReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if err := s.Reply(c.Request.Context(), t.ID, AuthorUser, req.Body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fresh, _ := s.Get(c.Request.Context(), t.ID)
	c.JSON(http.StatusOK, gin.H{"ticket": fresh})
}

// ---- operator queue ----

func (s *Service) adminList(c *gin.Context) {
	limit, offset := pageParams(c)
	items, total, err := s.ListForAdmin(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tickets": items, "total": total, "limit": limit, "offset": offset,
		"open": s.OpenCount(c.Request.Context()),
	})
}

func (s *Service) adminGet(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := s.Get(c.Request.Context(), id)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": t})
}

func (s *Service) adminReply(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req replyReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	t, err := s.Get(c.Request.Context(), id)
	if err != nil {
		respondErr(c, err)
		return
	}
	if err := s.Reply(c.Request.Context(), id, AuthorAdmin, req.Body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Mail the reply out. A disabled user has no reason to keep checking a page
	// they can't log into, so the notification is what actually closes the loop.
	if s.Mailer != nil {
		subject, html, text := replyEmail(s.SiteName, s.SiteURL, t, req.Body)
		to := t.Email
		go func() {
			if err := s.Mailer.Send(to, subject, html, text); err != nil {
				log.Warnf("support: reply mail to %s failed: %v", to, err)
			}
		}()
	}
	fresh, _ := s.Get(c.Request.Context(), id)
	c.JSON(http.StatusOK, gin.H{"ticket": fresh})
}

type statusReq struct {
	Status string `json:"status"`
}

func (s *Service) adminStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req statusReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if err := s.SetStatus(c.Request.Context(), id, req.Status); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fresh, _ := s.Get(c.Request.Context(), id)
	c.JSON(http.StatusOK, gin.H{"ticket": fresh})
}

// ---- helpers ----

func pathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

func pageParams(c *gin.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.Query("limit"))
	offset, _ = strconv.Atoi(c.Query("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func respondErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "工单不存在"})
	case errors.Is(err, ErrForbidden):
		// Same body as not-found so an access key cannot be probed for validity.
		c.JSON(http.StatusNotFound, gin.H{"error": "工单不存在"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// validEmail is the same shallow check the auth package applies: exactly one
// "@", something either side, no spaces. Real validation is the OTP.
func validEmail(s string) bool {
	if len(s) < 3 || len(s) > 254 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1 && strings.IndexByte(s[at+1:], '@') < 0 && strings.Contains(s[at+1:], ".")
}

func appealCodeEmail(siteName, code string) (subject, html, text string) {
	subject = fmt.Sprintf("%s 账号申诉验证码", siteName)
	html = fmt.Sprintf(`<div style="font-family:system-ui,-apple-system,'Segoe UI',sans-serif;max-width:520px;margin:0 auto;padding:32px 24px;color:#0f172a">
<p style="font-size:15px;line-height:1.7;margin:0 0 20px">你正在提交 %s 的账号申诉。请在申诉页面填入以下验证码：</p>
<p style="font-size:32px;font-weight:700;letter-spacing:.32em;margin:0 0 20px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace">%s</p>
<p style="font-size:13px;line-height:1.7;color:#64748b;margin:0">验证码 30 分钟内有效。如果这不是你本人的操作，忽略这封邮件即可，你的账号不会有任何变化。</p>
</div>`, htmlEsc(siteName), htmlEsc(code))
	text = fmt.Sprintf("你正在提交 %s 的账号申诉。验证码：%s（30 分钟内有效）。如果这不是你本人的操作，忽略这封邮件即可。", siteName, code)
	return subject, html, text
}

func replyEmail(siteName, siteURL string, t *Ticket, body string) (subject, html, text string) {
	subject = fmt.Sprintf("[%s] 工单 #%d 有新回复", siteName, t.ID)
	link := fmt.Sprintf("%s/support", strings.TrimRight(siteURL, "/"))
	html = fmt.Sprintf(`<div style="font-family:system-ui,-apple-system,'Segoe UI',sans-serif;max-width:560px;margin:0 auto;padding:32px 24px;color:#0f172a">
<p style="font-size:15px;line-height:1.7;margin:0 0 8px">你的工单 <strong>#%d · %s</strong> 收到了新回复：</p>
<div style="border-left:3px solid #10b981;background:#f8fafc;padding:14px 18px;margin:16px 0;font-size:14px;line-height:1.8;white-space:pre-wrap">%s</div>
<p style="font-size:14px;line-height:1.7;margin:0 0 20px">你可以直接回复这封邮件所在的工单页面继续沟通：<br><a href="%s" style="color:#0f766e">%s</a></p>
<p style="font-size:13px;color:#64748b;margin:0">— %s 支持团队</p>
</div>`, t.ID, htmlEsc(t.Subject), htmlEsc(body), link, link, htmlEsc(siteName))
	text = fmt.Sprintf("你的工单 #%d · %s 收到了新回复：\n\n%s\n\n查看并继续沟通：%s\n\n— %s 支持团队",
		t.ID, t.Subject, body, link, siteName)
	return subject, html, text
}

func htmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}
