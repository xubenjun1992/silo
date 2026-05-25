package liquidation

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

const aggregatorABI = `[{"inputs":[],"name":"latestRoundData","outputs":[{"name":"roundId","type":"uint80"},{"name":"answer","type":"int256"},{"name":"startedAt","type":"uint256"},{"name":"updatedAt","type":"uint256"},{"name":"answeredInRound","type":"uint80"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"stateMutability":"view","type":"function"}]`

type priceResult struct {
	price     decimal.Decimal
	updatedAt int64
	err       error
}

// OraclePriceFeeder fetches spot prices from Chainlink AggregatorV3 contracts
// via multiple RPC endpoints and returns the median price.
type OraclePriceFeeder struct {
	clients       []*ethclient.Client
	aggregatorABI abi.ABI
	aggregators   map[string]string // tokenAddr → aggregatorAddr
}

func NewOraclePriceFeeder(rpcURLs []string, aggregators map[string]string) *OraclePriceFeeder {
	if len(rpcURLs) == 0 {
		log.Fatal().Msg("OraclePriceFeeder: at least one RPC URL is required")
	}

	parsedABI, err := abi.JSON(strings.NewReader(aggregatorABI))
	if err != nil {
		log.Fatal().Err(err).Msg("OraclePriceFeeder: failed to parse ABI")
	}

	clients := make([]*ethclient.Client, 0, len(rpcURLs))
	for _, url := range rpcURLs {
		client, err := ethclient.Dial(url)
		if err != nil {
			log.Error().Err(err).Str("url", url).Msg("OraclePriceFeeder: failed to dial RPC, skipping")
			continue
		}
		clients = append(clients, client)
	}
	if len(clients) == 0 {
		log.Fatal().Msg("OraclePriceFeeder: failed to connect to any RPC endpoint")
	}

	log.Info().Int("connected", len(clients)).Int("configured", len(rpcURLs)).Msg("OraclePriceFeeder initialized")
	return &OraclePriceFeeder{
		clients:       clients,
		aggregatorABI: parsedABI,
		aggregators:   aggregators,
	}
}

// GetPrice returns the median spot price across all RPC endpoints.
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

// getPriceWithAge queries all RPC endpoints in parallel and returns the median price.
func (f *OraclePriceFeeder) getPriceWithAge(ctx context.Context, tokenAddr string) (decimal.Decimal, int64, error) {
	aggAddr, ok := f.aggregators[tokenAddr]
	if !ok {
		return decimal.Zero, 0, fmt.Errorf("no aggregator registered for token %s", tokenAddr)
	}

	results := f.queryAllClients(ctx, aggAddr)
	prices, latestTs := collectValid(results)
	if len(prices) == 0 {
		return decimal.Zero, 0, fmt.Errorf("all RPC endpoints failed for token %s", tokenAddr)
	}

	median := medianPrice(prices)
	return median, latestTs, nil
}

// queryAllClients fans out to every RPC client concurrently.
func (f *OraclePriceFeeder) queryAllClients(ctx context.Context, aggAddr string) []priceResult {
	addr := common.HexToAddress(aggAddr)
	results := make([]priceResult, len(f.clients))
	var wg sync.WaitGroup

	for i, client := range f.clients {
		wg.Add(1)
		go func(idx int, c *ethclient.Client) {
			defer wg.Done()
			results[idx] = f.querySingle(ctx, c, addr)
		}(i, client)
	}
	wg.Wait()
	return results
}

// querySingle fetches price + decimals from one RPC.
func (f *OraclePriceFeeder) querySingle(ctx context.Context, client *ethclient.Client, addr common.Address) priceResult {
	data, err := f.aggregatorABI.Pack("latestRoundData")
	if err != nil {
		return priceResult{err: err}
	}

	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: data}, nil)
	if err != nil {
		return priceResult{err: fmt.Errorf("call latestRoundData: %w", err)}
	}

	unpacked, err := f.aggregatorABI.Unpack("latestRoundData", result)
	if err != nil || len(unpacked) < 4 {
		return priceResult{err: fmt.Errorf("unpack latestRoundData: %w", err)}
	}

	answer := unpacked[1].(*big.Int)
	updatedAt := unpacked[3].(*big.Int)

	decimalsVal := f.fetchDecimals(ctx, client, addr)

	price := decimal.NewFromBigInt(answer, 0)
	divisor := decimal.NewFromInt(10).Pow(decimal.NewFromInt(int64(decimalsVal)))

	return priceResult{
		price:     price.Div(divisor),
		updatedAt: updatedAt.Int64(),
	}
}

func (f *OraclePriceFeeder) fetchDecimals(ctx context.Context, client *ethclient.Client, addr common.Address) uint8 {
	decData, _ := f.aggregatorABI.Pack("decimals")
	decResult, err := client.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: decData}, nil)
	if err != nil {
		return 8 // Chainlink default
	}
	decUnpacked, _ := f.aggregatorABI.Unpack("decimals", decResult)
	if len(decUnpacked) > 0 {
		return decUnpacked[0].(uint8)
	}
	return 8
}

// collectValid extracts successful prices and the latest timestamp.
func collectValid(results []priceResult) ([]decimal.Decimal, int64) {
	prices := make([]decimal.Decimal, 0, len(results))
	var latestTs int64
	for _, r := range results {
		if r.err != nil {
			log.Warn().Err(r.err).Msg("OraclePriceFeeder: RPC query failed")
			continue
		}
		prices = append(prices, r.price)
		if r.updatedAt > latestTs {
			latestTs = r.updatedAt
		}
	}
	return prices, latestTs
}

// medianPrice returns the median of a sorted price slice.
// For odd lengths the middle element; for even lengths the average of the two middle elements.
func medianPrice(prices []decimal.Decimal) decimal.Decimal {
	if len(prices) == 1 {
		return prices[0]
	}

	sorted := make([]decimal.Decimal, len(prices))
	copy(sorted, prices)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].LessThan(sorted[j])
	})

	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return sorted[mid-1].Add(sorted[mid]).Div(decimal.NewFromInt(2))
}
