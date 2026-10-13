package wallet

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stellar/go/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/pkg/crypto"
)

// newTestService builds a wallet service for unit tests. DeriveWalletSeed does
// not touch the repository or Horizon, so a nil repo and a throwaway master
// keypair suffice.
func newTestService(t *testing.T, cfg Config) Service {
	t.Helper()
	kp, err := keypair.Random()
	require.NoError(t, err)
	if cfg.MasterSecretKey == "" {
		cfg.MasterSecretKey = kp.Seed()
	}
	svc, err := NewService(nil, cfg)
	require.NoError(t, err)
	return svc
}

func TestDeriveWalletSeed_MissingPepper(t *testing.T) {
	svc := newTestService(t, Config{})

	seed, err := svc.DeriveWalletSeed(context.Background(), "user@example.com")
	assert.Error(t, err)
	assert.Empty(t, seed)
	assert.Contains(t, err.Error(), "wallet pepper is not configured")
}

func TestDeriveWalletSeed_DeterministicAndUniquePerEmail(t *testing.T) {
	svc := newTestService(t, Config{
		WalletPepper:  "test-secret-pepper-123",
		Argon2Time:    1,
		Argon2Memory:  64 * 1024,
		Argon2Threads: 4,
	})

	seed, err := svc.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.Len(t, seed, 64) // 32-byte key, hex-encoded

	// Deterministic: the same email always derives the same seed.
	seed2, err := svc.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.Equal(t, seed, seed2)

	// Unique per email: the email participates in the salt.
	seedOther, err := svc.DeriveWalletSeed(context.Background(), "other@example.com")
	require.NoError(t, err)
	assert.NotEqual(t, seed, seedOther)
}

func TestDeriveWalletSeed_DifferentPepperChangesSeed(t *testing.T) {
	svcA := newTestService(t, Config{WalletPepper: "pepper-a", Argon2Time: 1})
	svcB := newTestService(t, Config{WalletPepper: "pepper-b", Argon2Time: 1})

	seedA, err := svcA.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	seedB, err := svcB.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.NotEqual(t, seedA, seedB)
}

func TestDeriveWalletSeed_DefaultsWhenParamsZero(t *testing.T) {
	// Zero argon2 params fall back to the conservative defaults rather than
	// failing or producing degenerate output.
	svc := newTestService(t, Config{WalletPepper: "pepper"})

	seed, err := svc.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.Len(t, seed, 64)
}

func TestCreateWallet_HexSeedMatchesRawSeed(t *testing.T) {
	// Legacy callers pass []byte(hexString); the hex bug copied ASCII bytes.
	// Both forms must now derive the same Stellar address.
	svc := newTestService(t, Config{})
	hexSeed := "81362d66c26500a3ffca1259323cfe1bc3b59658b65d6d534aaee0cb4eabf904"
	raw, err := crypto.DecodeSeed32(hexSeed)
	require.NoError(t, err)

	wHex, err := svc.CreateWallet(context.Background(), "u1", []byte(hexSeed))
	require.NoError(t, err)
	wRaw, err := svc.CreateWallet(context.Background(), "u1", raw[:])
	require.NoError(t, err)
	assert.Equal(t, wRaw.PublicKey, wHex.PublicKey)
	assert.True(t, strings.HasPrefix(wHex.PublicKey, "G"))
}

func TestCreateHostedArcWallet_DeterministicEncrypted(t *testing.T) {
	encKey := strings.Repeat("ab", 32) // 64 hex chars = 32B
	svc := newTestService(t, Config{EncryptionKey: encKey})
	hexSeed := "81362d66c26500a3ffca1259323cfe1bc3b59658b65d6d534aaee0cb4eabf904"

	w1, err := svc.CreateHostedArcWallet(context.Background(), "u1", []byte(hexSeed))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(w1.PublicKey, "0x"))
	assert.Len(t, w1.PublicKey, 42)
	assert.Equal(t, WalletTypeHosted, w1.WalletType)
	assert.NotEmpty(t, w1.EncryptedSecretKey)
	assert.NotEmpty(t, w1.EncryptionNonce)

	// Same seed → same address (new row id, same key).
	w2, err := svc.CreateHostedArcWallet(context.Background(), "u1", []byte(hexSeed))
	require.NoError(t, err)
	assert.Equal(t, w1.PublicKey, w2.PublicKey)

	// Secret decrypts with the configured key.
	key, err := crypto.ParseEncryptionKey(encKey)
	require.NoError(t, err)
	plain, err := w1.DecryptSecret(key)
	require.NoError(t, err)
	assert.Len(t, plain, 32)
}

func TestCreateHostedArcWallet_RequiresEncryptionKey(t *testing.T) {
	svc := newTestService(t, Config{})
	_, err := svc.CreateHostedArcWallet(context.Background(), "u1", []byte(strings.Repeat("a", 32)))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "encryption key")
}

// fakeArcRPC serves canned Arc JSON-RPC for wallet send/balance tests.
type fakeArcRPC struct {
	t       *testing.T
	balance string // hex uint256 for eth_call
}

