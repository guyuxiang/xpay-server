package handler

import (
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/payapi/x402-server/internal/config"
	"github.com/payapi/x402-server/internal/pricing"
	"github.com/payapi/x402-server/internal/store"
	"github.com/payapi/x402-server/internal/types"
	"math/big"
)

// Info handles informational endpoints (no payment required).
type Info struct {
	cfg *config.Config
	db  *store.DB
}

func NewInfo(cfg *config.Config, db *store.DB) *Info {
	return &Info{cfg: cfg, db: db}
}

// Address returns the server's payment address and chain info.
func (h *Info) Address(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"payTo":   h.cfg.PayToAddress,
		"network": h.cfg.Network,
		"chainId": h.cfg.ChainID,
		"asset":   h.cfg.USDCAddress,
		"scheme":  types.SchemeExact,
	})
}

// Balance returns historical spend for an address.
func (h *Info) Balance(c *gin.Context) {
	addr := c.Query("address")
	if !common.IsHexAddress(addr) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid address"})
		return
	}
	norm := common.HexToAddress(addr).Hex()
	total, count, err := h.db.SumByAddress(norm)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	recent, _ := h.db.RecentByAddress(norm, 50)
	c.JSON(http.StatusOK, gin.H{
		"address":       norm,
		"totalSpent":    total,
		"totalSpentUSD": pricing.USDCUnitsToUSD(big.NewInt(total)),
		"paymentCount":  count,
		"recent":        recent,
	})
}
