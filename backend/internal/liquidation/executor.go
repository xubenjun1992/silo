package liquidation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

// poolLiquidateABI is a minimal ABI for the liquidate(address) function.
const poolLiquidateABI = `[{"inputs":[{"name":"borrower","type":"address"}],"name":"liquidate","outputs":[{"name":"debtRepaid","type":"uint256"},{"name":"collateralSeized","type":"uint256"},{"name":"reward","type":"uint256"}],"stateMutability":"nonpayable","type":"function"}]`

// flashbotsRequest is the JSON-RPC request body for eth_sendPrivateTransaction.
type flashbotsRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

// LiquidationExecutor submits liquidation transactions on-chain.
// Supports both public mempool and Flashbots private relay for MEV protection.
type LiquidationExecutor struct {
	client   *ethclient.Client
	chainID  *big.Int
	privKey  *ecdsa.PrivateKey
	fromAddr common.Address
	poolABI  abi.ABI

	mu       sync.Mutex
	nonce    uint64
	nonceSet bool

	// MEV protection
	flashbotsURL string // e.g. "https://relay.flashbots.net"

	// Profitability
	minProfitMargin decimal.Decimal // e.g. 1.1 = 10% minimum profit over gas
}

func NewLiquidationExecutor(rpcURL, privateKeyHex, flashbotsRelayURL string, minProfitMargin decimal.Decimal) (*LiquidationExecutor, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	chainID, err := client.ChainID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("chainID: %w", err)
	}

	privKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	parsedABI, err := abi.JSON(strings.NewReader(poolLiquidateABI))
	if err != nil {
		return nil, fmt.Errorf("parse ABI: %w", err)
	}

	if minProfitMargin.LessThanOrEqual(decimal.Zero) {
		minProfitMargin = decimal.NewFromFloat(1.1) // default 10% margin
	}

	return &LiquidationExecutor{
		client:          client,
		chainID:         chainID,
		privKey:         privKey,
		fromAddr:        crypto.PubkeyToAddress(privKey.PublicKey),
		poolABI:         parsedABI,
		flashbotsURL:    flashbotsRelayURL,
		minProfitMargin: minProfitMargin,
	}, nil
}

/*═══════════════════════════════════════════════════════════════════════════════
  Profitability simulation
  ═══════════════════════════════════════════════════════════════════════════════*/

// SimulateLiquidation performs a static call to estimate the liquidation reward.
// Returns (debtRepaid, collateralSeized, reward, gasEstimate, error).
func (e *LiquidationExecutor) SimulateLiquidation(ctx context.Context, poolAddr, borrower string) (
	debtRepaid, collateralSeized, reward decimal.Decimal, gasEstimate uint64, err error) {

	toAddr := common.HexToAddress(poolAddr)
	borrowerAddr := common.HexToAddress(borrower)

	data, err := e.poolABI.Pack("liquidate", borrowerAddr)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, 0, fmt.Errorf("pack: %w", err)
	}

	// Estimate gas
	gas, err := e.client.EstimateGas(ctx, ethereum.CallMsg{
		From: e.fromAddr,
		To:   &toAddr,
		Data: data,
	})
	if err != nil {
		gas = 500_000 // fallback
	}
	gasWithBuffer := gas * 12 / 10

	// Simulate the call to get expected returns
	result, err := e.client.CallContract(ctx, ethereum.CallMsg{
		From: e.fromAddr,
		To:   &toAddr,
		Data: data,
	}, nil)
	if err != nil {
		// Simulation reverted — position may already be liquidated or unhealthy
		return decimal.Zero, decimal.Zero, decimal.Zero, gasWithBuffer, fmt.Errorf("simulate: %w", err)
	}

	unpacked, err := e.poolABI.Unpack("liquidate", result)
	if err != nil || len(unpacked) < 3 {
		return decimal.Zero, decimal.Zero, decimal.Zero, gasWithBuffer, fmt.Errorf("unpack simulation result: %w", err)
	}

	debtRepaidVal := unpacked[0].(*big.Int)
	collateralVal := unpacked[1].(*big.Int)
	rewardVal := unpacked[2].(*big.Int)

	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

	return normalizeAmount(debtRepaidVal, divisor),
		normalizeAmount(collateralVal, divisor),
		normalizeAmount(rewardVal, divisor),
		gasWithBuffer,
		nil
}

