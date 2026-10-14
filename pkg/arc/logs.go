package arc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Log is one eth_getLogs entry. Quantities arrive as 0x hex; indexes as hex
// too. Removed is true for logs orphaned by a reorg.
type Log struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	BlockNumber      uint64
	TransactionHash  string `json:"transactionHash"`
	TransactionIndex uint64
	LogIndex         uint64
	Removed          bool `json:"removed"`
}

// rawLog mirrors the wire shape before quantity parsing.
type rawLog struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	BlockNumber      string   `json:"blockNumber"`
	TransactionHash  string   `json:"transactionHash"`
	TransactionIndex string   `json:"transactionIndex"`
	LogIndex         string   `json:"logIndex"`
	Removed          bool     `json:"removed"`
}

func parseLogQuantity(field, raw string) (uint64, error) {
	v, err := parseHexUint(raw)
	if err != nil {
		return 0, fmt.Errorf("log %s: %w", field, err)
	}
	if !v.IsUint64() {
		return 0, fmt.Errorf("log %s overflows uint64: %q", field, raw)
	}
	return v.Uint64(), nil
}

func (l *rawLog) toLog() (Log, error) {
	out := Log{
		Address:         l.Address,
		Topics:          l.Topics,
		Data:            l.Data,
		TransactionHash: l.TransactionHash,
		Removed:         l.Removed,
	}
	var err error
	if out.BlockNumber, err = parseLogQuantity("blockNumber", l.BlockNumber); err != nil {
		return Log{}, err
	}
	if out.TransactionIndex, err = parseLogQuantity("transactionIndex", l.TransactionIndex); err != nil {
		return Log{}, err
	}
	if out.LogIndex, err = parseLogQuantity("logIndex", l.LogIndex); err != nil {
		return Log{}, err
	}
	return out, nil
}

// BlockNumber returns the latest block via eth_blockNumber.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	raw, err := c.call(ctx, "eth_blockNumber")
	if err != nil {
		return 0, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("eth_blockNumber: %w", err)
	}
	v, err := parseHexUint(s)
	if err != nil {
		return 0, err
	}
	if !v.IsUint64() {
		return 0, fmt.Errorf("eth_blockNumber overflows uint64: %q", s)
	}
	return v.Uint64(), nil
}

// ethBlockRange renders [from, to] as 0x quantities for filters.
func ethBlockRange(from, to uint64) (string, string) {
	return hexUint(new(big.Int).SetUint64(from)), hexUint(new(big.Int).SetUint64(to))
}

// GetLogs fetches eth_getLogs for the address set over [fromBlock, toBlock]
// (inclusive). topicFilter is the outer topics array (nil/empty = any); a nil
// inner entry is JSON null (wildcard). fromBlock > toBlock is a no-op.
func (c *Client) GetLogs(ctx context.Context, fromBlock, toBlock uint64, addresses []string, topicFilter [][]string) ([]Log, error) {
	if toBlock < fromBlock {
		return nil, nil
	}
	fromHex, toHex := ethBlockRange(fromBlock, toBlock)
	filter := map[string]any{
		"fromBlock": fromHex,
		"toBlock":   toHex,
	}
	if len(addresses) > 0 {
		filter["address"] = addresses
	}
	if len(topicFilter) > 0 {
		topics := make([]any, len(topicFilter))
		for i, set := range topicFilter {
			if set == nil {
				topics[i] = nil
				continue
			}
			topics[i] = set
		}
		filter["topics"] = topics
	}
	raw, err := c.call(ctx, "eth_getLogs", filter)
	if err != nil {
		return nil, err
	}
	var raws []rawLog
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, fmt.Errorf("eth_getLogs: %w", err)
	}
	out := make([]Log, 0, len(raws))
	for i := range raws {
		l, err := raws[i].toLog()
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// Receipt is the subset of eth_getTransactionReceipt the indexer needs:
// execution status and the contract the tx executed against.
type Receipt struct {
	TransactionHash string
	BlockNumber     uint64
	To              string
	Status          uint64
	Found           bool
}

// GetTransactionReceipt returns nil, nil when the tx is not yet mined.
func (c *Client) GetTransactionReceipt(ctx context.Context, txHash string) (*Receipt, error) {
	raw, err := c.call(ctx, "eth_getTransactionReceipt", txHash)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var r struct {
		TransactionHash string `json:"transactionHash"`
		BlockNumber     string `json:"blockNumber"`
		To              string `json:"to"`
		Status          string `json:"status"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("eth_getTransactionReceipt: %w", err)
	}
	out := &Receipt{TransactionHash: r.TransactionHash, To: r.To, Found: true}
	bn, err := parseHexUint(r.BlockNumber)
	if err != nil {
		return nil, fmt.Errorf("receipt blockNumber: %w", err)
	}
	if !bn.IsUint64() {
		return nil, fmt.Errorf("receipt blockNumber overflows uint64: %q", r.BlockNumber)
	}
	out.BlockNumber = bn.Uint64()
	st, err := parseHexUint(r.Status)
	if err != nil {
		return nil, fmt.Errorf("receipt status: %w", err)
	}
	if !st.IsUint64() {
		return nil, fmt.Errorf("receipt status overflows uint64: %q", r.Status)
	}
	out.Status = st.Uint64()
	return out, nil
}

// TopicToAddress decodes an indexed address topic (32-byte left-padded)
// to 0x hex.
func TopicToAddress(topic string) (string, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(topic, "0x"), "0X"))
	if err != nil {
		return "", fmt.Errorf("decoding topic %q: %w", topic, err)
	}
	if len(b) != 32 {
		return "", fmt.Errorf("address topic must be 32 bytes, got %d", len(b))
	}
	return "0x" + hex.EncodeToString(b[12:]), nil
}

// TopicToUint decodes an indexed uint256 topic.
func TopicToUint(topic string) (*big.Int, error) {
	return parseHexUint(topic)
}

// DataWords splits 0x event data into 32-byte words.
func DataWords(data string) ([][]byte, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(data, "0x"), "0X"))
	if err != nil {
		return nil, fmt.Errorf("decoding event data: %w", err)
	}
	if len(b)%32 != 0 {
		return nil, fmt.Errorf("event data length %d is not a multiple of 32", len(b))
	}
	words := make([][]byte, 0, len(b)/32)
	for i := 0; i < len(b); i += 32 {
		words = append(words, b[i:i+32])
	}
	return words, nil
}

// WordToAddress decodes a non-indexed address word (right-aligned 20 bytes).
func WordToAddress(w []byte) string {
	return "0x" + hex.EncodeToString(w[12:])
}

// WordToUint decodes a non-indexed uint256 word.
func WordToUint(w []byte) *big.Int {
	return new(big.Int).SetBytes(w)
}

// WordToBool decodes a non-indexed bool word (any nonzero = true).
func WordToBool(w []byte) bool {
	return new(big.Int).SetBytes(w).Sign() != 0
}
