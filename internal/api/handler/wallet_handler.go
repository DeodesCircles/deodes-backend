package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/deodes/backend/internal/api/middleware"
	"github.com/deodes/backend/internal/domain/auth"
	"github.com/deodes/backend/internal/domain/wallet"
	"github.com/deodes/backend/pkg/arc"
	"github.com/deodes/backend/pkg/response"
)

type WalletHandler struct {
	walletSvc wallet.Service
	authSvc   auth.Service
}

func NewWalletHandler(svc wallet.Service, authSvc auth.Service) *WalletHandler {
	return &WalletHandler{walletSvc: svc, authSvc: authSvc}
}

// CreateWallet creates a new Stellar wallet for the authenticated user, or returns existing
// POST /v1/wallets
func (h *WalletHandler) CreateWallet(c *gin.Context) {
	userID := middleware.GetUserID(c)

	// Check if wallet already exists — idempotent
	existing, err := h.walletSvc.GetWallets(c.Request.Context(), userID)
	if err == nil && len(existing) > 0 {
		response.OK(c, gin.H{"wallet": existing[0]})
		return
	}

	var req struct {
		PasskeySeed string `json:"passkeySeed" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "passkeySeed is required")
		return
	}

	w, err := h.walletSvc.CreateWallet(c.Request.Context(), userID, []byte(req.PasskeySeed))
	if err != nil {
		response.InternalError(c, "failed to create wallet: "+err.Error())
		return
	}

	response.Created(c, gin.H{"wallet": w})
}

// EnsureHostedArcWallet guarantees the caller has a custodial Arc (EVM 0x)
// wallet, creating it on first call. The seed derives deterministically from
// the user ID (never the email — emails change, IDs don't), so this is
// idempotent and recoverable: same user, same key, every time. No request
// body; safe to call after every login (best-effort, never blocks auth).
// POST /v1/wallets/hosted-arc/ensure
func (h *WalletHandler) EnsureHostedArcWallet(c *gin.Context) {	userID := middleware.GetUserID(c)
	ctx := c.Request.Context()

	if existing, err := h.walletSvc.GetWallets(ctx, userID); err == nil {
		for _, w := range existing {
			if w.WalletType == wallet.WalletTypeHosted {
				response.OK(c, gin.H{"wallet": w})
				return
			}
		}
	}

	seed, err := h.walletSvc.DeriveWalletSeed(ctx, "hosted-arc:"+userID)
	if err != nil {
		response.InternalError(c, "wallet derivation unavailable")
		return
	}
	w, err := h.walletSvc.CreateHostedArcWallet(ctx, userID, []byte(seed))
	if err != nil {
		// Lost race with a concurrent ensure: return the winner's wallet.
		if existing, rerr := h.walletSvc.GetWallets(ctx, userID); rerr == nil {
			for _, ew := range existing {
				if ew.WalletType == wallet.WalletTypeHosted {
					response.OK(c, gin.H{"wallet": ew})
					return
				}
			}
		}
		response.InternalError(c, "failed to ensure hosted wallet: "+err.Error())
		return
	}
	response.Created(c, gin.H{"wallet": w})
}

// CreateHostedArcWallet creates the custodial Arc (EVM 0x) wallet under us,
// or returns the existing hosted wallet (idempotent, one per user).
// POST /v1/wallets/hosted-arc
func (h *WalletHandler) CreateHostedArcWallet(c *gin.Context) {
	userID := middleware.GetUserID(c)

	if existing, err := h.walletSvc.GetWallets(c.Request.Context(), userID); err == nil {
		for _, w := range existing {
			if w.WalletType == wallet.WalletTypeHosted {
				response.OK(c, gin.H{"wallet": w})
				return
			}
		}
	}

	var req struct {
		PasskeySeed string `json:"passkeySeed" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "passkeySeed is required")
		return
	}

	w, err := h.walletSvc.CreateHostedArcWallet(c.Request.Context(), userID, []byte(req.PasskeySeed))
	if err != nil {
		response.InternalError(c, "failed to create hosted wallet: "+err.Error())
		return
	}

	response.Created(c, gin.H{"wallet": w})
}