// CheckProfitability determines whether a liquidation is worth executing.
func (e *LiquidationExecutor) CheckProfitability(ctx context.Context, target *LiquidationTarget) bool {
	_, _, reward, gasEstimate, err := e.SimulateLiquidation(ctx, target.PoolAddr, target.UserAddr)
	if err != nil {
		log.Debug().
			Err(err).
			Str("pool", target.PoolAddr).
			Str("borrower", target.UserAddr).
			Msg("Profitability simulation failed, skipping")
		return false
	}

	// Get current gas price
	gasTip, err := e.client.SuggestGasTipCap(ctx)
	if err != nil {
		gasTip = big.NewInt(2e9)
	}

	// Estimate total gas cost in ETH
	gasCostWei := new(big.Int).Mul(new(big.Int).SetUint64(gasEstimate), gasTip)

	// For profitability, we compare reward (native token, e.g. ETH) vs gas cost
	rewardBN := bigIntFromDecimal(reward, 18)
	gasCostDecimal := decimal.NewFromBigInt(gasCostWei, 0).Div(decimal.NewFromInt(10).Pow(decimal.NewFromInt(18)))

	target.ExpectedReward = reward
	target.EstimatedGasCost = gasCostDecimal

	if rewardBN.Cmp(gasCostWei) <= 0 {
		log.Debug().
			Str("pool", target.PoolAddr).
			Str("borrower", target.UserAddr).
			Str("reward", reward.String()).
			Str("gasCost", gasCostDecimal.String()).
			Msg("Skipping — reward does not exceed gas cost")
		target.IsProfitable = false
		return false
	}

	// Check profit margin: reward / gasCost >= minProfitMargin
	profitRatio := reward.Div(gasCostDecimal)
	if profitRatio.LessThan(e.minProfitMargin) {
		log.Debug().
			Str("pool", target.PoolAddr).
			Str("borrower", target.UserAddr).
			Str("profitRatio", profitRatio.String()).
			Str("minMargin", e.minProfitMargin.String()).
			Msg("Skipping — profit margin too low")
		target.IsProfitable = false
		return false
	}

	target.IsProfitable = true
	// Compute composite priority: risk weight (1-HF) × profit weight
	riskWeight := decimal.NewFromInt(1).Sub(target.HealthFactor)
	if riskWeight.LessThan(decimal.Zero) {
		riskWeight = decimal.Zero
	}
	target.PriorityScore = riskWeight.Add(profitRatio.Sub(decimal.NewFromInt(1)))

	return true
}

/*═══════════════════════════════════════════════════════════════════════════════
  Transaction submission
  ═══════════════════════════════════════════════════════════════════════════════*/

