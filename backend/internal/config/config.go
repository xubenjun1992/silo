package config

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	ServerAddr  string `mapstructure:"SERVER_ADDR"`
	DBDsn       string `mapstructure:"DB_DSN"`
	HttpRpcUrls []string `mapstructure:"HTTP_RPC_URLS"`
	HttpRpcUrl  string   // derived: first element of HttpRpcUrls, for single-URL consumers
	WsRpcUrl    string   `mapstructure:"WS_RPC_URL"`
	PoolFactory string `mapstructure:"POOL_FACTORY_ADDRESS"`
	StartBlock  uint64 `mapstructure:"START_BLOCK"`

	// Kafka
	KafkaBrokers     string `mapstructure:"KAFKA_BROKERS"`
	KafkaTopicPrefix string `mapstructure:"KAFKA_TOPIC_PREFIX"`

	// Safe block
	SafeConfirmations uint64 `mapstructure:"SAFE_CONFIRMATIONS"`

	// Redis
	RedisAddr     string `mapstructure:"REDIS_ADDR"`
	RedisPassword string `mapstructure:"REDIS_PASSWORD"`
	RedisDB       int    `mapstructure:"REDIS_DB"`

	// Liquidation — TWAP
	TWAPWindow      time.Duration `mapstructure:"TWAP_WINDOW"`
	TWAPGranularity time.Duration `mapstructure:"TWAP_GRANULARITY"`

	// Liquidation — market condition thresholds
	NormalMaxDeviation  float64 `mapstructure:"LIQ_NORMAL_MAX_DEVIATION"`
	ExtremeMaxDeviation float64 `mapstructure:"LIQ_EXTREME_MAX_DEVIATION"`

	// Liquidation — circuit breaker
	CircuitBreakerCooldown time.Duration `mapstructure:"LIQ_CB_COOLDOWN"`
	CircuitBreakerMaxLiq   int           `mapstructure:"LIQ_CB_MAX_LIQ"`

	// Liquidation — rate limiting
	NormalMaxLiqPerBatch int           `mapstructure:"LIQ_NORMAL_MAX_BATCH"`
	ScanInterval         time.Duration `mapstructure:"LIQ_SCAN_INTERVAL"`

	// Chainlink price feed aggregator addresses: tokenAddr → aggregatorAddr
	PriceFeedAddrs map[string]string `mapstructure:"PRICE_FEED_ADDRS"`

	// Liquidation executor
	ExecutorKey string `mapstructure:"LIQ_EXECUTOR_KEY"`

	// MEV protection — Flashbots / MEV-boost relay
	FlashbotsRelayURL string `mapstructure:"LIQ_FLASHBOTS_RELAY_URL"`

	// Profitability
	MinProfitMargin float64 `mapstructure:"LIQ_MIN_PROFIT_MARGIN"` // minimum profit ratio, e.g. 1.1 = 10% profit over gas cost

	// Price staleness
	PriceStalenessSeconds int64 `mapstructure:"LIQ_PRICE_STALE_SECONDS"` // max age of oracle price before considered stale
}

func Load() (*Config, error) {
	viper.SetDefault("SERVER_ADDR", ":8080")
	viper.SetDefault("START_BLOCK", 0)
	viper.SetDefault("KAFKA_BROKERS", "localhost:9092")
	viper.SetDefault("KAFKA_TOPIC_PREFIX", "silo")
	viper.SetDefault("SAFE_CONFIRMATIONS", 12)

	// Redis defaults
	viper.SetDefault("REDIS_ADDR", "localhost:6379")
	viper.SetDefault("REDIS_PASSWORD", "")
	viper.SetDefault("REDIS_DB", 0)

	// TWAP defaults
	viper.SetDefault("TWAP_WINDOW", "30m")
	viper.SetDefault("TWAP_GRANULARITY", "30s")

	// Market thresholds
	viper.SetDefault("LIQ_NORMAL_MAX_DEVIATION", 0.05)
	viper.SetDefault("LIQ_EXTREME_MAX_DEVIATION", 0.15)

	// Circuit breaker
	viper.SetDefault("LIQ_CB_COOLDOWN", "5m")
	viper.SetDefault("LIQ_CB_MAX_LIQ", 3)

	// Rate limiting
	viper.SetDefault("LIQ_NORMAL_MAX_BATCH", 20)
	viper.SetDefault("LIQ_SCAN_INTERVAL", "15s")

	// MEV protection
	viper.SetDefault("LIQ_FLASHBOTS_RELAY_URL", "")

	// Profitability — default 10% minimum profit over gas cost
	viper.SetDefault("LIQ_MIN_PROFIT_MARGIN", 1.1)

	// Price staleness — default 1 hour
	viper.SetDefault("LIQ_PRICE_STALE_SECONDS", 3600)

	viper.SetConfigFile(".env")
	viper.SetConfigType("env")
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()

	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// PRICE_FEED_ADDRS is a JSON map: {"0xtoken":"0xaggregator",...}
	if raw := viper.GetString("PRICE_FEED_ADDRS"); raw != "" {
		cfg.PriceFeedAddrs = make(map[string]string)
		if err := json.Unmarshal([]byte(raw), &cfg.PriceFeedAddrs); err != nil {
			return nil, fmt.Errorf("failed to parse PRICE_FEED_ADDRS JSON: %w", err)
		}
	}

	// Backward compat: HTTP_RPC_URL (single) → HTTP_RPC_URLS (comma-separated list)
	if len(cfg.HttpRpcUrls) == 0 {
		if single := viper.GetString("HTTP_RPC_URL"); single != "" {
			cfg.HttpRpcUrls = []string{single}
		}
	}
	if len(cfg.HttpRpcUrls) > 0 {
		cfg.HttpRpcUrl = cfg.HttpRpcUrls[0]
	}

	return cfg, nil
}
