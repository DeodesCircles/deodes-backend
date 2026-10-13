package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/deodes/backend/internal/api/handler"
	"github.com/deodes/backend/internal/domain/auth"
	"github.com/deodes/backend/internal/domain/wallet"
)

// stubLinkAuthService embeds auth.Service so only VerifySignature needs an
// override; any unexpected call fails loudly instead of nil-panicking.
type stubLinkAuthService struct {
	auth.Service
	verify func(ctx context.Context, address, signature string) (bool, error)
}

func (s *stubLinkAuthService) VerifySignature(ctx context.Context, address, signature string) (bool, error) {
	if s.verify == nil {
		return false, fmt.Errorf("unexpected VerifySignature call")
	}
	return s.verify(ctx, address, signature)
}

func TestWalletHandler_Withdraw_WithoutPasskeySeed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := new(mockWalletService)
	svc.On(
		"SendPayment",
		mock.Anything,
		"user-123",
		mock.AnythingOfType("[]uint8"), // empty seed when omitted
		"GDEST...",
		"XLM",
		float64(10.5),
		"",
		mock.AnythingOfType("string"),
		mock.AnythingOfType("string"),
	).Return("txhash-wallet", nil)

	h := handler.NewWalletHandler(svc, &stubLinkAuthService{})
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-123")
		c.Next()
	})
	r.POST("/v1/wallets/withdraw", h.Withdraw)

	// No passkeySeed in the body — must not be rejected.
	body, _ := json.Marshal(map[string]any{
		"destination": "GDEST...",
		"asset":       "XLM",
		"amount":      10.5,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/wallets/withdraw", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "txhash-wallet")
	assert.NotContains(t, w.Body.String(), "passkeySeed")
	svc.AssertExpectations(t)
}

func TestWalletHandler_Withdraw_MemoAndLabel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	post := func(svc *mockWalletService, payload map[string]any) *httptest.ResponseRecorder {
		h := handler.NewWalletHandler(svc, &stubLinkAuthService{})
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("userID", "user-123"); c.Next() })
		r.POST("/v1/wallets/withdraw", h.Withdraw)
		body, _ := json.Marshal(payload)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/wallets/withdraw", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	base := func() map[string]any {
		return map[string]any{"destination": "GDEST...", "asset": "XLM", "amount": 1.0}
	}

	t.Run("memo and label are passed through and echoed", func(t *testing.T) {
		svc := new(mockWalletService)
		svc.On("SendPayment", mock.Anything, "user-123", mock.Anything, "GDEST...", "XLM", float64(1),
			"inv-42", mock.Anything, mock.Anything).Return("txhash-memo", nil)
		p := base()
		p["memo"], p["label"] = "inv-42", "  Rent  "

		w := post(svc, p)

		assert.Equal(t, 200, w.Code)
		assert.Contains(t, w.Body.String(), `"memo":"inv-42"`)
		assert.Contains(t, w.Body.String(), `"label":"Rent"`)
		svc.AssertExpectations(t)
	})

	t.Run("over-long memo is rejected before sending", func(t *testing.T) {
		svc := new(mockWalletService)
		p := base()
		p["memo"] = "this memo is far too long for stellar"

		w := post(svc, p)

		assert.Equal(t, 400, w.Code)
		svc.AssertNotCalled(t, "SendPayment")
	})
}

var _ wallet.Service = (*mockWalletService)(nil)

func TestWalletHandler_EnsureHostedArcWallet(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("returns existing hosted wallet without creating", func(t *testing.T) {
		svc := new(mockWalletService)
		existing := &wallet.Wallet{ID: "w-1", UserID: "user-123", PublicKey: "0xabc", WalletType: wallet.WalletTypeHosted}
		svc.On("GetWallets", mock.Anything, "user-123").Return([]wallet.Wallet{*existing}, nil)

		h := handler.NewWalletHandler(svc, &stubLinkAuthService{})
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("userID", "user-123"); c.Next() })
		r.POST("/v1/wallets/hosted-arc/ensure", h.EnsureHostedArcWallet)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/wallets/hosted-arc/ensure", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)
		assert.Contains(t, w.Body.String(), "0xabc")
		svc.AssertNotCalled(t, "CreateHostedArcWallet", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("creates hosted wallet when missing", func(t *testing.T) {
		svc := new(mockWalletService)
		svc.On("GetWallets", mock.Anything, "user-123").Return([]wallet.Wallet{}, nil)
		svc.On("DeriveWalletSeed", mock.Anything, "hosted-arc:user-123").Return("seedhex", nil)
		created := &wallet.Wallet{ID: "w-2", UserID: "user-123", PublicKey: "0xdef", WalletType: wallet.WalletTypeHosted}
		svc.On("CreateHostedArcWallet", mock.Anything, "user-123", mock.Anything).Return(created, nil)

		h := handler.NewWalletHandler(svc, &stubLinkAuthService{})
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("userID", "user-123"); c.Next() })
		r.POST("/v1/wallets/hosted-arc/ensure", h.EnsureHostedArcWallet)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/wallets/hosted-arc/ensure", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, 201, w.Code)
		assert.Contains(t, w.Body.String(), "0xdef")
		svc.AssertExpectations(t)
	})
}

const testLinkAddress = "0x742d35Cc6634C0532925a3b844Bc454e4438f44e"

func setupLinkRouter(svc *mockWalletService, authSvc auth.Service) *gin.Engine {
	h := handler.NewWalletHandler(svc, authSvc)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-123"); c.Next() })
	r.POST("/v1/wallets/link", h.LinkWallet)
	return r
}

