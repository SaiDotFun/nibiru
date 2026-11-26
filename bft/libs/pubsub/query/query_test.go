package query_test

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/NibiruChain/nibiru/v2/bft/libs/pubsub/query"
)

func TestBigNumbers(t *testing.T) {
	bigInt := "10000000000000000000"
	bigIntAsFloat := "10000000000000000000.0"
	bigFloat := "10000000000000000000.6"
	bigFloatLowerRounding := "10000000000000000000.1"
	doubleBigInt := "20000000000000000000"

	testCases := []struct {
		s        string
		events   map[string][]string
		err      bool
		matches  bool
		matchErr bool
	}{

		{"account.balance <= " + bigInt, map[string][]string{"account.balance": {bigInt}}, false, true, false},
		{"account.balance <= " + bigInt, map[string][]string{"account.balance": {bigIntAsFloat}}, false, true, false},
		{"account.balance <= " + doubleBigInt, map[string][]string{"account.balance": {bigInt}}, false, true, false},
		{"account.balance <= " + bigInt, map[string][]string{"account.balance": {"10000000000000000001"}}, false, false, false},
		{"account.balance <= " + doubleBigInt, map[string][]string{"account.balance": {bigFloat}}, false, true, false},
		// To maintain compatibility with the old implementation which did a simple cast of float to int64, we do not round the float
		// Thus both 10000000000000000000.6 and "10000000000000000000.1 are equal to 10000000000000000000
		// and the test does not find a match
		{"account.balance > " + bigInt, map[string][]string{"account.balance": {bigFloat}}, false, false, false},
		{"account.balance > " + bigInt, map[string][]string{"account.balance": {bigFloatLowerRounding}}, true, false, false},
		// This test should also find a match, but floats that are too big cannot be properly converted, thus
		// 10000000000000000000.6 gets rounded to 10000000000000000000
		{"account.balance > " + bigIntAsFloat, map[string][]string{"account.balance": {bigFloat}}, false, false, false},
		{"account.balance > 11234.0", map[string][]string{"account.balance": {"11234.6"}}, false, true, false},
		{"account.balance <= " + bigInt, map[string][]string{"account.balance": {"1000.45"}}, false, true, false},
	}

	for _, tc := range testCases {
		q, err := query.New(tc.s)
		if !tc.err {
			require.Nil(t, err)
		}
		require.NotNil(t, q, "Query '%s' should not be nil", tc.s)

		if tc.matches {
			match, err := q.Matches(tc.events)
			assert.Nil(t, err, "Query '%s' should not error on match %v", tc.s, tc.events)
			assert.True(t, match, "Query '%s' should match %v", tc.s, tc.events)
		} else {
			match, err := q.Matches(tc.events)
			assert.Equal(t, tc.matchErr, err != nil, "Unexpected error for query '%s' match %v", tc.s, tc.events)
			assert.False(t, match, "Query '%s' should not match %v", tc.s, tc.events)
		}
	}
}

