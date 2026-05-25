package liquidation

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

// Minimal Chainlink AggregatorV3 ABI for latestRoundData()
const aggregatorABI = `[{"inputs":[],"name":"latestRoundData","outputs":[{"name":"roundId","type":"uint80"},{"name":"answer","type":"int256"},{"name":"startedAt","type":"uint256"},{"name":"updatedAt","type":"uint256"},{"name":"answeredInRound","type":"uint80"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"stateMutability":"view","type":"function"}]`

// OraclePriceFeeder fetches spot prices from Chainlink AggregatorV3 contracts.
type OraclePriceFeeder struct {
	client        *ethclient.Client
	aggregatorABI abi.ABI
	aggregators   map[string]string // tokenAddr → aggregatorAddr
}

func NewOraclePriceFeeder(rpcURL string, aggregators map[string]string) *OraclePriceFeeder {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal().Err(err).Msg("OraclePriceFeeder: failed to dial RPC")
	}

	parsedABI, err := abi.JSON(strings.NewReader(aggregatorABI))
	if err != nil {
		log.Fatal().Err(err).Msg("OraclePriceFeeder: failed to parse ABI")
	}

	return &OraclePriceFeeder{
		client:        client,
		aggregatorABI: parsedABI,
		aggregators:   aggregators,
	}
}

// GetPrice returns the spot price as decimal.Decimal and the Chainlink updatedAt timestamp.
func (f *OraclePriceFeeder) GetPrice(ctx context.Context, tokenAddr string) (decimal.Decimal, int64, error) {
	return f.getPriceWithAge(ctx, tokenAddr)
}

// IsStale checks whether the price feed for a token has not updated within maxAge seconds.
func (f *OraclePriceFeeder) IsStale(ctx context.Context, tokenAddr string, maxAge int64) (bool, error) {
	_, updatedAt, err := f.getPriceWithAge(ctx, tokenAddr)
	if err != nil {
		return true, err
	}
	age := time.Now().Unix() - updatedAt
	if age < 0 {
		age = 0
	}
	return age > maxAge, nil
}

// getPriceWithAge returns (price, updatedAt, error).
func (f *OraclePriceFeeder) getPriceWithAge(ctx context.Context, tokenAddr string) (decimal.Decimal, int64, error) {
	aggAddr, ok := f.aggregators[tokenAddr]
	if !ok {
		return decimal.Zero, 0, fmt.Errorf("no aggregator registered for token %s", tokenAddr)
	}

	addr := common.HexToAddress(aggAddr)

	data, err := f.aggregatorABI.Pack("latestRoundData")
	if err != nil {
		return decimal.Zero, 0, err
	}

	result, err := f.client.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: data}, nil)
	if err != nil {
		return decimal.Zero, 0, fmt.Errorf("call latestRoundData for %s: %w", tokenAddr, err)
	}

	unpacked, err := f.aggregatorABI.Unpack("latestRoundData", result)
	if err != nil || len(unpacked) < 4 {
		return decimal.Zero, 0, fmt.Errorf("unpack latestRoundData: %w", err)
	}

	answer := unpacked[1].(*big.Int)
	updatedAt := unpacked[3].(*big.Int)

	// Get decimals
	decData, _ := f.aggregatorABI.Pack("decimals")
	decResult, err := f.client.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: decData}, nil)
	if err != nil {
		return decimal.Zero, 0, fmt.Errorf("call decimals: %w", err)
	}
	decUnpacked, _ := f.aggregatorABI.Unpack("decimals", decResult)
	decimalsVal := uint8(8) // Chainlink default
	if len(decUnpacked) > 0 {
		decimalsVal = decUnpacked[0].(uint8)
	}

	// Convert answer to decimal.Decimal with correct decimals
	price := decimal.NewFromBigInt(answer, 0)
	divisor := decimal.NewFromInt(10).Pow(decimal.NewFromInt(int64(decimalsVal)))
	priceFloat := price.Div(divisor)

	return priceFloat, updatedAt.Int64(), nil
}
