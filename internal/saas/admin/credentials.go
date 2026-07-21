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

	"github.com/wjsoj/cc-core/auth"
)

// CredHandler is the SaaS-admin's view onto the credential pool. It does not
// own the pool — it is a thin façade so the new operator panel doesn't have
// to talk to the legacy /admin/api/* surface.
type CredHandler struct {
	Pool    *auth.Pool
	AuthDir string
	UseUTLS bool
}

func NewCred(p *auth.Pool, authDir string, useUTLS bool) *CredHandler {
	return &CredHandler{Pool: p, AuthDir: authDir, UseUTLS: useUTLS}
}

func (c *CredHandler) Routes(g *gin.RouterGroup) {
	// GET /credentials and the per-id mutation routes (PATCH / refresh /
	// clear-quota / clear-failure) are owned by the legacy admin bridge so
	// the SaaS panel sees the same rich usage / quota / model_map data the
	// operator panel does. See legacyadmin.Handler.RegisterSaaSBridge.
	g.POST("/credentials/apikey", c.createAPIKey)
	g.POST("/credentials/oauth/start", c.oauthStart)
	g.POST("/credentials/oauth/finish", c.oauthFinish)
	g.POST("/credentials/oauth/session-cookie", c.sessionCookie)
	g.DELETE("/credentials/:id", c.remove)
}

type apikeyReq struct {
	Provider string            `json:"provider"`
	APIKey   string            `json:"api_key"`
	Key      string            `json:"key"` // Backward compatibility for older clients.
	Label    string            `json:"label"`
	BaseURL  string            `json:"base_url"`
	ProxyURL string            `json:"proxy_url"`
	Group    string            `json:"group"`
	ModelMap map[string]string `json:"model_map"`
}

func (c *CredHandler) createAPIKey(ctx *gin.Context) {
	var req apikeyReq
	if err := ctx.BindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	key := strings.TrimSpace(req.APIKey)
	if key == "" {
		key = strings.TrimSpace(req.Key)
	}
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
	if len(req.ModelMap) > 0 {
		mm := make(map[string]any, len(req.ModelMap))
		for k, v := range req.ModelMap {
			if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
				mm[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		if len(mm) > 0 {
			raw["model_map"] = mm
		}
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

// ---- Session-cookie login (Anthropic only) ----

type sessionCookieReq struct {
	SessionCookie string `json:"session_cookie"`
	ProxyURL      string `json:"proxy_url"`
	Label         string `json:"label"`
	Group         string `json:"group"`
	MaxConcurrent int    `json:"max_concurrent"`
}

// sessionCookie drives the claude.com sessionKey login: it exchanges a
// sk-ant-sid… cookie for a usable OAuth credential. A proxy is required because
// driving claude.com from a server IP without browser-grade TLS fails
// Cloudflare's bot challenge — cc-core forces uTLS for this path.
func (c *CredHandler) sessionCookie(ctx *gin.Context) {
	var req sessionCookieReq
	if err := ctx.BindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if c.AuthDir == "" {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "auth_dir not configured"})
		return
	}
	cctx, cancel := context.WithTimeout(ctx.Request.Context(), 60*time.Second)
	defer cancel()
	a, err := auth.LoginWithSessionCookie(cctx, req.SessionCookie, req.ProxyURL, req.Label, req.Group, req.MaxConcurrent, c.AuthDir)
	if err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Pool.AddOAuth(a)
	ctx.JSON(http.StatusOK, gin.H{"id": a.ID, "email": a.Email})
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
