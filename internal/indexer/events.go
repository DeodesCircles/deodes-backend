// Package indexer tails DeodesCircle/DeodesFactory logs on Arc (2.6).
package indexer

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/deodes/backend/pkg/arc"
)

// ParamKind is one of the ABI types used by IDeodesEvents.
type ParamKind string

const (
	KindAddress ParamKind = "address"
	KindUint256 ParamKind = "uint256"
	KindBool    ParamKind = "bool"
)

// Param is one event parameter in declaration order.
type Param struct {
	Name    string
	Kind    ParamKind
	Indexed bool
}

// Schema is the frozen IDeodesEvents signature (UNIFIED-PLAN 1.9).
// Topic0 is computed at init from Signature so a schema typo fails fast.
type Schema struct {
	Name      string
	Signature string
	Topic0    string
	Params    []Param
}

func mustSchema(name, sig string, params []Param) Schema {
	topic := "0x" + hex.EncodeToString(arc.Keccak256([]byte(sig)))
	return Schema{Name: name, Signature: sig, Topic0: topic, Params: params}
}

func addr(name string, indexed bool) Param {
	return Param{Name: name, Kind: KindAddress, Indexed: indexed}
}
func u256(name string, indexed bool) Param {
	return Param{Name: name, Kind: KindUint256, Indexed: indexed}
}
func boolean(name string, indexed bool) Param {
	return Param{Name: name, Kind: KindBool, Indexed: indexed}
}

// Schemas mirrors IDeodesEvents.sol exactly: circle indexed first everywhere.
var Schemas = []Schema{
	mustSchema("CircleCreated", "CircleCreated(address,address,address,uint256,uint256,uint256)",
		[]Param{addr("circle", true), addr("organizer", true), addr("treasury", false), u256("contribution", false), u256("maxMembers", false), u256("index", false)}),
	mustSchema("MemberJoined", "MemberJoined(address,address)",
		[]Param{addr("circle", true), addr("member", true)}),
	mustSchema("ContributionReceived", "ContributionReceived(address,address,uint256)",
		[]Param{addr("circle", true), addr("member", true), u256("amount", false)}),
	mustSchema("PayoutExecuted", "PayoutExecuted(address,address,uint256,uint256)",
		[]Param{addr("circle", true), addr("recipient", true), u256("net", false), u256("fee", false)}),
	mustSchema("FeeDeposited", "FeeDeposited(address,address,uint256)",
		[]Param{addr("circle", true), addr("treasury", true), u256("fee", false)}),
	mustSchema("LateReported", "LateReported(address,address,uint256)",
		[]Param{addr("circle", true), addr("member", true), u256("round", false)}),
	mustSchema("MemberExited", "MemberExited(address,address)",
		[]Param{addr("circle", true), addr("member", true)}),
	mustSchema("DefaultRecorded", "DefaultRecorded(address,address)",
		[]Param{addr("circle", true), addr("member", true)}),
	mustSchema("CircleCompleted", "CircleCompleted(address,uint256)",
		[]Param{addr("circle", true), u256("paidCount", false)}),
	mustSchema("AuctionBid", "AuctionBid(address,address,uint256,uint256)",
		[]Param{addr("circle", true), addr("bidder", true), u256("amount", false), u256("round", false)}),
	mustSchema("VoteCast", "VoteCast(address,address,bool)",
		[]Param{addr("circle", true), addr("voter", true), boolean("support", false)}),
	mustSchema("DisputeRaised", "DisputeRaised(address,address,uint256)",
		[]Param{addr("circle", true), addr("raiser", true), u256("round", false)}),
}

// ByTopic0 indexes schemas by their topic0 hash.
var ByTopic0 = func() map[string]Schema {
	m := make(map[string]Schema, len(Schemas))
	for _, s := range Schemas {
		m[strings.ToLower(s.Topic0)] = s
	}
	return m
}()

// DecodedEvent is a log matched to its schema with stringified fields:
// addresses as 0x hex, uints as decimal, bools as "true"/"false".
type DecodedEvent struct {
	Schema Schema
	Fields map[string]string
}

func decodeValue(kind ParamKind, topic string, word []byte) (string, error) {
	switch kind {
	case KindAddress:
		if topic != "" {
			return arc.TopicToAddress(topic)
		}
		return arc.WordToAddress(word), nil
	case KindUint256:
		if topic != "" {
			v, err := arc.TopicToUint(topic)
			if err != nil {
				return "", err
			}
			return v.String(), nil
		}
		return arc.WordToUint(word).String(), nil
	case KindBool:
		if topic != "" {
			v, err := arc.TopicToUint(topic)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%t", v.Sign() != 0), nil
		}
		return fmt.Sprintf("%t", arc.WordToBool(word)), nil
	default:
		return "", fmt.Errorf("unknown param kind %q", kind)
	}
}

// DecodeLog matches topics[0] to a schema and decodes the log. Anonymous
// logs (no topics) and unknown topic0 values return ok=false, nil.
func DecodeLog(topics []string, data string) (ev DecodedEvent, ok bool, err error) {
	if len(topics) == 0 {
		return DecodedEvent{}, false, nil
	}
	schema, known := ByTopic0[strings.ToLower(topics[0])]
	if !known {
		return DecodedEvent{}, false, nil
	}
	var indexed, plain []Param
	for _, p := range schema.Params {
		if p.Indexed {
			indexed = append(indexed, p)
		} else {
			plain = append(plain, p)
		}
	}
	if len(topics)-1 != len(indexed) {
		return DecodedEvent{}, true, fmt.Errorf("%s: want %d indexed topics, got %d", schema.Name, len(indexed), len(topics)-1)
	}
	words, err := arc.DataWords(data)
	if err != nil {
		return DecodedEvent{}, true, fmt.Errorf("%s: %w", schema.Name, err)
	}
	if len(words) != len(plain) {
		return DecodedEvent{}, true, fmt.Errorf("%s: want %d data words, got %d", schema.Name, len(plain), len(words))
	}
	fields := make(map[string]string, len(schema.Params))
	for i, p := range indexed {
		v, err := decodeValue(p.Kind, topics[i+1], nil)
		if err != nil {
			return DecodedEvent{}, true, fmt.Errorf("%s.%s: %w", schema.Name, p.Name, err)
		}
		fields[p.Name] = v
	}
	for i, p := range plain {
		v, err := decodeValue(p.Kind, "", words[i])
		if err != nil {
			return DecodedEvent{}, true, fmt.Errorf("%s.%s: %w", schema.Name, p.Name, err)
		}
		fields[p.Name] = v
	}
	return DecodedEvent{Schema: schema, Fields: fields}, true, nil
}
