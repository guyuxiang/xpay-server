package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/payapi/x402-server/internal/cache"
	"github.com/payapi/x402-server/internal/config"
	"github.com/payapi/x402-server/internal/pricing"
	"github.com/payapi/x402-server/internal/settler"
	"github.com/payapi/x402-server/internal/store"
	"github.com/payapi/x402-server/internal/types"
)

// Relay handles all OpenAI-compatible endpoints with x402 payment gating.
type Relay struct {
	cfg     *config.Config
	cache   *cache.Cache
	settler settler.PaymentSettler
	db      *store.DB
	client  *http.Client
	prices  *pricing.Manager
}

// NewRelay constructs a Relay with all dependencies wired.
func NewRelay(cfg *config.Config, c *cache.Cache, s settler.PaymentSettler, db *store.DB, prices *pricing.Manager) *Relay {
	return &Relay{
		cfg:     cfg,
		cache:   c,
		settler: s,
		db:      db,
		client:  &http.Client{Timeout: time.Duration(cfg.UpstreamTimeoutSecs) * time.Second},
		prices:  prices,
	}
}

// Handle is the gin handler registered for all /v1/* routes.
func (h *Relay) Handle(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.cfg.MaxBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read body: " + err.Error()})
		return
	}
	if c.GetHeader(types.HeaderPayment) != "" {
		h.handlePayment(c, body)
		return
	}
	h.handleInitial(c, body)
}

// handleInitial calls upstream with the system token, prices the response,
// caches it, and returns HTTP 402.
func (h *Relay) handleInitial(c *gin.Context, body []byte) {
	upstream := h.cfg.UpstreamURL + c.Request.URL.Path
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstream, bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "build upstream request"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.cfg.UpstreamToken)
	if v := c.GetHeader("anthropic-version"); v != "" {
		req.Header.Set("anthropic-version", v)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream relay failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		c.Data(resp.StatusCode, ct, respBody)
		return
	}

	// Parse token usage — supports both OpenAI and Anthropic response shapes.
	var env struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			CacheReadTokens  int `json:"cache_read_input_tokens"`
			PromptDetails    struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(respBody, &env)
	u := pricing.Usage{
		PromptTokens:     env.Usage.PromptTokens,
		CompletionTokens: env.Usage.CompletionTokens,
		CachedTokens:     env.Usage.PromptDetails.CachedTokens,
	}
	if u.PromptTokens == 0 && u.CompletionTokens == 0 {
		u.PromptTokens = env.Usage.InputTokens
		u.CompletionTokens = env.Usage.OutputTokens
	}
	if u.CachedTokens == 0 {
		u.CachedTokens = env.Usage.InputDetails.CachedTokens
	}
	if u.CachedTokens == 0 {
		u.CachedTokens = env.Usage.CacheReadTokens
	}
	if u.PromptTokens == 0 && u.CompletionTokens == 0 {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "x402: could not determine token usage (streaming is not supported; set stream=false)",
		})
		return
	}

	model := env.Model
	if model == "" {
		var reqModel struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &reqModel)
		model = reqModel.Model
	}
	cost := h.prices.CostMicroUSDC(model, u, h.cfg.Markup)

	reqID := cache.RequestID(append(body, []byte(fmt.Sprintf("|%d", time.Now().UnixNano()))...))
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	h.cache.Store(reqID, &cache.Entry{
		Body: respBody, Status: resp.StatusCode, ContentType: ct,
		Cost: cost, Usage: u, Model: model,
	})

	challenge := types.PaymentRequiredResponse{
		X402Version: types.X402Version,
		Accepts: []types.PaymentRequirements{
			h.settler.BuildRequirements(
				c.Request.URL.Path,
				fmt.Sprintf("%s: %d prompt + %d completion tokens", model, u.PromptTokens, u.CompletionTokens),
				reqID,
				cost,
				h.cfg.SigTimeoutSecs,
			),
		},
	}
	c.Header(types.HeaderRequest, reqID)
	c.Header(types.HeaderCost, pricing.USDCUnitsToUSD(cost))
	c.JSON(http.StatusPaymentRequired, challenge)
}

// handlePayment verifies the signed authorization, settles on-chain, and
// delivers the cached response.
func (h *Relay) handlePayment(c *gin.Context, _ []byte) {
	reqID := c.GetHeader(types.HeaderRequest)
	if reqID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing " + types.HeaderRequest})
		return
	}
	cached := h.cache.Get(reqID)
	if cached == nil {
		c.JSON(http.StatusGone, gin.H{"error": "staged response expired or not found"})
		return
	}

	payment, err := decodePaymentHeader(c.GetHeader(types.HeaderPayment))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad X-PAYMENT: " + err.Error()})
		return
	}
	if payment.X402Version != types.X402Version {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported x402Version"})
		return
	}
	if payment.Network != h.cfg.Network {
		c.JSON(http.StatusBadRequest, gin.H{"error": "network mismatch"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	settlement, err := h.settler.Settle(ctx, payment, cached.Cost, reqID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "settlement failed: " + err.Error()})
		return
	}

	if err := h.db.Record(&store.Payment{
		FromAddress:      settlement.Payer,
		ToAddress:        h.cfg.PayToAddress,
		Amount:           cached.Cost.Int64(),
		TxHash:           settlement.TxHash,
		Model:            cached.Model,
		PromptTokens:     cached.Usage.PromptTokens,
		CompletionTokens: cached.Usage.CompletionTokens,
		RequestID:        reqID,
		Network:          h.cfg.Network,
	}); err != nil {
		slog.Error("x402: record payment", "err", err)
	}

	settlementJSON, _ := json.Marshal(types.SettlementResponse{
		Success: true, Transaction: settlement.TxHash, Network: h.cfg.Network, Payer: settlement.Payer,
	})
	h.cache.Delete(reqID)

	c.Header(types.HeaderResponse, base64.StdEncoding.EncodeToString(settlementJSON))
	c.Header(types.HeaderCost, pricing.USDCUnitsToUSD(cached.Cost))
	c.Header(types.HeaderTx, settlement.TxHash)
	c.Data(cached.Status, cached.ContentType, cached.Body)
}

func decodePaymentHeader(h string) (types.PaymentPayload, error) {
	var p types.PaymentPayload
	if h == "" {
		return p, fmt.Errorf("empty header")
	}
	raw, err := base64.StdEncoding.DecodeString(h)
	if err != nil {
		raw = []byte(h) // tolerate raw JSON for debugging
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if p.Scheme != types.SchemeExact {
		return p, fmt.Errorf("unsupported scheme %q", p.Scheme)
	}
	return p, nil
}