func postLink(t *testing.T, r *gin.Engine, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/wallets/link", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func linkVerifyOK() *stubLinkAuthService {
	return &stubLinkAuthService{verify: func(ctx context.Context, address, sig string) (bool, error) {
		return true, nil
	}}
}

func TestWalletHandler_LinkWallet(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("links a new address with 201", func(t *testing.T) {
		svc := new(mockWalletService)
		svc.On("GetWalletByAddress", mock.Anything, testLinkAddress).Return(nil, nil)
		svc.On("GetWallets", mock.Anything, "user-123").Return([]wallet.Wallet{}, nil)
		linked := &wallet.Wallet{ID: "w-link-1", UserID: "user-123", PublicKey: "0x742d35cc6634c0532925a3b844bc454e4438f44e", WalletType: wallet.WalletTypeLinked}
		svc.On("LinkWallet", mock.Anything, "user-123", testLinkAddress).Return(linked, nil)

		w := postLink(t, setupLinkRouter(svc, linkVerifyOK()), map[string]any{
			"address": testLinkAddress, "signature": "0xdeadbeef",
		})

		assert.Equal(t, 201, w.Code)
		assert.Contains(t, w.Body.String(), "w-link-1")
		assert.Contains(t, w.Body.String(), `"walletType":"linked"`)
		svc.AssertExpectations(t)
	})

	t.Run("rejects an invalid address with 400 before verifying", func(t *testing.T) {
		svc := new(mockWalletService)
		called := false
		authSvc := &stubLinkAuthService{verify: func(ctx context.Context, address, sig string) (bool, error) {
			called = true
			return true, nil
		}}

		w := postLink(t, setupLinkRouter(svc, authSvc), map[string]any{
			"address": "not-an-address", "signature": "0xdeadbeef",
		})

		assert.Equal(t, 400, w.Code)
		assert.False(t, called, "VerifySignature must not run for a malformed address")
		svc.AssertNotCalled(t, "LinkWallet", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("bad signature yields 401 without linking", func(t *testing.T) {
		svc := new(mockWalletService)
		authSvc := &stubLinkAuthService{verify: func(ctx context.Context, address, sig string) (bool, error) {
			return false, nil
		}}

		w := postLink(t, setupLinkRouter(svc, authSvc), map[string]any{
			"address": testLinkAddress, "signature": "0xbad",
		})

		assert.Equal(t, 401, w.Code)
		svc.AssertNotCalled(t, "GetWalletByAddress", mock.Anything, mock.Anything)
		svc.AssertNotCalled(t, "LinkWallet", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("verify error yields 401 without linking", func(t *testing.T) {
		svc := new(mockWalletService)
		authSvc := &stubLinkAuthService{verify: func(ctx context.Context, address, sig string) (bool, error) {
			return false, fmt.Errorf("nonce expired")
		}}

		w := postLink(t, setupLinkRouter(svc, authSvc), map[string]any{
			"address": testLinkAddress, "signature": "0xbad",
		})

		assert.Equal(t, 401, w.Code)
		svc.AssertNotCalled(t, "LinkWallet", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("re-linking your own address is idempotent 200", func(t *testing.T) {
		svc := new(mockWalletService)
		existing := &wallet.Wallet{ID: "w-link-1", UserID: "user-123", PublicKey: "0x742d35cc6634c0532925a3b844bc454e4438f44e", WalletType: wallet.WalletTypeLinked}
		svc.On("GetWalletByAddress", mock.Anything, testLinkAddress).Return(existing, nil)

		w := postLink(t, setupLinkRouter(svc, linkVerifyOK()), map[string]any{
			"address": testLinkAddress, "signature": "0xdeadbeef",
		})

		assert.Equal(t, 200, w.Code)
		assert.Contains(t, w.Body.String(), "w-link-1")
		svc.AssertNotCalled(t, "LinkWallet", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("address linked to another user yields 409", func(t *testing.T) {
		svc := new(mockWalletService)
		taken := &wallet.Wallet{ID: "w-other", UserID: "user-999", PublicKey: "0x742d35cc6634c0532925a3b844bc454e4438f44e", WalletType: wallet.WalletTypeLinked}
		svc.On("GetWalletByAddress", mock.Anything, testLinkAddress).Return(taken, nil)

		w := postLink(t, setupLinkRouter(svc, linkVerifyOK()), map[string]any{
			"address": testLinkAddress, "signature": "0xdeadbeef",
		})

		assert.Equal(t, 409, w.Code)
		svc.AssertNotCalled(t, "LinkWallet", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a second different address for the same user yields 409", func(t *testing.T) {
		svc := new(mockWalletService)
		other := "0x0000000000000000000000000000000000000001"
		svc.On("GetWalletByAddress", mock.Anything, other).Return(nil, nil)
		mine := wallet.Wallet{ID: "w-link-1", UserID: "user-123", PublicKey: "0x742d35cc6634c0532925a3b844bc454e4438f44e", WalletType: wallet.WalletTypeLinked}
		svc.On("GetWallets", mock.Anything, "user-123").Return([]wallet.Wallet{mine}, nil)

		w := postLink(t, setupLinkRouter(svc, linkVerifyOK()), map[string]any{
			"address": other, "signature": "0xdeadbeef",
		})

		assert.Equal(t, 409, w.Code)
		svc.AssertNotCalled(t, "LinkWallet", mock.Anything, mock.Anything, mock.Anything)
	})
}
