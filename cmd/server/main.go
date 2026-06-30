package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xpay/xpay-server/internal/cache"
	"github.com/xpay/xpay-server/internal/config"
	"github.com/xpay/xpay-server/internal/handler"
	"github.com/xpay/xpay-server/internal/pricing"
	"github.com/xpay/xpay-server/internal/settler"
	"github.com/xpay/xpay-server/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config error", "err", err)
		os.Exit(1)
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		slog.Error("db error", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.BootstrapDefaultPrices(pricing.DefaultEntries()); err != nil {
		slog.Error("bootstrap prices error", "err", err)
		os.Exit(1)
	}
	if value, ok, err := db.GetSetting("markup"); err != nil {
		slog.Error("load settings error", "err", err)
		os.Exit(1)
	} else if ok {
		markup, err := strconv.ParseFloat(value, 64)
		if err != nil || markup <= 0 {
			slog.Error("invalid stored markup", "value", value)
			os.Exit(1)
		}
		cfg.Markup = markup
	}

	s, err := settler.New(cfg)
	if err != nil {
		slog.Error("settler error", "err", err)
		os.Exit(1)
	}

	ttl := time.Duration(cfg.CacheTTLSecs) * time.Second
	c := cache.New(ttl)
	defer c.Close()

	modelPrices, err := db.ListModelPrices()
	if err != nil {
		slog.Error("load prices error", "err", err)
		os.Exit(1)
	}
	priceTable, err := pricing.NewTableFromEntries(pricing.DefaultPrice(), modelPrices)
	if err != nil {
		slog.Error("compile prices error", "err", err)
		os.Exit(1)
	}
	priceManager := pricing.NewManager(priceTable)

	relay := handler.NewRelay(cfg, c, s, db, priceManager)
	info := handler.NewInfo(cfg, db)
	admin := handler.NewAdmin(cfg, db, priceManager)

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware(cfg.AllowedOrigins))

	base := r.Group(cfg.BaseURL)

	// x402-gated LLM endpoints
	base.POST("/v1/chat/completions", relay.Handle)
	base.POST("/v1/responses", relay.Handle)
	base.POST("/v1/messages", relay.Handle)
	base.POST("/v1/completions", relay.Handle)

	// Info endpoints (no auth)
	base.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	base.GET("/v1/info/address", info.Address)
	base.GET("/v1/info/balance", info.Balance)
	base.GET("/admin", admin.Page)
	adminAPI := base.Group("/admin/api", handler.AdminAuth(cfg.AdminToken))
	adminAPI.GET("/overview", admin.Overview)
	adminAPI.PUT("/settings", admin.SaveSettings)
	adminAPI.POST("/prices", admin.SavePrice)
	adminAPI.DELETE("/prices/:model", admin.DeletePrice)
	adminAPI.POST("/prices/reset-defaults", admin.ResetPrices)

	addr := ":" + cfg.Port
	slog.Info("xpay-server starting",
		"addr", addr,
		"upstream", cfg.UpstreamURL,
		"network", cfg.Network,
		"baseURL", cfg.BaseURL,
		"payTo", cfg.PayToAddress,
		"dryRun", cfg.DryRun,
	)

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Duration(cfg.UpstreamTimeoutSecs+10) * time.Second,
		WriteTimeout:      time.Duration(cfg.UpstreamTimeoutSecs+60) * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stop:
		slog.Info("shutdown signal received", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeoutSecs)*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("graceful shutdown failed", "err", err)
			os.Exit(1)
		}
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return
		}
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func corsMiddleware(allowed []string) gin.HandlerFunc {
	allowAll := len(allowed) == 0 || slices.Contains(allowed, "*")
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowAll {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if origin != "" && slices.Contains(allowed, origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, "+
			"X-PAYMENT, X-PAYMENT-REQUEST-ID, anthropic-version")
		c.Header("Access-Control-Expose-Headers", "X-PAYMENT-RESPONSE, X-Cost, X-Tx, X-PAYMENT-REQUEST-ID")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
