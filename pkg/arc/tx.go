package arc

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// EIP1559Tx is an unsigned EIP-1559 (type 0x02) transaction.
type EIP1559Tx struct {
	ChainID              *big.Int
	Nonce                uint64
	MaxPriorityFeePerGas *big.Int
	MaxFeePerGas         *big.Int
	GasLimit             uint64
	To                   [20]byte
	Value                *big.Int
	Data                 []byte
}

// signingPayload RLP-encodes the unsigned 9-element tx body for hashing.
func (t *EIP1559Tx) signingPayload() []byte {
	var buf bytes.Buffer
	buf.Write(rlpUint(t.ChainID))
	buf.Write(rlpUint(new(big.Int).SetUint64(t.Nonce)))
	buf.Write(rlpUint(t.MaxPriorityFeePerGas))
	buf.Write(rlpUint(t.MaxFeePerGas))
	buf.Write(rlpUint(new(big.Int).SetUint64(t.GasLimit)))
	buf.Write(rlpAddress(t.To))
	buf.Write(rlpUint(t.Value))
	buf.Write(rlpBytes(t.Data))
	buf.Write(rlpEncode(nil, true)) // empty access list
	return buf.Bytes()
}

// SigningHash returns keccak(0x02 || rlp(body)) per EIP-1559.
func (t *EIP1559Tx) SigningHash() []byte {
	payload := t.signingPayload()
	return keccak256(append([]byte{0x02}, rlpEncode(payload, true)...))
}

// Sign signs the tx with a 32-byte secp256k1 private key and returns the
// signed raw transaction bytes (0x02 || rlp(body + v,r,s)), broadcastable via
// eth_sendRawTransaction, plus the tx hash.
func (t *EIP1559Tx) Sign(privKey []byte) (rawTx []byte, txHash string, err error) {
	if len(privKey) != 32 {
		return nil, "", fmt.Errorf("private key must be 32 bytes, got %d", len(privKey))
	}
	priv := secp256k1.PrivKeyFromBytes(privKey)
	if priv == nil {
		return nil, "", fmt.Errorf("invalid secp256k1 private key")
	}
	hash := t.SigningHash()
	compact := ecdsa.SignCompact(priv, hash, false)
	if len(compact) != 65 {
		return nil, "", fmt.Errorf("compact signature must be 65 bytes, got %d", len(compact))
	}
	v := uint64(compact[0]) - 27
	if v > 1 {
		return nil, "", fmt.Errorf("invalid recovery id %d", compact[0])
	}
	r := new(big.Int).SetBytes(compact[1:33])
	s := new(big.Int).SetBytes(compact[33:65])

	var buf bytes.Buffer
	buf.Write(rlpUint(t.ChainID))
	buf.Write(rlpUint(new(big.Int).SetUint64(t.Nonce)))
	buf.Write(rlpUint(t.MaxPriorityFeePerGas))
	buf.Write(rlpUint(t.MaxFeePerGas))
	buf.Write(rlpUint(new(big.Int).SetUint64(t.GasLimit)))
	buf.Write(rlpAddress(t.To))
	buf.Write(rlpUint(t.Value))
	buf.Write(rlpBytes(t.Data))
	buf.Write(rlpEncode(nil, true))
	buf.Write(rlpUint(new(big.Int).SetUint64(v)))
	buf.Write(rlpUint(r))
	buf.Write(rlpUint(s))
	rawTx = append([]byte{0x02}, rlpEncode(buf.Bytes(), true)...)
	txHash = "0x" + hex.EncodeToString(keccak256(rawTx))
	return rawTx, txHash, nil
}

// addressFromPrivKey derives the lowercase 0x address for a 32-byte key.
func addressFromPrivKey(privKey []byte) (string, error) {
	if len(privKey) != 32 {
		return "", fmt.Errorf("private key must be 32 bytes, got %d", len(privKey))
	}
	priv := secp256k1.PrivKeyFromBytes(privKey)
	if priv == nil {
		return "", fmt.Errorf("invalid secp256k1 private key")
	}
	uncompressed := priv.PubKey().SerializeUncompressed()
	digest := keccak256(uncompressed[1:])
	return "0x" + hex.EncodeToString(digest[12:]), nil
}