func TestMatches(t *testing.T) {
	var (
		txDate = "2017-01-01"
		txTime = "2018-05-03T14:45:00Z"
	)

	testCases := []struct {
		s        string
		events   map[string][]string
		err      bool
		matches  bool
		matchErr bool
	}{
		{"tm.events.type='NewBlock'", map[string][]string{"tm.events.type": {"NewBlock"}}, false, true, false},
		{"tx.gas > 7", map[string][]string{"tx.gas": {"8"}}, false, true, false},
		{"transfer.amount > 7", map[string][]string{"transfer.amount": {"8stake"}}, false, true, false},
		{"transfer.amount > 7", map[string][]string{"transfer.amount": {"8.045stake"}}, false, true, false},
		{"transfer.amount > 7.043", map[string][]string{"transfer.amount": {"8.045stake"}}, false, true, false},
		{"transfer.amount > 8.045", map[string][]string{"transfer.amount": {"8.045stake"}}, false, false, false},
		{"tx.gas > 7 AND tx.gas < 9", map[string][]string{"tx.gas": {"8"}}, false, true, false},
		{"body.weight >= 3.5", map[string][]string{"body.weight": {"3.5"}}, false, true, false},
		{"account.balance < 1000.0", map[string][]string{"account.balance": {"900"}}, false, true, false},
		{"apples.kg <= 4", map[string][]string{"apples.kg": {"4.0"}}, false, true, false},
		{"body.weight >= 4.5", map[string][]string{"body.weight": {fmt.Sprintf("%v", float32(4.5))}}, false, true, false},
		{
			"oranges.kg < 4 AND watermellons.kg > 10",
			map[string][]string{"oranges.kg": {"3"}, "watermellons.kg": {"12"}},
			false,
			true,
			false,
		},
		{"peaches.kg < 4", map[string][]string{"peaches.kg": {"5"}}, false, false, false},
		{
			"tx.date > DATE 2017-01-01",
			map[string][]string{"tx.date": {time.Now().Format(query.DateLayout)}},
			false,
			true,
			false,
		},
		{"tx.date = DATE 2017-01-01", map[string][]string{"tx.date": {txDate}}, false, true, false},
		{"tx.date = DATE 2018-01-01", map[string][]string{"tx.date": {txDate}}, false, false, false},
		{
			"tx.time >= TIME 2013-05-03T14:45:00Z",
			map[string][]string{"tx.time": {time.Now().Format(query.TimeLayout)}},
			false,
			true,
			false,
		},
		{"tx.time = TIME 2013-05-03T14:45:00Z", map[string][]string{"tx.time": {txTime}}, false, false, false},
		{"abci.owner.name CONTAINS 'Igor'", map[string][]string{"abci.owner.name": {"Igor,Ivan"}}, false, true, false},
		{"abci.owner.name CONTAINS 'Igor'", map[string][]string{"abci.owner.name": {"Pavel,Ivan"}}, false, false, false},
		{"abci.owner.name = 'Igor'", map[string][]string{"abci.owner.name": {"Igor", "Ivan"}}, false, true, false},
		{
			"abci.owner.name = 'Ivan'",
			map[string][]string{"abci.owner.name": {"Igor", "Ivan"}},
			false,
			true,
			false,
		},
		{
			"abci.owner.name = 'Ivan' AND abci.owner.name = 'Igor'",
			map[string][]string{"abci.owner.name": {"Igor", "Ivan"}},
			false,
			true,
			false,
		},
		{
			"abci.owner.name = 'Ivan' AND abci.owner.name = 'John'",
			map[string][]string{"abci.owner.name": {"Igor", "Ivan"}},
			false,
			false,
			false,
		},
		{
			"tm.events.type='NewBlock'",
			map[string][]string{"tm.events.type": {"NewBlock"}, "app.name": {"fuzzed"}},
			false,
			true,
			false,
		},
		{
			"app.name = 'fuzzed'",
			map[string][]string{"tm.events.type": {"NewBlock"}, "app.name": {"fuzzed"}},
			false,
			true,
			false,
		},
		{
			"tm.events.type='NewBlock' AND app.name = 'fuzzed'",
			map[string][]string{"tm.events.type": {"NewBlock"}, "app.name": {"fuzzed"}},
			false,
			true,
			false,
		},
		{
			"tm.events.type='NewHeader' AND app.name = 'fuzzed'",
			map[string][]string{"tm.events.type": {"NewBlock"}, "app.name": {"fuzzed"}},
			false,
			false,
			false,
		},
		{"slash EXISTS",
			map[string][]string{"slash.reason": {"missing_signature"}, "slash.power": {"6000"}},
			false,
			true,
			false,
		},
		{"sl EXISTS",
			map[string][]string{"slash.reason": {"missing_signature"}, "slash.power": {"6000"}},
			false,
			true,
			false,
		},
		{"slash EXISTS",
			map[string][]string{"transfer.recipient": {"cosmos1gu6y2a0ffteesyeyeesk23082c6998xyzmt9mz"},
				"transfer.sender": {"cosmos1crje20aj4gxdtyct7z3knxqry2jqt2fuaey6u5"}},
			false,
			false,
			false,
		},
		{"slash.reason EXISTS AND slash.power > 1000",
			map[string][]string{"slash.reason": {"missing_signature"}, "slash.power": {"6000"}},
			false,
			true,
			false,
		},
		{"slash.reason EXISTS AND slash.power > 1000",
			map[string][]string{"slash.reason": {"missing_signature"}, "slash.power": {"500"}},
			false,
			false,
			false,
		},
		{"slash.reason EXISTS",
			map[string][]string{"transfer.recipient": {"cosmos1gu6y2a0ffteesyeyeesk23082c6998xyzmt9mz"},
				"transfer.sender": {"cosmos1crje20aj4gxdtyct7z3knxqry2jqt2fuaey6u5"}},
			false,
			false,
			false,
		},
	}

	for _, tc := range testCases {
		q, err := query.New(tc.s)
		if !tc.err {
			require.Nil(t, err)
		}
		require.NotNil(t, q, "Query '%s' should not be nil", tc.s)

		if tc.matches {
			match, err := q.Matches(tc.events)
			assert.Nil(t, err, "Query '%s' should not error on match %v", tc.s, tc.events)
			assert.True(t, match, "Query '%s' should match %v", tc.s, tc.events)
		} else {
			match, err := q.Matches(tc.events)
			assert.Equal(t, tc.matchErr, err != nil, "Unexpected error for query '%s' match %v", tc.s, tc.events)
			assert.False(t, match, "Query '%s' should not match %v", tc.s, tc.events)
		}
	}
}

