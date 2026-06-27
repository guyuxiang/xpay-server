package handler

import (
	"embed"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/payapi/x402-server/internal/config"
	"github.com/payapi/x402-server/internal/pricing"
	"github.com/payapi/x402-server/internal/store"
)

//go:embed admin/*
var adminAssets embed.FS

type Admin struct {
	cfg    *config.Config
	db     *store.DB
	prices *pricing.Manager
}

func NewAdmin(cfg *config.Config, db *store.DB, prices *pricing.Manager) *Admin {
	return &Admin{cfg: cfg, db: db, prices: prices}
}

func (h *Admin) Page(c *gin.Context) {
	data, err := adminAssets.ReadFile("admin/index.html")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func (h *Admin) Overview(c *gin.Context) {
	summary, err := h.db.Summary()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	recent, err := h.db.RecentPayments(20)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	models, err := h.db.ListModelPrices()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"summary": summary,
		"recent":  recent,
		"prices":  models,
		"config": gin.H{
			"paymentChain":     h.cfg.PaymentChain,
			"network":          h.cfg.Network,
			"chainId":          h.cfg.ChainID,
			"payTo":            h.cfg.PayToAddress,
			"asset":            h.cfg.USDCAddress,
			"upstream":         h.cfg.UpstreamURL,
			"markup":           h.cfg.Markup,
			"cacheTTLSecs":     h.cfg.CacheTTLSecs,
			"sigTimeoutSecs":   h.cfg.SigTimeoutSecs,
			"maxBodyBytes":     h.cfg.MaxBodyBytes,
			"allowedOrigins":   h.cfg.AllowedOrigins,
			"adminAuthEnabled": h.cfg.AdminToken != "",
			"dryRun":           h.cfg.DryRun,
		},
	})
}

func (h *Admin) SaveSettings(c *gin.Context) {
	var req struct {
		Markup float64 `json:"markup"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Markup <= 0 || math.IsNaN(req.Markup) || math.IsInf(req.Markup, 0) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "markup must be a positive finite number"})
		return
	}
	h.cfg.Markup = req.Markup
	if err := h.db.SetSetting("markup", strconv.FormatFloat(req.Markup, 'f', -1, 64)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "markup": h.cfg.Markup})
}

func (h *Admin) SavePrice(c *gin.Context) {
	var req pricing.ModelPriceEntry
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Model = strings.ToLower(strings.TrimSpace(req.Model))
	if req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}
	if _, err := pricing.NewTableFromEntries(pricing.DefaultPrice(), []pricing.ModelPriceEntry{req}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.IsDefault = false
	if err := h.db.UpsertModelPrice(req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.reloadPrices(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Admin) DeletePrice(c *gin.Context) {
	model := strings.ToLower(strings.TrimSpace(c.Param("model")))
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}
	if err := h.db.DeleteModelPrice(model); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.reloadPrices(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Admin) ResetPrices(c *gin.Context) {
	if err := h.db.ReplaceModelPrices(pricing.DefaultEntries()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.reloadPrices(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Admin) reloadPrices() error {
	models, err := h.db.ListModelPrices()
	if err != nil {
		return err
	}
	table, err := pricing.NewTableFromEntries(pricing.DefaultPrice(), models)
	if err != nil {
		return err
	}
	h.prices.Replace(table)
	return nil
}

func AdminAuth(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		got := c.GetHeader("X-Admin-Token")
		if got == "" {
			got = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		}
		if got != token {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "admin token required"})
			return
		}
		c.Next()
	}
}
