package admin

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/auth"
)

// CredHandler is the SaaS-admin's view onto the credential pool. It does not
// own the pool — it is a thin façade so the new operator panel doesn't have
// to talk to the legacy /mgmt-console API surface.
type CredHandler struct {
	Pool *auth.Pool
}

func NewCred(p *auth.Pool) *CredHandler { return &CredHandler{Pool: p} }

func (c *CredHandler) Routes(g *gin.RouterGroup) {
	g.GET("/credentials", c.list)
	g.POST("/credentials/apikey", c.createAPIKey)
	g.DELETE("/credentials/:id", c.remove)
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
	if strings.TrimSpace(req.Key) == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "key required"})
		return
	}
	if req.Label == "" {
		req.Label = fmt.Sprintf("apikey-%d", time.Now().Unix())
	}
	a := &auth.Auth{
		ID:          "apikey:" + req.Label,
		Kind:        auth.KindAPIKey,
		Provider:    auth.NormalizeProvider(req.Provider),
		Label:       req.Label,
		AccessToken: req.Key,
		ProxyURL:    req.ProxyURL,
		BaseURL:     req.BaseURL,
		Group:       auth.NormalizeGroup(req.Group),
	}
	c.Pool.AddAPIKey(a)
	ctx.JSON(http.StatusOK, gin.H{"id": a.ID, "label": a.Label})
}

func (c *CredHandler) remove(ctx *gin.Context) {
	id := ctx.Param("id")
	if c.Pool.RemoveAuth(id) == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"removed": id})
}
