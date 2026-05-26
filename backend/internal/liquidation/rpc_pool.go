package liquidation

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog/log"
)

const (
	defaultMaxRetries = 2
	defaultRetryBase  = 150 * time.Millisecond
	healthDecayHalf   = 5 * time.Minute
	minSamples        = 5
	degradeErrRate    = 0.5
	recoverErrRate    = 0.1
	degradeLatency    = 3 * time.Second
)

// rpcNode wraps an ethclient.Client with health tracking for adaptive routing.
type rpcNode struct {
	client *ethclient.Client
	url    string

	mu        sync.Mutex
	errCount  float64 // decayed error count
	okCount   float64 // decayed success count
	latSum    float64 // decayed latency sum (seconds)
	lastDecay time.Time
	degraded  bool
}

// record a call result. isErr=true for failures; latency is the call duration.
func (n *rpcNode) record(latency time.Duration, isErr bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.decayLocked()

	if isErr {
		n.errCount++
	} else {
		n.okCount++
		n.latSum += latency.Seconds()
	}

	n.assessLocked()
}

// decayLocked halves counters after healthDecayHalf has passed, so recent
// behaviour carries more weight.
func (n *rpcNode) decayLocked() {
	now := time.Now()
	if n.lastDecay.IsZero() {
		n.lastDecay = now
		return
	}
	elapsed := now.Sub(n.lastDecay)
	halvings := int(elapsed / healthDecayHalf)
	if halvings == 0 {
		return
	}
	factor := math.Pow(0.5, float64(halvings))
	n.errCount *= factor
	n.okCount *= factor
	n.latSum *= factor
	n.lastDecay = now
}

func (n *rpcNode) assessLocked() {
	total := n.errCount + n.okCount
	if total < minSamples {
		return
	}
	errRate := n.errCount / total
	avgLat := time.Duration(0)
	if n.okCount > 0 {
		avgLat = time.Duration(n.latSum / n.okCount * float64(time.Second))
	}

	if (errRate >= degradeErrRate || avgLat >= degradeLatency) && !n.degraded {
		n.degraded = true
		log.Warn().
			Str("url", n.url).
			Float64("err_rate", errRate).
			Dur("avg_lat", avgLat).
			Msg("RPC node degraded")
	} else if errRate <= recoverErrRate && avgLat < degradeLatency && n.degraded {
		n.degraded = false
		log.Info().
			Str("url", n.url).
			Float64("err_rate", errRate).
			Dur("avg_lat", avgLat).
			Msg("RPC node recovered")
	}
}

func (n *rpcNode) isHealthy() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return !n.degraded
}

// rpcPool manages RPC nodes with health-based selection, per-call retry,
// and adaptive failover.
type rpcPool struct {
	nodes        []*rpcNode
	maxRetries   int
	retryBase    time.Duration
}

func newRPCPool(clients []*ethclient.Client, urls []string, maxRetries int, retryBase time.Duration) *rpcPool {
	nodes := make([]*rpcNode, len(clients))
	for i, c := range clients {
		url := ""
		if i < len(urls) {
			url = urls[i]
		}
		nodes[i] = &rpcNode{client: c, url: url}
	}
	return &rpcPool{
		nodes:      nodes,
		maxRetries: maxRetries,
		retryBase:  retryBase,
	}
}

// queryAll queries every healthy node in parallel. Each node gets up to
// maxRetries retries with exponential backoff. Degraded nodes are skipped
// unless no healthy nodes remain.
func (p *rpcPool) queryAll(ctx context.Context, fn func(client *ethclient.Client) priceResult) []priceResult {
	nodes := p.selectNodes()

	results := make([]priceResult, len(nodes))
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func(idx int, n *rpcNode) {
			defer wg.Done()
			results[idx] = p.queryWithRetry(ctx, n, fn)
		}(i, node)
	}
	wg.Wait()
	return results
}

func (p *rpcPool) selectNodes() []*rpcNode {
	healthy := make([]*rpcNode, 0, len(p.nodes))
	for _, n := range p.nodes {
		if n.isHealthy() {
			healthy = append(healthy, n)
		}
	}
	if len(healthy) > 0 {
		return healthy
	}
	// All nodes degraded — try them anyway (they may have recovered).
	log.Warn().Msg("All RPC nodes degraded, falling back to full pool")
	return p.nodes
}

func (p *rpcPool) queryWithRetry(ctx context.Context, n *rpcNode, fn func(client *ethclient.Client) priceResult) priceResult {
	start := time.Now()

	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := p.retryBase * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				n.record(time.Since(start), true)
				return priceResult{err: ctx.Err()}
			case <-time.After(backoff):
			}
		}

		result := fn(n.client)
		if result.err == nil {
			n.record(time.Since(start), false)
			return result
		}

		log.Debug().
			Str("url", n.url).
			Int("attempt", attempt+1).
			Err(result.err).
			Msg("RPC call failed, retrying")
	}

	n.record(time.Since(start), true)
	return priceResult{err: fmt.Errorf("rpc %s: failed after %d attempts", n.url, p.maxRetries+1)}
}