// Liquidate submits a liquidation transaction. Tries Flashbots private relay first,
// falls back to public mempool.
func (e *LiquidationExecutor) Liquidate(ctx context.Context, poolAddr string, borrower string) (*LiquidationResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.nonceSet {
		nonce, err := e.client.PendingNonceAt(ctx, e.fromAddr)
		if err != nil {
			return nil, fmt.Errorf("nonce: %w", err)
		}
		e.nonce = nonce
		e.nonceSet = true
	}

	data, err := e.poolABI.Pack("liquidate", common.HexToAddress(borrower))
	if err != nil {
		return nil, fmt.Errorf("pack liquidate: %w", err)
	}

	toAddr := common.HexToAddress(poolAddr)
	gasLimit, err := e.client.EstimateGas(ctx, ethereum.CallMsg{
		From: e.fromAddr,
		To:   &toAddr,
		Data: data,
	})
	if err != nil {
		log.Warn().Err(err).Str("borrower", borrower).Msg("Gas estimation failed, using fallback")
		gasLimit = 500_000
	}
	gasLimit = gasLimit * 12 / 10 // +20% buffer

	gasTip, err := e.client.SuggestGasTipCap(ctx)
	if err != nil {
		gasTip = big.NewInt(2e9)
	}
	gasFeeCap := new(big.Int).Add(gasTip, new(big.Int).Mul(gasTip, big.NewInt(2)))

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   e.chainID,
		Nonce:     e.nonce,
		GasTipCap: gasTip,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &toAddr,
		Value:     big.NewInt(0),
		Data:      data,
	})

	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(e.chainID), e.privKey)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	// Try Flashbots private transaction first
	txHash := signedTx.Hash().Hex()
	if e.flashbotsURL != "" {
		if err := e.sendPrivateTransaction(ctx, signedTx); err != nil {
			log.Warn().Err(err).Str("tx", txHash).Msg("Flashbots submission failed, falling back to public mempool")
			if err := e.client.SendTransaction(ctx, signedTx); err != nil {
				return nil, fmt.Errorf("send tx (public fallback): %w", err)
			}
		} else {
			log.Info().Str("tx", txHash).Msg("Transaction submitted via Flashbots relay")
		}
	} else {
		if err := e.client.SendTransaction(ctx, signedTx); err != nil {
			return nil, fmt.Errorf("send tx: %w", err)
		}
	}

	e.nonce++

	return &LiquidationResult{
		TxHash:    txHash,
		PoolAddr:  poolAddr,
		Borrower:  borrower,
		Timestamp: time.Now().Unix(),
	}, nil
}

// sendPrivateTransaction submits a signed tx to a Flashbots/MEV-boost relay.
func (e *LiquidationExecutor) sendPrivateTransaction(ctx context.Context, signedTx *types.Transaction) error {
	txBytes, err := signedTx.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal tx: %w", err)
	}
	txHex := fmt.Sprintf("0x%x", txBytes)

	reqBody := flashbotsRequest{
		JSONRPC: "2.0",
		Method:  "eth_sendPrivateTransaction",
		Params:  []interface{}{txHex},
		ID:      1,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.flashbotsURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Use a short timeout — private relay should respond quickly
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("relay request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relay returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Check for JSON-RPC error in response
	var rpcResp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err == nil && rpcResp.Error != nil {
		return fmt.Errorf("relay error: %s", rpcResp.Error.Message)
	}

	return nil
}

/*═══════════════════════════════════════════════════════════════════════════════
  Receipt
  ═══════════════════════════════════════════════════════════════════════════════*/

// WaitForReceipt waits for a transaction to be mined (up to 120s for private txs).
func (e *LiquidationExecutor) WaitForReceipt(ctx context.Context, txHash string) error {
	hash := common.HexToHash(txHash)
	for i := 0; i < 120; i++ {
		receipt, err := e.client.TransactionReceipt(ctx, hash)
		if err == nil && receipt != nil {
			if receipt.Status == types.ReceiptStatusSuccessful {
				return nil
			}
			return fmt.Errorf("tx reverted: %s", txHash)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return fmt.Errorf("tx %s not mined within 120s", txHash)
}

/*═══════════════════════════════════════════════════════════════════════════════
  Helpers
  ═══════════════════════════════════════════════════════════════════════════════*/

func normalizeAmount(val *big.Int, divisor *big.Int) decimal.Decimal {
	f := new(big.Float).SetInt(val)
	divF := new(big.Float).SetInt(divisor)
	result, _ := new(big.Float).Quo(f, divF).Float64()
	return decimal.NewFromFloat(result)
}

func bigIntFromDecimal(d decimal.Decimal, decimals int) *big.Int {
	multiplier := decimal.NewFromInt(10).Pow(decimal.NewFromInt(int64(decimals)))
	scaled := d.Mul(multiplier)
	return scaled.BigInt()
}