// LinkWallet links the caller's own self-custody Arc (EVM 0x) wallet after
// they prove ownership by signing the login nonce (same flow as wallet
// login: fetch POST /v1/auth/nonce {walletAddress}, personal_sign it, send
// the signature here). The address must be passed exactly as it was when the
// nonce was requested, because nonces are keyed by that string.
// Idempotent: re-linking your own address returns it with 200.
// POST /v1/wallets/link
func (h *WalletHandler) LinkWallet(c *gin.Context) {
	userID := middleware.GetUserID(c)
	ctx := c.Request.Context()

	var req struct {
		Address       string `json:"address"`
		WalletAddress string `json:"walletAddress"`
		Signature     string `json:"signature" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	address := strings.TrimSpace(req.Address)
	if address == "" {
		address = strings.TrimSpace(req.WalletAddress)
	}
	if err := arc.ValidateAddress(address); err != nil {
		response.BadRequest(c, "invalid wallet address: must be Arc 0x + 40 hex chars")
		return
	}

	valid, err := h.authSvc.VerifySignature(ctx, address, req.Signature)
	if err != nil || !valid {
		response.Unauthorized(c, "signature verification failed")
		return
	}

	existing, err := h.walletSvc.GetWalletByAddress(ctx, address)
	if err != nil {
		response.InternalError(c, "failed to look up wallet")
		return
	}
	if existing != nil {
		if existing.UserID != userID {
			response.Conflict(c, "address is already linked to another account")
			return
		}
		response.OK(c, gin.H{"wallet": existing})
		return
	}

	// One linked wallet per user (UNIQUE on user_id+wallet_type): a second,
	// different address is a conflict, not a second row. SameSigner covers a
	// checksum-case variant that slipped past the normalized global lookup.
	if mine, err := h.walletSvc.GetWallets(ctx, userID); err == nil {
		for _, w := range mine {
			if w.WalletType != wallet.WalletTypeLinked {
				continue
			}
			if arc.SameSigner(w.PublicKey, address) {
				response.OK(c, gin.H{"wallet": w})
				return
			}
			response.Conflict(c, "account already has a different wallet linked")
			return
		}
	}

	w, err := h.walletSvc.LinkWallet(ctx, userID, address)
	if err != nil {
		response.InternalError(c, "failed to link wallet: "+err.Error())
		return
	}
	response.Created(c, gin.H{"wallet": w})
}

// ListWallets returns all wallets for the authenticated user
// GET /v1/wallets
func (h *WalletHandler) ListWallets(c *gin.Context) {	userID := middleware.GetUserID(c)
	wallets, err := h.walletSvc.GetWallets(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to list wallets")
		return
	}
	response.OK(c, gin.H{"wallets": wallets})
}

// Withdraw sends crypto from the user's wallet to a destination address
// POST /v1/wallets/withdraw
func (h *WalletHandler) Withdraw(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var req struct {
		Destination string  `json:"destination" binding:"required"`
		Asset       string  `json:"asset" binding:"required,oneof=XLM USDC"`
		Amount      float64 `json:"amount" binding:"required,gt=0"`
		PasskeySeed string  `json:"passkeySeed"`
		Memo        string  `json:"memo"`
		Label       string  `json:"label"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := wallet.ValidateMemo(req.Memo); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	label, err := wallet.NormalizeLabel(req.Label)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ipAddress := c.ClientIP()
	userAgent := c.GetHeader("User-Agent")
	txHash, err := h.walletSvc.SendPayment(c.Request.Context(), userID, []byte(req.PasskeySeed), req.Destination, req.Asset, req.Amount, req.Memo, ipAddress, userAgent)
	if err != nil {
		response.InternalError(c, "withdrawal failed: "+err.Error())
		return
	}
	response.OK(c, gin.H{"txHash": txHash, "memo": req.Memo, "label": label})
}

// GetBalance returns the XLM and USDC balance for the user's primary wallet
// GET /v1/wallets/balance
func (h *WalletHandler) GetBalance(c *gin.Context) {
	userID := middleware.GetUserID(c)
	bal, err := h.walletSvc.GetBalance(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to get balance")
		return
	}
	response.OK(c, gin.H{"balance": bal})
}

// DeleteWallet deletes a wallet by ID
// DELETE /v1/wallets/:id
func (h *WalletHandler) DeleteWallet(c *gin.Context) {
	userID := middleware.GetUserID(c)
	walletID := c.Param("id")
	if err := h.walletSvc.DeleteWallet(c.Request.Context(), userID, walletID); err != nil {
		response.InternalError(c, "failed to delete wallet")
		return
	}
	response.OK(c, gin.H{"success": true})
}
