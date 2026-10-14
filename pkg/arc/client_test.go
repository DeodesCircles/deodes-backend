package arc

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRPC serves canned JSON-RPC responses keyed by method.
type mockRPC struct {
	t       *testing.T
	methods map[string]func() string
}

func (m *mockRPC) handler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string `json:"method"`
	}
	require.NoError(m.t, json.NewDecoder(r.Body).Decode(&req))
	fn, ok := m.methods[req.Method]
	if !ok {
		http.Error(w, "unknown method", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%s}`, fn())
}

func TestClient_ChainIDMatchAndMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc((&mockRPC{
		t: t,
		methods: map[string]func() string{
			"eth_chainId": func() string { return `"0x4cef52"` }, // 5042002
		},
	}).handler))
	defer srv.Close()

	c := NewClient(srv.URL, ChainIDTestnet)
	id, err := c.ChainID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, ChainIDTestnet, id)

	cMain := NewClient(srv.URL, ChainIDMainnet)
	_, err = cMain.ChainID(context.Background())
	assert.ErrorContains(t, err, "chain mismatch")
}

func TestClient_USDCBalanceOf(t *testing.T) {
	// 2.5 USDC in ERC-20 base units (6 decimals), 32-byte padded.
	bal := new(big.Int)
	bal.SetString("2500000", 10)
	hexBal := fmt.Sprintf(`"0x%064x"`, bal)
	srv := httptest.NewServer(http.HandlerFunc((&mockRPC{
		t: t,
		methods: map[string]func() string{
			"eth_call": func() string { return hexBal },
		},
	}).handler))
	defer srv.Close()

	c := NewClient(srv.URL, ChainIDTestnet)
	got, err := USDCBalanceOf(context.Background(), c,
		"0x0000000000000000000000000000000000000001",
		"0x742d35Cc6634C0532925a3b844Bc454e4438f44e")
	require.NoError(t, err)
	assert.Equal(t, "2.500000", got)
}

func TestQuoteFees_FloorEnforced(t *testing.T) {
	// Base fee 1 Gwei: 2*1+1 = 3 Gwei < 20 floor -> 20 Gwei.
	srv := httptest.NewServer(http.HandlerFunc((&mockRPC{
		t: t,
		methods: map[string]func() string{
			"eth_getBlockByNumber": func() string { return `{"baseFeePerGas":"0x3b9aca00"}` },
		},
	}).handler))
	defer srv.Close()

	c := NewClient(srv.URL, ChainIDTestnet)
	q, err := QuoteFees(context.Background(), c)
	require.NoError(t, err)
	assert.Equal(t, uint64(MinMaxFeePerGasWei), q.MaxFeePerGas.Uint64())
}

func TestQuoteFees_HighBasePassesThrough(t *testing.T) {
	// Base fee 30 Gwei: 2*30+1 = 61 Gwei > floor.
	srv := httptest.NewServer(http.HandlerFunc((&mockRPC{
		t: t,
		methods: map[string]func() string{
			"eth_getBlockByNumber": func() string { return `{"baseFeePerGas":"0x6fc23ac00"}` },
		},
	}).handler))
	defer srv.Close()

	c := NewClient(srv.URL, ChainIDTestnet)
	q, err := QuoteFees(context.Background(), c)
	require.NoError(t, err)
	assert.Equal(t, "61000000000", q.MaxFeePerGas.String())
}

func TestUSDCTransfer_FullPath(t *testing.T) {
	var seenNonce, seenGas, seenTx string
	srv := httptest.NewServer(http.HandlerFunc((&mockRPC{
		t: t,
		methods: map[string]func() string{
			"eth_chainId":             func() string { return `"0x4cef52"` },
			"eth_getTransactionCount": func() string { seenNonce = "called"; return `"0x5"` },
			"eth_estimateGas":         func() string { seenGas = "called"; return `"0xfde8"` }, // 65000
			"eth_getBlockByNumber":    func() string { return `{"baseFeePerGas":"0x3b9aca00"}` },
			"eth_sendRawTransaction":  func() string { seenTx = "called"; return `"0xabc123"` },
		},
	}).handler))
	defer srv.Close()

	c := NewClient(srv.URL, ChainIDTestnet)
	priv := make([]byte, 32)
	priv[31] = 1
	hash, err := USDCTransfer(context.Background(), c,
		"0x0000000000000000000000000000000000000001",
		priv,
		"0x742d35Cc6634C0532925a3b844Bc454e4438f44e",
		"1.25")
	require.NoError(t, err)
	assert.Equal(t, "0xabc123", hash)
	assert.Equal(t, "called", seenNonce)
	assert.Equal(t, "called", seenGas)
	assert.Equal(t, "called", seenTx)
}

func TestUSDCTransfer_RejectsBadAmount(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", ChainIDTestnet)
	priv := make([]byte, 32)
	priv[31] = 1
	_, err := USDCTransfer(context.Background(), c,
		"0x0000000000000000000000000000000000000001",
		priv,
		"0x742d35Cc6634C0532925a3b844Bc454e4438f44e",
		"nope")
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "invalid amount"))
}
