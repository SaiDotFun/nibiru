package kv

import (
	"fmt"
	"math/big"

	"github.com/NibiruChain/nibiru/v2/bft/state/indexer"
	"github.com/google/orderedcode"
)

type HeightInfo struct {
	heightRange indexer.QueryRange
	height      int64
}

// IntInSlice returns true if a is found in the list.
func intInSlice(a int, list []int) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func ParseEventSeqFromEventKey(key []byte) (int64, error) {
	var (
		compositeKey, typ, eventValue string
		height                        int64
		eventSeq                      int64
	)

	remaining, err := orderedcode.Parse(string(key), &compositeKey, &eventValue, &height, &typ, &eventSeq)
	if err != nil {
		return 0, fmt.Errorf("failed to parse event key: %w", err)
	}

	if len(remaining) != 0 {
		return 0, fmt.Errorf("unexpected remainder in key: %s", remaining)
	}

	return eventSeq, nil
}

func checkHeightConditions(heightInfo HeightInfo, keyHeight int64) bool {
	if heightInfo.height > 0 {
		return keyHeight == heightInfo.height
	}
	return heightInfo.heightRange.Key == "" || checkBounds(heightInfo.heightRange, big.NewInt(keyHeight))
}
