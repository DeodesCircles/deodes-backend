package wallet

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/deodes/backend/pkg/arc"
	"github.com/deodes/backend/pkg/crypto"
	"github.com/deodes/backend/pkg/logger"
	"github.com/deodes/backend/pkg/money"
)

type Service interface {
	DeriveWalletSeed(ctx context.Context, email string) (string, error)
	CreateWallet(ctx context.Context, userID string, passkeySeed []byte) (*Wallet, error)
	// CreateHostedArcWallet derives, encrypts and persists a custodial Arc
	// (EVM 0x) wallet under us. One hosted wallet per user (UNIQUE on
	// user_id+wallet_type). Requires EncryptionKey; legacy wallets stay
	// backward compatible without it.
	CreateHostedArcWallet(ctx context.Context, userID string, passkeySeed []byte) (*Wallet, error)
	// LinkWallet persists a self-custody Arc (EVM 0x) wallet link for userID.
	// Ownership must already be proven by the caller via a signed-nonce
	// verify; this only validates the address format and stores the record
	// with no secret (EncryptedSecretKey/Nonce stay empty, IsPrimary false).
	// One linked wallet per user (UNIQUE on user_id+wallet_type); the address
	// itself is globally unique (UNIQUE on public_key).
	LinkWallet(ctx context.Context, userID, address string) (*Wallet, error)
	// GetWalletByAddress returns the wallet row for a public key, or
	// (nil, nil) when no wallet is linked to that address.
	GetWalletByAddress(ctx context.Context, address string) (*Wallet, error)
	SignTransaction(ctx context.Context, walletID string, passkeySeed []byte, txnXDR string) (string, error)
	GetWallets(ctx context.Context, userID string) ([]Wallet, error)
	GetBalance(ctx context.Context, userID string) (*Balance, error)
	SendPayment(ctx context.Context, userID string, passkeySeed []byte, destination, asset string, amount float64, memo, ipAddress, userAgent string) (string, error)
	DeleteWallet(ctx context.Context, userID, walletID string) error
}

type Config struct {
	WalletPepper  string
	Argon2Time    int
	Argon2Memory  int
	Argon2Threads int
	// EncryptionKey is the hex-encoded 32-byte AES key for custodial secret
	// storage (security.encryption_key, required in prod).
	EncryptionKey string
	// Arc chain settings for hosted 0x wallets (arc.rpc_url/chain_id/usdc_contract).
	ArcRPCURL       string
	ArcChainID      uint64
	ArcUSDCContract string
}

type service struct {
	repo Repository
	cfg  Config
	arc  *arc.Client
}

func NewService(repo Repository, cfg Config) (Service, error) {
	chainID := cfg.ArcChainID
	if chainID == 0 {
		chainID = arc.ChainIDTestnet
	}
	return &service{
		repo: repo,
		cfg:  cfg,
		arc:  arc.NewClient(cfg.ArcRPCURL, chainID),
	}, nil
}

func (s *service) DeriveWalletSeed(ctx context.Context, email string) (string, error) {
	// Deterministic Argon2id key derivation lives in pkg/crypto; the service
	// only supplies the configured pepper and derivation parameters.
	return crypto.DeriveWalletSeed(email, s.cfg.WalletPepper, s.cfg.Argon2Time, s.cfg.Argon2Memory, s.cfg.Argon2Threads)
}

func (s *service) CreateWallet(ctx context.Context, userID string, passkeySeed []byte) (*Wallet, error) {
	rawSeed, err := decodeSeedInput(passkeySeed)
	if err != nil {
		l := logger.Ctx(ctx)
		l.Error().Err(err).Str("userID", userID).Msg("invalid wallet seed input")
		return nil, err
	}
	_, addr := crypto.Ed25519AddressFromSeed(rawSeed)

	walletID := uuid.New().String()
	w := &Wallet{
		ID:                 walletID,
		UserID:             userID,
		PublicKey:          addr,
		WalletType:         WalletTypeAuto,
		IsPrimary:          true,
		EncryptedSecretKey: []byte{},
		EncryptionNonce:    []byte{},
	}

	// Persist the secret encrypted when an encryption key is configured.
	// Legacy callers without a key keep watch-only semantics, but store
	// empty (non-nil) bytea so the NOT NULL columns never receive NULL.
	if key, err := crypto.ParseEncryptionKey(strings.TrimSpace(s.cfg.EncryptionKey)); err == nil {
		ct, nonce, err := crypto.Encrypt(rawSeed[:], key)
		if err == nil {
			w.EncryptedSecretKey = ct
			w.EncryptionNonce = nonce
		}
	}

	if s.repo != nil {
		if err := s.repo.Create(ctx, w); err != nil {
			return nil, err
		}
	}

	return w,
		nil
}