func (f *fakeArcRPC) handler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string `json:"method"`
	}
	require.NoError(f.t, json.NewDecoder(r.Body).Decode(&req))
	var result string
	switch req.Method {
	case "eth_chainId":
		result = `"0x4cef52"` // 5042002
	case "eth_getTransactionCount":
		result = `"0x0"`
	case "eth_estimateGas":
		result = `"0xfde8"`
	case "eth_getBlockByNumber":
		result = `{"baseFeePerGas":"0x3b9aca00"}`
	case "eth_sendRawTransaction":
		result = `"0xdeadbeef"`
	case "eth_call":
		result = f.balance
	default:
		http.Error(w, "unknown method", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%s}`, result)
}

// stubRepo is an in-memory Repository for send/balance tests.
type stubRepo struct {
	wallets   []Wallet
	allowed   bool
	audited   []*WithdrawalRecord
	createErr error
}

func (s *stubRepo) Create(ctx context.Context, w *Wallet) error { return s.createErr }
func (s *stubRepo) FindByID(ctx context.Context, id string) (*Wallet, error) {
	return nil, fmt.Errorf("not found")
}
func (s *stubRepo) FindByUserID(ctx context.Context, userID string) ([]Wallet, error) {
	return s.wallets, nil
}
func (s *stubRepo) FindByPublicKey(ctx context.Context, publicKey string) (*Wallet, error) {
	for i := range s.wallets {
		if strings.EqualFold(s.wallets[i].PublicKey, publicKey) {
			return &s.wallets[i], nil
		}
	}
	return nil, fmt.Errorf("finding wallet by public key: %w", sql.ErrNoRows)
}
func (s *stubRepo) Delete(ctx context.Context, id string) error                      { return nil }
func (s *stubRepo) DeleteByOwner(ctx context.Context, walletID, userID string) error { return nil }
func (s *stubRepo) CheckRateLimit(ctx context.Context, userID uuid.UUID) (bool, error) {
	return s.allowed, nil
}
func (s *stubRepo) IncrementRateLimit(ctx context.Context, userID uuid.UUID) error { return nil }
func (s *stubRepo) GetDailySpending(ctx context.Context, userID uuid.UUID) (float64, error) {
	return 0, nil
}
func (s *stubRepo) RecordWithdrawalAudit(ctx context.Context, rec *WithdrawalRecord) error {
	s.audited = append(s.audited, rec)
	return nil
}

const (
	testEncKey = "abababababababababababababababababababababababababababababababab"
	testSeed   = "81362d66c26500a3ffca1259323cfe1bc3b59658b65d6d534aaee0cb4eabf904"
)

func newHostedWalletFixture(t *testing.T) Wallet {
	t.Helper()
	arcSeed, err := crypto.DeriveArcSeed(testSeed)
	require.NoError(t, err)
	addr, priv, err := crypto.EVMAddressFromSeed(arcSeed)
	require.NoError(t, err)
	key, err := crypto.ParseEncryptionKey(testEncKey)
	require.NoError(t, err)
	ct, nonce, err := crypto.Encrypt(priv, key)
	require.NoError(t, err)
	return Wallet{
		ID: "w-hosted-1", UserID: "11111111-1111-1111-1111-111111111111", PublicKey: addr,
		EncryptedSecretKey: ct, EncryptionNonce: nonce,
		WalletType: WalletTypeHosted, IsPrimary: true,
	}
}

func newArcTestService(t *testing.T, rpcURL string, repo Repository) Service {
	t.Helper()
	kp, err := keypair.Random()
	require.NoError(t, err)
	svc, err := NewService(repo, Config{
		MasterSecretKey: kp.Seed(),
		EncryptionKey:   testEncKey,
		ArcRPCURL:       rpcURL,
		ArcChainID:      5042002,
		ArcUSDCContract: "0x0000000000000000000000000000000000000001",
	})
	require.NoError(t, err)
	return svc
}

func TestSendPayment_HostedArcSuccess(t *testing.T) {
	bal := new(big.Int)
	bal.SetString("5000000", 10)
	srv := httptest.NewServer(http.HandlerFunc((&fakeArcRPC{
		t:       t,
		balance: fmt.Sprintf(`"0x%064x"`, bal),
	}).handler))
	defer srv.Close()

	repo := &stubRepo{wallets: []Wallet{newHostedWalletFixture(t)}, allowed: true}
	svc := newArcTestService(t, srv.URL, repo)

	hash, err := svc.SendPayment(context.Background(), "11111111-1111-1111-1111-111111111111", nil,
		"0x742d35Cc6634C0532925a3b844Bc454e4438f44e", "USDC", 1.25, "", "127.0.0.1", "test")
	require.NoError(t, err)
	assert.Equal(t, "0xdeadbeef", hash)
	require.Len(t, repo.audited, 1)
	assert.Equal(t, "completed", repo.audited[0].Status)
	assert.Equal(t, "0xdeadbeef", repo.audited[0].TxHash)
}

func TestSendPayment_Validation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc((&fakeArcRPC{t: t, balance: `"0x0"`}).handler))
	defer srv.Close()
	repo := &stubRepo{wallets: []Wallet{newHostedWalletFixture(t)}, allowed: true}
	svc := newArcTestService(t, srv.URL, repo)
	ctx := context.Background()

	_, err := svc.SendPayment(ctx, "11111111-1111-1111-1111-111111111111", nil, "0x742d35Cc6634C0532925a3b844Bc454e4438f44e", "USDC", -5, "", "", "")
	assert.Error(t, err)

	_, err = svc.SendPayment(ctx, "11111111-1111-1111-1111-111111111111", nil, "0x742d35Cc6634C0532925a3b844Bc454e4438f44e", "XLM", 5, "", "", "")
	assert.ErrorContains(t, err, "unsupported asset")

	_, err = svc.SendPayment(ctx, "11111111-1111-1111-1111-111111111111", nil, "not-an-address", "USDC", 5, "", "", "")
	assert.Error(t, err)
}

func TestSendPayment_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc((&fakeArcRPC{t: t, balance: `"0x0"`}).handler))
	defer srv.Close()
	repo := &stubRepo{wallets: []Wallet{newHostedWalletFixture(t)}, allowed: false}
	svc := newArcTestService(t, srv.URL, repo)

	_, err := svc.SendPayment(context.Background(), "11111111-1111-1111-1111-111111111111", nil,
		"0x742d35Cc6634C0532925a3b844Bc454e4438f44e", "USDC", 5, "", "127.0.0.1", "test")
	assert.ErrorContains(t, err, "rate limit")
	require.Len(t, repo.audited, 1)
	assert.Equal(t, "failed", repo.audited[0].Status)
}

func TestSendPayment_NoHostedWallet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc((&fakeArcRPC{t: t, balance: `"0x0"`}).handler))
	defer srv.Close()
	svc := newArcTestService(t, srv.URL, &stubRepo{allowed: true})

	_, err := svc.SendPayment(context.Background(), "11111111-1111-1111-1111-111111111111", nil,
		"0x742d35Cc6634C0532925a3b844Bc454e4438f44e", "USDC", 5, "", "", "")
	assert.ErrorContains(t, err, "no hosted Arc wallet")
}

func TestGetBalance_HostedArc(t *testing.T) {
	bal := new(big.Int)
	bal.SetString("2500000", 10) // 2.5 USDC in ERC-20 base units (6 decimals)
	srv := httptest.NewServer(http.HandlerFunc((&fakeArcRPC{
		t:       t,
		balance: fmt.Sprintf(`"0x%064x"`, bal),
	}).handler))
	defer srv.Close()
	repo := &stubRepo{wallets: []Wallet{newHostedWalletFixture(t)}, allowed: true}
	svc := newArcTestService(t, srv.URL, repo)

	b, err := svc.GetBalance(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, 2.5, b.USDC)
	assert.Equal(t, 0.0, b.XLM)
}

func TestLinkWallet_PersistsSelfCustodyRecord(t *testing.T) {
	svc := newTestService(t, Config{})

	w, err := svc.LinkWallet(context.Background(), "u1", "0x742d35Cc6634C0532925a3b844Bc454e4438f44e")
	require.NoError(t, err)
	assert.Equal(t, "u1", w.UserID)
	// Checksum-case input is normalized to the stored lowercase form.
	assert.Equal(t, "0x742d35cc6634c0532925a3b844bc454e4438f44e", w.PublicKey)
	assert.Equal(t, WalletTypeLinked, w.WalletType)
	assert.False(t, w.IsPrimary)
	assert.Empty(t, w.EncryptedSecretKey)
	assert.Empty(t, w.EncryptionNonce)
}

func TestLinkWallet_RejectsInvalidAddress(t *testing.T) {
	svc := newTestService(t, Config{})

	for _, addr := range []string{"", "not-an-address", "GABCDEF123", "0x1234"} {
		_, err := svc.LinkWallet(context.Background(), "u1", addr)
		assert.Error(t, err, "address %q must be rejected", addr)
	}
}

func TestGetWalletByAddress_FoundMissingAndNilRepo(t *testing.T) {
	linked := Wallet{ID: "w-link-1", UserID: "u1", PublicKey: "0x742d35cc6634c0532925a3b844bc454e4438f44e", WalletType: WalletTypeLinked}
	kp, err := keypair.Random()
	require.NoError(t, err)
	svc, err := NewService(&stubRepo{wallets: []Wallet{linked}}, Config{MasterSecretKey: kp.Seed()})
	require.NoError(t, err)

	// Checksum-case lookup still finds the lowercase stored row.
	found, err := svc.GetWalletByAddress(context.Background(), "0x742d35Cc6634C0532925a3b844Bc454e4438f44e")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "w-link-1", found.ID)

	missing, err := svc.GetWalletByAddress(context.Background(), "0x0000000000000000000000000000000000000001")
	require.NoError(t, err)
	assert.Nil(t, missing)

	nilRepoSvc := newTestService(t, Config{})
	none, err := nilRepoSvc.GetWalletByAddress(context.Background(), "0x742d35Cc6634C0532925a3b844Bc454e4438f44e")
	require.NoError(t, err)
	assert.Nil(t, none)
}
