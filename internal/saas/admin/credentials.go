package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/auth"
)

// CredHandler is the SaaS-admin's view onto the credential pool. It does not
// own the pool — it is a thin façade so the new operator panel doesn't have
// to talk to the legacy /mgmt-console API surface.
type CredHandler struct {
	Pool    *auth.Pool
	AuthDir string
	UseUTLS bool
}

func NewCred(p *auth.Pool, authDir string, useUTLS bool) *CredHandler {
	return &CredHandler{Pool: p, AuthDir: authDir, UseUTLS: useUTLS}
}

func (c *CredHandler) Routes(g *gin.RouterGroup) {
	g.GET("/credentials", c.list)
	g.POST("/credentials/apikey", c.createAPIKey)
	g.POST("/credentials/oauth/start", c.oauthStart)
	g.POST("/credentials/oauth/finish", c.oauthFinish)
	g.DELETE("/credentials/:id", c.remove)
	g.PATCH("/credentials/:id/billing-rate", c.setBillingRate)
}

func (c *CredHandler) list(ctx *gin.Context) {
	type row struct {
		ID            string    `json:"id"`
		Kind          string    `json:"kind"`
		Provider      string    `json:"provider"`
		Label         string    `json:"label"`
		ProxyURL      string    `json:"proxy_url,omitempty"`
		BaseURL       string    `json:"base_url,omitempty"`
		Group         string    `json:"group,omitempty"`
		MaxConcurrent int       `json:"max_concurrent"`
		ActiveClients int       `json:"active_clients"`
		Disabled      bool      `json:"disabled"`
		Healthy       bool      `json:"healthy"`
		HardFailure   bool      `json:"hard_failure"`
		FailureReason string    `json:"failure_reason,omitempty"`
		QuotaExceeded bool      `json:"quota_exceeded,omitempty"`
		QuotaResetAt  time.Time `json:"quota_reset_at,omitempty"`
		ExpiresAt     time.Time `json:"expires_at,omitempty"`
		BillingRate   float64   `json:"billing_rate"`
	}
	out := make([]row, 0)
	for _, st := range c.Pool.Status() {
		kind := "oauth"
		if st.Auth.Kind == auth.KindAPIKey {
			kind = "apikey"
		}
		live := c.Pool.FindByID(st.Auth.ID)
		healthy := false
		hard := false
		failReason := ""
		if live != nil {
			healthy = live.IsHealthy()
			hard = live.IsHardFailed()
			failReason = live.LastFailureReason
		}
		out = append(out, row{
			ID:            st.Auth.ID,
			Kind:          kind,
			Provider:      auth.NormalizeProvider(st.Auth.Provider),
			Label:         st.Auth.Label,
			ProxyURL:      st.Auth.ProxyURL,
			BaseURL:       st.Auth.BaseURL,
			Group:         st.Auth.Group,
			MaxConcurrent: st.Auth.MaxConcurrent,
			ActiveClients: st.ActiveClients,
			Disabled:      st.Auth.Disabled,
			Healthy:       healthy,
			HardFailure:   hard,
			FailureReason: failReason,
			QuotaExceeded: !st.Auth.QuotaExceededAt.IsZero(),
			QuotaResetAt:  st.Auth.QuotaResetAt,
			ExpiresAt:     st.Auth.ExpiresAt,
			BillingRate:   st.Auth.BillingRate,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{"credentials": out})
}

type apikeyReq struct {
	Provider string `json:"provider"`
	Key      string `json:"key"`
	Label    string `json:"label"`
	BaseURL  string `json:"base_url"`
	ProxyURL string `json:"proxy_url"`
	Group    string `json:"group"`
}

func (c *CredHandler) createAPIKey(ctx *gin.Context) {
	var req apikeyReq
	if err := ctx.BindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "key required"})
		return
	}
	if c.AuthDir == "" {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "auth_dir not configured"})
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = fmt.Sprintf("apikey-%d", time.Now().Unix())
	}
	provider := auth.NormalizeProvider(req.Provider)
	typ := "apikey"
	if provider == auth.ProviderOpenAI {
		typ = "openai_api_key"
	}
	raw := map[string]any{
		"type":     typ,
		"provider": provider,
		"api_key":  key,
		"label":    label,
	}
	if p := strings.TrimSpace(req.ProxyURL); p != "" {
		raw["proxy_url"] = p
	}
	if b := strings.TrimSpace(req.BaseURL); b != "" {
		raw["base_url"] = strings.TrimRight(b, "/")
	}
	if g := auth.NormalizeGroup(req.Group); g != "" {
		raw["group"] = g
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(c.AuthDir, 0o700); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	name := sanitizeFilename("apikey-"+label) + ".json"
	full := filepath.Join(c.AuthDir, name)
	a, err := auth.ParseFile(full, data)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Pool.AddAPIKey(a)
	ctx.JSON(http.StatusOK, gin.H{"id": a.ID, "label": a.Label})
}