// decodeSeedInput normalizes a wallet seed input into 32 raw bytes.
// New callers pass raw 32B. Legacy callers passed []byte(hexString) (64 ASCII
// chars) — detected and hex-decoded so the derived key matches the intended
// derivation instead of ASCII bytes.
func decodeSeedInput(in []byte) ([32]byte, error) {
	var out [32]byte
	s := strings.TrimSpace(string(in))
	if decoded, err := hex.DecodeString(s); err == nil && len(decoded) == 32 {
		copy(out[:], decoded)
		return out, nil
	}
	if len(in) < 32 {
		return out, fmt.Errorf("seed input must be at least 32 bytes, got %d", len(in))
	}
	copy(out[:], in[:32])
	return out, nil
}

// CreateHostedArcWallet derives (chain-separated), encrypts and persists a
// custodial Arc EVM wallet for userID. The 0x address is returned; the
// private key is encrypted with EncryptionKey and never logged.
// Requires cfg.EncryptionKey (hex 32B) — production always sets it.
func (s *service) CreateHostedArcWallet(ctx context.Context, userID string, passkeySeed []byte) (*Wallet, error) {
	raw, err := decodeSeedInput(passkeySeed)
	if err != nil {
		return nil, err
	}
	baseHex := hex.EncodeToString(raw[:])
	arcSeed, err := crypto.DeriveArcSeed(baseHex)
	if err != nil {
		return nil, err
	}
	address, privKey, err := crypto.EVMAddressFromSeed(arcSeed)
	if err != nil {
		return nil, err
	}

	key, err := crypto.ParseEncryptionKey(strings.TrimSpace(s.cfg.EncryptionKey))
	if err != nil {
		return nil, fmt.Errorf("hosted wallets require a configured encryption key: %w", err)
	}
	ct, nonce, err := crypto.Encrypt(privKey, key)
	if err != nil {
		return nil, fmt.Errorf("encrypting hosted secret: %w", err)
	}

	w := &Wallet{
		ID:                 uuid.New().String(),
		UserID:             userID,
		PublicKey:          address,
		EncryptedSecretKey: ct,
		EncryptionNonce:    nonce,
		WalletType:         WalletTypeHosted,
		IsPrimary:          true,
	}
	if s.repo != nil {
		if err := s.repo.Create(ctx, w); err != nil {
			return nil, err
		}
	}
	// Clear key material copies; the persisted ciphertext + caller's seed remain.
	for i := range privKey {
		privKey[i] = 0
	}
	return w, nil
}

// normalizeArcAddress trims and lowercases an Arc 0x address so checksum-case
// variants map to the single stored form (hosted addresses are already stored
// lowercase hex). Callers must validate with arc.ValidateAddress first.
func normalizeArcAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// LinkWallet validates an Arc 0x address and persists a self-custody link
// record: no secret is derived or stored, and the wallet is never primary.
func (s *service) LinkWallet(ctx context.Context, userID, address string) (*Wallet, error) {
	normalized := normalizeArcAddress(address)
	if err := arc.ValidateAddress(normalized); err != nil {
		return nil, err
	}
	w := &Wallet{
		ID:                 uuid.New().String(),
		UserID:             userID,
		PublicKey:          normalized,
		EncryptedSecretKey: []byte{},
		EncryptionNonce:    []byte{},
		WalletType:         WalletTypeLinked,
		IsPrimary:          false,
	}
	if s.repo != nil {
		if err := s.repo.Create(ctx, w); err != nil {
			return nil, err
		}
	}
	return w, nil
}

// GetWalletByAddress looks up the wallet row for a public key (EVM case
// normalized before lookup). Returns (nil, nil) when the address is not
// linked to any wallet.
func (s *service) GetWalletByAddress(ctx context.Context, address string) (*Wallet, error) {
	if s.repo == nil {
		return nil, nil
	}
	w, err := s.repo.FindByPublicKey(ctx, normalizeArcAddress(address))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return w, nil
}

func (s *service) SignTransaction(ctx context.Context, walletID string, passkeySeed []byte, txnXDR string) (string, error) {
	return "", nil
}

func (s *service) GetWallets(ctx context.Context, userID string) ([]Wallet, error) {
	if s.repo == nil {
		return nil, nil
	}
	return s.repo.FindByUserID(ctx, userID)
}

