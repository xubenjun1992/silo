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

// PriceSource fetches a price from a single price feed (e.g. one Chainlink aggregator).
type PriceSource interface {
	Price(ctx context.Context) (decimal.Decimal, int64, error)
}

// ChainlinkPriceSource queries a specific Chainlink AggregatorV3 contract.
// It delegates RPC management (retry, health tracking, failover) to rpcPool.
type ChainlinkPriceSource struct {
	pool          *rpcPool
	aggregatorABI abi.ABI
	aggregator    common.Address
}

// NewChainlinkPriceSource creates a price source backed by a single Chainlink aggregator.
func NewChainlinkPriceSource(pool *rpcPool, parsedABI abi.ABI, aggregatorAddr string) *ChainlinkPriceSource {
	return &ChainlinkPriceSource{
		pool:          pool,
		aggregatorABI: parsedABI,
		aggregator:    common.HexToAddress(aggregatorAddr),
	}
}

// Price queries the aggregator via healthy RPC nodes (with retry) and returns the median.
func (s *ChainlinkPriceSource) Price(ctx context.Context) (decimal.Decimal, int64, error) {
	fn := func(client *ethclient.Client) priceResult {
		return s.querySingle(ctx, client)
	}
	results := s.pool.queryAll(ctx, fn)
	prices, latestTs := collectValid(results)
	if len(prices) == 0 {
		return decimal.Zero, 0, fmt.Errorf("all RPC endpoints failed for aggregator %s", s.aggregator.Hex())
	}
	return medianPrice(prices), latestTs, nil
}

func (s *ChainlinkPriceSource) querySingle(ctx context.Context, client *ethclient.Client) priceResult {
	data, err := s.aggregatorABI.Pack("latestRoundData")
	if err != nil {
		return priceResult{err: err}
	}

	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &s.aggregator, Data: data}, nil)
	if err != nil {
		return priceResult{err: fmt.Errorf("call latestRoundData: %w", err)}
	}

	unpacked, err := s.aggregatorABI.Unpack("latestRoundData", result)
	if err != nil || len(unpacked) < 4 {
		return priceResult{err: fmt.Errorf("unpack latestRoundData: %w", err)}
	}

	answer := unpacked[1].(*big.Int)
	updatedAt := unpacked[3].(*big.Int)

	decimalsVal := s.fetchDecimals(ctx, client)

	price := decimal.NewFromBigInt(answer, 0)
	divisor := decimal.NewFromInt(10).Pow(decimal.NewFromInt(int64(decimalsVal)))

	return priceResult{
		price:     price.Div(divisor),
		updatedAt: updatedAt.Int64(),
	}
}

func (s *ChainlinkPriceSource) fetchDecimals(ctx context.Context, client *ethclient.Client) uint8 {
	decData, _ := s.aggregatorABI.Pack("decimals")
	decResult, err := client.CallContract(ctx, ethereum.CallMsg{To: &s.aggregator, Data: decData}, nil)
	if err != nil {
		return 8 // Chainlink default
	}
	decUnpacked, _ := s.aggregatorABI.Unpack("decimals", decResult)
	if len(decUnpacked) > 0 {
		return decUnpacked[0].(uint8)
	}
	return 8
}

// OraclePriceFeeder fetches spot prices from multiple price sources per token
// and returns the median across sources.
type OraclePriceFeeder struct {
	sources map[string][]PriceSource // tokenAddr → price sources
}

// NewOraclePriceFeeder creates an OraclePriceFeeder with multiple Chainlink
// aggregators per token. feedAddrs maps token address → list of aggregator addresses.
func NewOraclePriceFeeder(rpcURLs []string, feedAddrs map[string][]string) *OraclePriceFeeder {
	if len(rpcURLs) == 0 {
		log.Fatal().Msg("OraclePriceFeeder: at least one RPC URL is required")
	}

	parsedABI, err := abi.JSON(strings.NewReader(aggregatorABI))
	if err != nil {
		log.Fatal().Err(err).Msg("OraclePriceFeeder: failed to parse ABI")
	}

	clients := make([]*ethclient.Client, 0, len(rpcURLs))
	connectedURLs := make([]string, 0, len(rpcURLs))
	for _, url := range rpcURLs {
		client, err := ethclient.Dial(url)
		if err != nil {
			log.Error().Err(err).Str("url", url).Msg("OraclePriceFeeder: failed to dial RPC, skipping")
			continue
		}
		clients = append(clients, client)
		connectedURLs = append(connectedURLs, url)
	}
	if len(clients) == 0 {
		log.Fatal().Msg("OraclePriceFeeder: failed to connect to any RPC endpoint")
	}

	pool := newRPCPool(clients, connectedURLs, defaultMaxRetries, defaultRetryBase)

	sources := make(map[string][]PriceSource, len(feedAddrs))
	totalSources := 0
	for tokenAddr, aggAddrs := range feedAddrs {
		srcs := make([]PriceSource, 0, len(aggAddrs))
		for _, aggAddr := range aggAddrs {
			srcs = append(srcs, NewChainlinkPriceSource(pool, parsedABI, aggAddr))
		}
		sources[tokenAddr] = srcs
		totalSources += len(srcs)
	}

	log.Info().
		Int("tokens", len(sources)).
		Int("total_sources", totalSources).
		Int("rpc_clients", len(clients)).
		Msg("OraclePriceFeeder initialized")
	return &OraclePriceFeeder{sources: sources}
}

// GetPrice returns the median spot price across all price sources for a token.
func (f *OraclePriceFeeder) GetPrice(ctx context.Context, tokenAddr string) (decimal.Decimal, int64, error) {
	return f.getPriceWithAge(ctx, tokenAddr)
}

// IsStale checks whether all price sources for a token have not updated within maxAge seconds.
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

// getPriceWithAge queries all price sources concurrently and returns the median.
func (f *OraclePriceFeeder) getPriceWithAge(ctx context.Context, tokenAddr string) (decimal.Decimal, int64, error) {
	srcs, ok := f.sources[tokenAddr]
	if !ok || len(srcs) == 0 {
		return decimal.Zero, 0, fmt.Errorf("no price source registered for token %s", tokenAddr)
	}

	results := make([]priceResult, len(srcs))
	var wg sync.WaitGroup
	for i, src := range srcs {
		wg.Add(1)
		go func(idx int, s PriceSource) {
			defer wg.Done()
			p, ts, err := s.Price(ctx)
			results[idx] = priceResult{price: p, updatedAt: ts, err: err}
		}(i, src)
	}
	wg.Wait()

	prices, latestTs := collectValid(results)
	if len(prices) == 0 {
		return decimal.Zero, 0, fmt.Errorf("all price sources failed for token %s", tokenAddr)
	}

	return medianPrice(prices), latestTs, nil
}

// collectValid extracts successful prices and the latest timestamp.
func collectValid(results []priceResult) ([]decimal.Decimal, int64) {
	prices := make([]decimal.Decimal, 0, len(results))
	var latestTs int64
	for _, r := range results {
		if r.err != nil {
			log.Warn().Err(r.err).Msg("OraclePriceFeeder: price query failed")
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