func (c *CredHandler) remove(ctx *gin.Context) {
	id := ctx.Param("id")
	a := c.Pool.RemoveAuth(id)
	if a == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if a.FilePath != "" {
		if err := os.Remove(a.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warnf("saas-admin: remove %s: %v", a.FilePath, err)
		}
	}
	ctx.JSON(http.StatusOK, gin.H{"removed": id})
}

// ---- OAuth login flow ----

type oauthStartReq struct {
	Provider string `json:"provider"`
	ProxyURL string `json:"proxy_url"`
	Label    string `json:"label"`
}

func (c *CredHandler) oauthStart(ctx *gin.Context) {
	var req oauthStartReq
	_ = ctx.BindJSON(&req)
	provider := auth.NormalizeProvider(req.Provider)
	sess, authURL, err := auth.StartLogin(provider, req.ProxyURL, req.Label)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"session_id":   sess.ID,
		"provider":     provider,
		"auth_url":     authURL,
		"redirect_uri": auth.RedirectURIFor(provider),
	})
}

type oauthFinishReq struct {
	SessionID     string `json:"session_id"`
	Callback      string `json:"callback"`
	Code          string `json:"code"`
	State         string `json:"state"`
	MaxConcurrent int    `json:"max_concurrent"`
	Group         string `json:"group"`
}

func (c *CredHandler) oauthFinish(ctx *gin.Context) {
	var req oauthFinishReq
	if err := ctx.BindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "missing session_id"})
		return
	}
	if c.AuthDir == "" {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "auth_dir not configured"})
		return
	}
	code := strings.TrimSpace(req.Code)
	state := strings.TrimSpace(req.State)
	if code == "" && strings.TrimSpace(req.Callback) != "" {
		c2, s2, err := auth.ParseCallback(req.Callback)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		code = c2
		if state == "" {
			state = s2
		}
	}
	cctx, cancel := context.WithTimeout(ctx.Request.Context(), 30*time.Second)
	defer cancel()
	a, err := auth.FinishLogin(cctx, req.SessionID, code, state, c.AuthDir, req.MaxConcurrent, c.UseUTLS, req.Group)
	if err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Pool.AddOAuth(a)
	ctx.JSON(http.StatusOK, gin.H{"id": a.ID, "email": a.Email})
}

func (c *CredHandler) setBillingRate(ctx *gin.Context) {
	id := ctx.Param("id")
	a := c.Pool.FindByID(id)
	if a == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		Rate float64 `json:"rate"`
	}
	if err := ctx.BindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if req.Rate < 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "rate must be >= 0"})
		return
	}
	a.SetBillingRate(req.Rate)
	if err := a.Persist(); err != nil {
		log.Warnf("saas-admin: persist billing rate for %s: %v", id, err)
	}
	ctx.JSON(http.StatusOK, gin.H{"id": id, "billing_rate": req.Rate})
}

var fnameSafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeFilename(s string) string {
	s = fnameSafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "apikey"
	}
	return s
}
