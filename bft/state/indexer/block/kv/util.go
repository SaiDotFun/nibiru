package kv

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"

	"github.com/google/orderedcode"

	"github.com/NibiruChain/nibiru/v2/bft/state/indexer"
	"github.com/NibiruChain/nibiru/v2/bft/types"
)

func intInSlice(a int, list []int) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}

	return false
}

func int64FromBytes(bz []byte) int64 {
	v, _ := binary.Varint(bz)
	return v
}

func int64ToBytes(i int64) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutVarint(buf, i)
	return buf[:n]
}

func heightKey(height int64) ([]byte, error) {
	return orderedcode.Append(
		nil,
		types.BlockHeightKey,
		height,
	)
}

func eventKey(compositeKey, typ, eventValue string, height int64, eventSeq int64) ([]byte, error) {
	return orderedcode.Append(
		nil,
		compositeKey,
		eventValue,
		height,
		typ,
		eventSeq,
	)
}

func parseValueFromPrimaryKey(key []byte) (string, error) {
	var (
		compositeKey string
		height       int64
	)

	remaining, err := orderedcode.Parse(string(key), &compositeKey, &height)
	if err != nil {
		return "", fmt.Errorf("failed to parse event key: %w", err)
	}

	if len(remaining) != 0 {
		return "", fmt.Errorf("unexpected remainder in key: %s", remaining)
	}

	return strconv.FormatInt(height, 10), nil
}

func parseValueFromEventKey(key []byte) (string, error) {
	var (
		compositeKey, typ, eventValue string
		height                        int64
	)

	_, err := orderedcode.Parse(string(key), &compositeKey, &eventValue, &height, &typ)
	if err != nil {
		return "", fmt.Errorf("failed to parse event key: %w", err)
	}

	return eventValue, nil
}

func parseEventSeqFromEventKey(key []byte) (int64, error) {
	var (
		compositeKey, typ, eventValue string
		height                        int64
		eventSeq                      int64
	)

	remaining, err := orderedcode.Parse(string(key), &compositeKey, &eventValue, &height, &typ)
	if err != nil {
		return 0, fmt.Errorf("failed to parse event key: %w", err)
	}

	// This is done to support previous versions that did not have event sequence in their key
	if len(remaining) != 0 {
		remaining, err = orderedcode.Parse(remaining, &eventSeq)
		if err != nil {
			return 0, fmt.Errorf("failed to parse event key: %w", err)
		}
		if len(remaining) != 0 {
			return 0, fmt.Errorf("unexpected remainder in key: %s", remaining)
		}
	}

	return eventSeq, nil
}

// checkHeightConditions applies the inclusive bounds from ExtractHeightFilter.
func checkHeightConditions(bounds indexer.QueryRange, height int64) bool {
	if bounds.Key == "" {
		return true
	}
	value := big.NewInt(height)
	lower, _ := bounds.LowerBound.(*big.Int)
	upper, _ := bounds.UpperBound.(*big.Int)
	return (lower == nil || value.Cmp(lower) >= 0) && (upper == nil || value.Cmp(upper) <= 0)
}