// RecoverSender recovers the signer address of signedTx (0x02-prefixed raw).
// Used in tests and to verify a tx before broadcast accounting.
func RecoverSender(signedTx []byte) (string, error) {
	if len(signedTx) < 2 || signedTx[0] != 0x02 {
		return "", fmt.Errorf("not a type-0x02 transaction")
	}
	fields, err := rlpDecodeList(signedTx[1:])
	if err != nil {
		return "", err
	}
	if len(fields) != 12 {
		return "", fmt.Errorf("EIP-1559 tx must have 12 fields, got %d", len(fields))
	}
	// Rebuild the signing hash from the first 9 fields. Each field is
	// re-encoded by type (our own signer always emits canonical RLP, so the
	// round-trip is exact): uints, 20-byte address, bytes, access list.
	var body bytes.Buffer
	for i, f := range fields[:9] {
		switch i {
		case 5:
			body.Write(rlpEncode(f, false)) // to
		case 7:
			body.Write(rlpBytes(f)) // data
		case 8:
			body.Write(rlpEncode(f, true)) // access list content
		default:
			body.Write(rlpUint(new(big.Int).SetBytes(f)))
		}
	}
	hash := keccak256(append([]byte{0x02}, rlpEncode(body.Bytes(), true)...))
	v := new(big.Int).SetBytes(fields[9]).Uint64()
	if v > 1 {
		return "", fmt.Errorf("invalid v %d", v)
	}
	r := new(big.Int).SetBytes(fields[10])
	s := new(big.Int).SetBytes(fields[11])
	compact := make([]byte, 65)
	compact[0] = byte(v) + 27
	copy(compact[1:33], leftPad(r.Bytes(), 32))
	copy(compact[33:65], leftPad(s.Bytes(), 32))
	pub, _, err := ecdsa.RecoverCompact(compact, hash)
	if err != nil {
		return "", fmt.Errorf("recovering sender: %w", err)
	}
	uncompressed := pub.SerializeUncompressed()
	digest := keccak256(uncompressed[1:])
	return "0x" + hex.EncodeToString(digest[12:]), nil
}

// rlpDecodeList decodes one top-level RLP list into its raw field payloads.
func rlpDecodeList(raw []byte) ([][]byte, error) {
	content, _, err := rlpDecodeItem(raw, 0)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for len(content) > 0 {
		var field []byte
		var rest []byte
		field, rest, err = rlpDecodeItem(content, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, field)
		content = rest
	}
	return out, nil
}

// rlpDecodeItem decodes one item starting at off, returning its payload and
// the remaining bytes. Long-form lengths beyond 8 bytes are rejected.
func rlpDecodeItem(raw []byte, off int) (payload, rest []byte, err error) {
	if off >= len(raw) {
		return nil, nil, fmt.Errorf("rlp: truncated input")
	}
	prefix := raw[off]
	switch {
	case prefix < 0x80:
		return raw[off : off+1], raw[off+1:], nil
	case prefix <= 0xb7:
		n := int(prefix - 0x80)
		if off+1+n > len(raw) {
			return nil, nil, fmt.Errorf("rlp: truncated string")
		}
		return raw[off+1 : off+1+n], raw[off+1+n:], nil
	case prefix <= 0xbf:
		ll := int(prefix - 0xb7)
		if ll > 8 || off+1+ll > len(raw) {
			return nil, nil, fmt.Errorf("rlp: bad length prefix")
		}
		n := 0
		for _, b := range raw[off+1 : off+1+ll] {
			n = n*256 + int(b)
		}
		if off+1+ll+n > len(raw) {
			return nil, nil, fmt.Errorf("rlp: truncated long string")
		}
		return raw[off+1+ll : off+1+ll+n], raw[off+1+ll+n:], nil
	case prefix <= 0xf7:
		n := int(prefix - 0xc0)
		if off+1+n > len(raw) {
			return nil, nil, fmt.Errorf("rlp: truncated list")
		}
		return raw[off+1 : off+1+n], raw[off+1+n:], nil
	default:
		ll := int(prefix - 0xf7)
		if ll > 8 || off+1+ll > len(raw) {
			return nil, nil, fmt.Errorf("rlp: bad list length prefix")
		}
		n := 0
		for _, b := range raw[off+1 : off+1+ll] {
			n = n*256 + int(b)
		}
		if off+1+ll+n > len(raw) {
			return nil, nil, fmt.Errorf("rlp: truncated long list")
		}
		return raw[off+1+ll : off+1+ll+n], raw[off+1+ll+n:], nil
	}
}