func (s *service) GetBalance(ctx context.Context, userID string) (*Balance, error) {
	wallets, err := s.GetWallets(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(wallets) == 0 {
		return nil, fmt.Errorf("no wallet found. Create a wallet first.")
	}
	for _, w := range wallets {
		if strings.HasPrefix(w.PublicKey, "0x") {
			human, err := arc.USDCBalanceOf(ctx, s.arc, s.cfg.ArcUSDCContract, w.PublicKey)
			if err != nil {
				return nil, err
			}
			f, err := strconv.ParseFloat(human, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing balance: %w", err)
			}
			return &Balance{XLM: 0, USDC: f}, nil
		}
	}
	// Legacy (non-0x) wallets predate the Arc migration: no chain left to
	// read them from. Surface a migration error, not a chain call.
	return nil, fmt.Errorf("legacy wallet %s is balance-unavailable post-migration (see Phase 2.11)", wallets[0].PublicKey)
}

func (s *service) SendPayment(ctx context.Context, userID string, passkeySeed []byte, destination, asset string, amount float64, memo, ipAddress, userAgent string) (string, error) {
	amt, err := money.FromFloat64(amount)
	if err != nil {
		return "", fmt.Errorf("invalid amount: %w", err)
	}
	if !amt.IsPositive() {
		return "", fmt.Errorf("amount must be positive")
	}
	if asset != "" && asset != "USDC" {
		return "", fmt.Errorf("unsupported asset %q: hosted sends settle in USDC on Arc", asset)
	}
	wallets, err := s.GetWallets(ctx, userID)
	if err != nil {
		return "", err
	}
	var hosted *Wallet
	for i := range wallets {
		if wallets[i].WalletType == WalletTypeHosted && strings.HasPrefix(wallets[i].PublicKey, "0x") {
			hosted = &wallets[i]
			break
		}
	}
	if hosted == nil {
		return "", fmt.Errorf("no hosted Arc wallet found. Create one first.")
	}
	key, err := crypto.ParseEncryptionKey(strings.TrimSpace(s.cfg.EncryptionKey))
	if err != nil {
		return "", fmt.Errorf("hosted sends require a configured encryption key: %w", err)
	}
	_ = passkeySeed // Reserved for future step-up auth binding; decrypt uses the server key.
	_ = memo        // EVM USDC transfers carry no memo field.
	secret, err := hosted.DecryptSecret(key)
	if err != nil {
		return "", err
	}
	privKey := []byte(secret)
	if len(privKey) != 32 {
		return "", fmt.Errorf("hosted secret has invalid length %d", len(privKey))
	}

	uid, parseErr := uuid.Parse(userID)
	if s.repo != nil && parseErr != nil {
		return "", fmt.Errorf("invalid user id for audited send")
	}
	if s.repo != nil {
		allowed, err := s.repo.CheckRateLimit(ctx, uid)
		if err != nil {
			return "", err
		}
		if !allowed {
			s.auditWithdrawal(ctx, uid, destination, asset, amt.String(), ipAddress, userAgent, "", "failed", "rate limited")
			return "", fmt.Errorf("withdrawal rate limit exceeded: max 3 per hour")
		}
		if err := s.repo.IncrementRateLimit(ctx, uid); err != nil {
			return "", err
		}
	}

	// Quantize to ERC-20 precision BEFORE touching the chain: Money carries
	// 7 decimals, the interface settles 6. Dust below $0.000001 stays in the
	// wallet; everything recorded below uses the floored (actual) value.
	sendAmount, err := arc.FloorToERC20(amt.String())
	if err != nil {
		return "", fmt.Errorf("invalid amount: %w", err)
	}

	txHash, err := arc.USDCTransfer(ctx, s.arc, s.cfg.ArcUSDCContract, privKey, destination, sendAmount)
	for i := range privKey {
		privKey[i] = 0
	}
	if s.repo != nil {
		status, failure := "completed", ""
		if err != nil {
			status, failure = "failed", err.Error()
		}
		s.auditWithdrawal(ctx, uid, destination, asset, sendAmount, ipAddress, userAgent, txHash, status, failure)
	}
	if err != nil {
		return "", err
	}
	return txHash, nil
}

func (s *service) auditWithdrawal(ctx context.Context, userID uuid.UUID, destination, asset, amount, ip, agent, txHash, status, failure string) {
	rec := &WithdrawalRecord{
		ID:          uuid.New(),
		UserID:      userID,
		Destination: destination,
		Asset:       asset,
		Amount:      mustParseAmount(amount),
		Status:      status,
		IPAddress:   ip,
		UserAgent:   agent,
		Failure:     failure,
		TxHash:      txHash,
		CreatedAt:   time.Now(),
	}
	_ = s.repo.RecordWithdrawalAudit(ctx, rec)
}

func mustParseAmount(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func (s *service) DeleteWallet(ctx context.Context, userID, walletID string) error {
	if s.repo == nil {
		return nil
	}
	return s.repo.DeleteByOwner(ctx, walletID, userID)
}
