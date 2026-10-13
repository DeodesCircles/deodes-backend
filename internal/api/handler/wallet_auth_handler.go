package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/deodes/backend/internal/domain/auth"
	"github.com/deodes/backend/internal/domain/user"
	"github.com/deodes/backend/pkg/apperrors"
	"github.com/deodes/backend/pkg/arc"
	"github.com/deodes/backend/pkg/response"
	"github.com/deodes/backend/pkg/stellar"
)

func isWalletAddress(addr string) bool {
	if err := stellar.ValidateAddress(addr); err == nil {
		return true
	}
	if err := arc.ValidateAddress(addr); err == nil {
		return true
	}
	return false
}

// WalletAuthHandler handles wallet-based authentication: obtaining a signed
// nonce and verifying a wallet signature to establish a session.
type WalletAuthHandler struct {
	authService auth.Service
	userService user.Service
}

// NewWalletAuthHandler builds a wallet authentication handler.
func NewWalletAuthHandler(authSvc auth.Service, userSvc user.Service) *WalletAuthHandler {
	return &WalletAuthHandler{authService: authSvc, userService: userSvc}
}

// @Summary Get authentication nonce
// @Description Returns a signed nonce for wallet authentication. The nonce must be signed with the wallet's private key and sent to /auth/verify.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Wallet address" { "walletAddress": "0x... or G..." }
// @Success 200 {object} response.Envelope{data=object{nonce=string}}
// @Failure 400 {object} response.Envelope
// @Router /auth/nonce [post]
func (h *WalletAuthHandler) Nonce(c *gin.Context) {
	var req struct {
		WalletAddress string `json:"walletAddress" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if !isWalletAddress(req.WalletAddress) {
		response.BadRequest(c, "invalid wallet address: must be Arc 0x or Stellar G...")
		return
	}

	nonce, err := h.authService.GenerateNonce(c.Request.Context(), req.WalletAddress)
	if err != nil {
		response.InternalError(c, "failed to generate nonce")
		return
	}
	response.OK(c, gin.H{"nonce": nonce})
}

// @Summary Verify wallet authentication
// @Description Verifies a signed nonce and creates a session.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Signature payload" { "walletAddress": "0x... or G...", "signature": "..." }
// @Success 200 {object} response.Envelope{data=object{token=string,refreshToken=string}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /auth/verify [post]
func (h *WalletAuthHandler) Verify(c *gin.Context) {
	var req struct {
		WalletAddress string `json:"walletAddress" binding:"required"`
		Signature     string `json:"signature" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if !isWalletAddress(req.WalletAddress) {
		response.BadRequest(c, "invalid wallet address: must be Arc 0x or Stellar G...")
		return
	}

	valid, err := h.authService.VerifySignature(c.Request.Context(), req.WalletAddress, req.Signature)
	if err != nil {
		// Verification couldn't complete (no/expired nonce, store error):
		// the client should retry with a fresh nonce, not the same payload.
		if errors.Is(err, apperrors.ErrNonceExpired) {
			response.Unauthorized(c, "signing session expired, please try again")
		} else {
			response.Unauthorized(c, "signature verification failed, please try again")
		}
		return
	}
	if !valid {
		// Completed check, wrong signer: wallet signed with a different
		// account than the nonce was issued for (or a contract wallet).
		response.Unauthorized(c, "signature does not match wallet address")
		return
	}

	// Auto-provision-or-login: wallet-first users have no email account yet.
	ctx := c.Request.Context()
	u, err := h.userService.GetByWallet(ctx, req.WalletAddress)
	if err != nil {
		created, createErr := h.userService.Create(ctx, req.WalletAddress)
		if createErr != nil {
			response.NotFound(c, "account not found")
			return
		}
		u = created
	}

	pair, err := h.authService.CreateSession(c.Request.Context(), u.ID, string(u.Role), sessionTTLFromUser(u), deviceInfoFromContext(c))
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}

	response.OK(c, gin.H{
		"token": pair.AccessToken, "refreshToken": pair.RefreshToken, "csrfToken": pair.CSRFToken, "user": u,
	})
}
