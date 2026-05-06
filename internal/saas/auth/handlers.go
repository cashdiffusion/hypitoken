package auth

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/mail"
)

type Handler struct {
	DB           *db.DB
	Issuer       *Issuer
	Mailer       mail.Mailer
	SiteName     string
	FreeRegister bool
	CodeTTL      time.Duration

	codeLimiter *codeRateLimiter
}

func NewHandler(store *db.DB, iss *Issuer, m mail.Mailer, siteName string, freeRegister bool) *Handler {
	return &Handler{
		DB: store, Issuer: iss, Mailer: m, SiteName: siteName, FreeRegister: freeRegister, CodeTTL: 10 * time.Minute,
		codeLimiter: newCodeRateLimiter(),
	}
}

// Routes mounts /auth/* under the given gin RouterGroup.
func (h *Handler) Routes(g *gin.RouterGroup) {
	g.POST("/register", h.register)
	g.POST("/login", h.login)
	g.POST("/send-code", h.sendCode)
	g.POST("/verify-email", h.verifyEmail)
	g.POST("/reset-password", h.resetPassword)
}

type registerReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Code     string `json:"code"` // email verification code (optional in dev)
}

func (h *Handler) register(c *gin.Context) {
	if !h.FreeRegister {
		c.JSON(http.StatusForbidden, gin.H{"error": "registration disabled"})
		return
	}
	var req registerReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(req.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email"})
		return
	}
	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 8 chars"})
		return
	}
	exists, err := h.DB.EmailExists(c.Request.Context(), req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
		return
	}
	// Email verification is mandatory in production. Empty code → reject.
	// The legacy "skip verification" path was a dev-only escape hatch and
	// is now closed: an attacker would otherwise be able to spam-create
	// unverified accounts without ever owning the inbox.
	if strings.TrimSpace(req.Code) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "verification code required"})
		return
	}
	if err := h.DB.ConsumeEmailCode(c.Request.Context(), req.Email, req.Code, db.PurposeVerify); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	verified := true
	hash, err := HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	def, err := h.DB.DefaultGroup(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	u, err := h.DB.CreateUser(c.Request.Context(), req.Email, hash, db.RoleUser, def.ID, verified)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tok, exp, err := h.Issuer.Issue(u.ID, u.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":    tok,
		"expires":  exp.Unix(),
		"user":     userView(u),
	})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) login(c *gin.Context) {
	var req loginReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	u, err := h.DB.GetUserByEmail(c.Request.Context(), req.Email)
	if err != nil || !CheckPassword(u.PWHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if u.Disabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "account disabled"})
		return
	}
	tok, exp, err := h.Issuer.Issue(u.ID, u.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":   tok,
		"expires": exp.Unix(),
		"user":    userView(u),
	})
}

type sendCodeReq struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"` // verify | reset
}

func (h *Handler) sendCode(c *gin.Context) {
	var req sendCodeReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(req.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email"})
		return
	}
	if req.Purpose != db.PurposeVerify && req.Purpose != db.PurposeReset {
		req.Purpose = db.PurposeVerify
	}
	if req.Purpose == db.PurposeReset {
		// silently no-op for unknown emails to avoid enumeration
		if _, err := h.DB.GetUserByEmail(c.Request.Context(), req.Email); err != nil {
			c.JSON(http.StatusOK, gin.H{"sent": true})
			return
		}
	}
	// Rate-limit before issuing the code (and before sending any mail). We
	// gate on (email, client IP) — protects the user's inbox and the
	// operator's SMTP quota.
	if ok, retry := h.codeLimiter.allow(req.Email, c.ClientIP()); !ok {
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":       "too many code requests",
			"retry_after": retry,
		})
		return
	}
	code, err := db.GenerateNumericCode(6)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.DB.PutEmailCode(c.Request.Context(), req.Email, code, req.Purpose, h.CodeTTL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var subject, body string
	if req.Purpose == db.PurposeReset {
		subject, body = mail.ResetEmail(h.SiteName, code)
	} else {
		subject, body = mail.VerificationEmail(h.SiteName, code)
	}
	if err := h.Mailer.Send(req.Email, subject, body); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "send mail: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

type verifyEmailReq struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func (h *Handler) verifyEmail(c *gin.Context) {
	var req verifyEmailReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if err := h.DB.ConsumeEmailCode(c.Request.Context(), req.Email, req.Code, db.PurposeVerify); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if u, err := h.DB.GetUserByEmail(c.Request.Context(), req.Email); err == nil {
		_ = h.DB.MarkEmailVerified(c.Request.Context(), u.ID)
	}
	c.JSON(http.StatusOK, gin.H{"verified": true})
}

type resetPasswordReq struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

func (h *Handler) resetPassword(c *gin.Context) {
	var req resetPasswordReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 8 chars"})
		return
	}
	if err := h.DB.ConsumeEmailCode(c.Request.Context(), req.Email, req.Code, db.PurposeReset); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := h.DB.GetUserByEmail(c.Request.Context(), req.Email)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user not found"})
		return
	}
	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.DB.UpdateUserPassword(c.Request.Context(), u.ID, hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"reset": true})
}

func validEmail(email string) bool {
	return strings.Contains(email, "@") && strings.Contains(email, ".") && len(email) >= 5 && len(email) < 200
}

func userView(u *db.User) gin.H {
	return gin.H{
		"id":             u.ID,
		"email":          u.Email,
		"role":           u.Role,
		"balance_usd":    u.BalanceUSD,
		"group_id":       u.GroupID,
		"email_verified": u.EmailVerified,
		"created_at":     u.CreatedAt.Unix(),
	}
}