func TestMustParse(t *testing.T) {
	assert.Panics(t, func() { query.MustParse("=") })
	assert.NotPanics(t, func() { query.MustParse("tm.events.type='NewBlock'") })
}

func TestUnicodeQueryTokens(t *testing.T) {
	q := query.MustParse(`typed.event.名前 = '"José"' AND tx.height = 7`)
	conditions, err := q.Conditions()
	require.NoError(t, err)
	require.Equal(t, []query.Condition{
		{CompositeKey: "typed.event.名前", Op: query.OpEqual, Operand: `"José"`},
		{CompositeKey: "tx.height", Op: query.OpEqual, Operand: big.NewInt(7)},
	}, conditions)

	matches, err := q.Matches(map[string][]string{
		"typed.event.名前": {`"José"`},
		"tx.height":      {"7"},
	})
	require.NoError(t, err)
	require.True(t, matches)
}

func TestConditions(t *testing.T) {
	txTime, err := time.Parse(time.RFC3339, "2013-05-03T14:45:00Z")
	require.NoError(t, err)

	bigInt := new(big.Int)
	bigInt, ok := bigInt.SetString("10000000000000000000", 10)
	require.True(t, ok)

	testCases := []struct {
		s          string
		conditions []query.Condition
	}{
		{
			s: "tm.events.type='NewBlock'",
			conditions: []query.Condition{
				{CompositeKey: "tm.events.type", Op: query.OpEqual, Operand: "NewBlock"},
			},
		},
		{
			s: `eth.evm.v1.EventEthereumTx.eth_hash = '"0x1234"' AND tx.height = 7`,
			conditions: []query.Condition{
				{CompositeKey: "eth.evm.v1.EventEthereumTx.eth_hash", Op: query.OpEqual, Operand: `"0x1234"`},
				{CompositeKey: "tx.height", Op: query.OpEqual, Operand: big.NewInt(7)},
			},
		},
		{
			s: "tx.gas > 7 AND tx.gas < 9",
			conditions: []query.Condition{
				{CompositeKey: "tx.gas", Op: query.OpGreater, Operand: big.NewInt(7)},
				{CompositeKey: "tx.gas", Op: query.OpLess, Operand: big.NewInt(9)},
			},
		},
		{

			s: "tx.gas > 7.5 AND tx.gas < 9",
			conditions: []query.Condition{
				{CompositeKey: "tx.gas", Op: query.OpGreater, Operand: 7.5},
				{CompositeKey: "tx.gas", Op: query.OpLess, Operand: big.NewInt(9)},
			},
		},
		{

			s: "tx.gas > " + bigInt.String() + " AND tx.gas < 9",
			conditions: []query.Condition{
				{CompositeKey: "tx.gas", Op: query.OpGreater, Operand: bigInt},
				{CompositeKey: "tx.gas", Op: query.OpLess, Operand: big.NewInt(9)},
			},
		},
		{
			s: "tx.time >= TIME 2013-05-03T14:45:00Z",
			conditions: []query.Condition{
				{CompositeKey: "tx.time", Op: query.OpGreaterEqual, Operand: txTime},
			},
		},
		{
			s: "slashing EXISTS",
			conditions: []query.Condition{
				{CompositeKey: "slashing", Op: query.OpExists},
			},
		},
	}

	for _, tc := range testCases {
		q, err := query.New(tc.s)
		require.Nil(t, err)

		c, err := q.Conditions()
		require.NoError(t, err)
		assert.Equal(t, tc.conditions, c)
	}
}

func TestConditionsWithExactDecimals(t *testing.T) {
	q := query.MustParse(`account.balance > 0.10000000000000000001 AND tx.height = 7`)
	decimal, ok := new(big.Rat).SetString("0.10000000000000000001")
	require.True(t, ok)

	conditions, err := q.ConditionsWithExactDecimals()
	require.NoError(t, err)
	require.Equal(t, []query.Condition{
		{CompositeKey: "account.balance", Op: query.OpGreater, Operand: decimal},
		{CompositeKey: "tx.height", Op: query.OpEqual, Operand: big.NewInt(7)},
	}, conditions)

	conditions, err = q.Conditions()
	require.NoError(t, err)
	require.Equal(t, float64(0.1), conditions[0].Operand)
}
