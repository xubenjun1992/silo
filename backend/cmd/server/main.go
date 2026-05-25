package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	"github.com/silo-protocol/backend/internal/api"
	"github.com/silo-protocol/backend/internal/config"
	"github.com/silo-protocol/backend/internal/database"
	"github.com/silo-protocol/backend/internal/event"
	"github.com/silo-protocol/backend/internal/indexer"
	"github.com/silo-protocol/backend/internal/liquidation"
	"github.com/silo-protocol/backend/internal/model"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load config")
	}

	// ── Database ──
	if err := database.Init(cfg.DBDsn); err != nil {
		log.Fatal().Err(err).Msg("Failed to init database")
	}
	defer database.Close()

	// ── Redis ──
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to Redis")
	}
	defer rdb.Close()
	log.Info().Str("addr", cfg.RedisAddr).Msg("Redis connected")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	/*═══════════════════════════════════════════════════════════════════════════
	   Pool configs — loaded once, used by position tracker & monitor
	   ═══════════════════════════════════════════════════════════════════════════*/

	poolConfigs := loadPoolConfigs()
	if len(poolConfigs) == 0 {
		log.Warn().Msg("No pools found in DB — position tracking & liquidation will be idle until pools are created")
	}

	/*═══════════════════════════════════════════════════════════════════════════
	   Liquidation components (always init, executor optional)
	   ═══════════════════════════════════════════════════════════════════════════*/

	positionStore := liquidation.NewPositionStore(rdb)
	tracker := liquidation.NewPositionTracker(positionStore)

	// Price feeder — maps token address → Chainlink aggregator
	if len(cfg.PriceFeedAddrs) == 0 {
		log.Warn().Msg("PRICE_FEED_ADDRS is empty — oracle prices will not be available")
	}
	feeder := liquidation.NewOraclePriceFeeder(cfg.HttpRpcUrls, cfg.PriceFeedAddrs)

	// onEventConfirmed: called by consumer after safe-block DB insert.
	// Updates Redis position cache with oracle prices, timestamps, and pool risk params.
	onEventConfirmed := func(event *model.PoolEvent) {
		pool, ok := poolConfigs[event.PoolAddr]
		if !ok {
			log.Warn().Str("pool", event.PoolAddr).Msg("Event from unknown pool, skipping position update")
			return
		}

		// Prices: try Redis cache first (updated by monitor every scan), fall back to oracle
		var colPrice, debtPrice decimal.Decimal
		var priceTimestamp int64

		colPrice = getCachedDecimal(ctx, rdb, event.PoolAddr+":col")
		if colPrice.IsZero() {
			colPrice, priceTimestamp, _ = feeder.GetPrice(ctx, pool.CollateralAsset)
		}
		debtPrice = getCachedDecimal(ctx, rdb, event.PoolAddr+":debt")
		if debtPrice.IsZero() {
			var ts int64
			debtPrice, ts, _ = feeder.GetPrice(ctx, pool.DepositAsset)
			if ts < priceTimestamp || priceTimestamp == 0 {
				priceTimestamp = ts
			}
		}

		// Cache prices by poolAddr (for subsequent events) and by tokenAddr
		if colPrice.IsPositive() {
			cacheDecimal(ctx, rdb, event.PoolAddr+":col", colPrice)
			cacheDecimal(ctx, rdb, pool.CollateralAsset, colPrice)
		}
		if debtPrice.IsPositive() {
			cacheDecimal(ctx, rdb, event.PoolAddr+":debt", debtPrice)
			cacheDecimal(ctx, rdb, pool.DepositAsset, debtPrice)
		}

		minCR := riskTierMinCollatRatioDecimal(pool.RiskTier)
		tracker.Apply(ctx, event, colPrice, debtPrice, minCR, priceTimestamp)
	}

	/*═══════════════════════════════════════════════════════════════════════════
	   Kafka pipeline: Chain → Listener → Kafka → Consumer → DB + Redis
	   ═══════════════════════════════════════════════════════════════════════════*/

	producer, err := event.NewKafkaProducer(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Kafka producer")
	}
	defer producer.Close()

	consumer, err := event.NewKafkaConsumer(cfg, onEventConfirmed)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Kafka consumer")
	}
	go consumer.Start(ctx)

	// ReorgHandler: chain reorganization detection + 4-layer rollback
	httpClient, err := ethclient.Dial(cfg.HttpRpcUrl)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create HTTP client for reorg handler")
	}
	reorgHandler := event.NewReorgHandler(cfg, httpClient, rdb)

	listener, err := event.NewListener(cfg, producer, consumer, reorgHandler)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create event listener")
	}
	go listener.Start(ctx)

	/*═══════════════════════════════════════════════════════════════════════════
	   Indexer + API
	   ═══════════════════════════════════════════════════════════════════════════*/

	idx := indexer.New(cfg)
	go idx.Start(ctx)

	router := api.NewRouter(cfg)
	log.Info().Str("addr", cfg.ServerAddr).Msg("Starting API server")
	go func() {
		if err := router.Run(cfg.ServerAddr); err != nil {
			log.Fatal().Err(err).Msg("Server failed")
		}
	}()

	/*═══════════════════════════════════════════════════════════════════════════
	   Liquidation monitor (optional — requires LIQ_EXECUTOR_KEY)
	   ═══════════════════════════════════════════════════════════════════════════*/

	if cfg.ExecutorKey != "" {
		minProfitMargin := decimal.NewFromFloat(cfg.MinProfitMargin)
		executor, err := liquidation.NewLiquidationExecutor(
			cfg.HttpRpcUrl,
			cfg.ExecutorKey,
			cfg.FlashbotsRelayURL,
			minProfitMargin,
		)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to create liquidation executor")
		}

		monitor := liquidation.NewMonitor(cfg, rdb, executor, feeder)
		go monitor.Start(ctx)
		log.Info().
			Str("flashbotsRelay", cfg.FlashbotsRelayURL).
			Str("minProfitMargin", minProfitMargin.String()).
			Msg("Liquidation monitor started")
	} else {
		log.Warn().Msg("LIQ_EXECUTOR_KEY not set — liquidation monitor disabled")
	}

	/*═══════════════════════════════════════════════════════════════════════════
	   Running
	   ═══════════════════════════════════════════════════════════════════════════*/

	log.Info().Msg("Silo backend running")
	log.Info().Msg("  Pipeline:     Chain → Listener(WS+polling) → Kafka → Consumer(safe-block) → DB + Redis")
	log.Info().Msg("  Topics:       silo.deposit | silo.withdraw | silo.borrow | silo.repay | silo.liquidate")
	log.Info().Msg("  Liquidation:  TWAP(30m) + CAS-breaker + decimal-precision + stale-price-filter + profitability-check + Flashbots")

	<-ctx.Done()
	log.Info().Msg("Shutting down...")
}

/*═══════════════════════════════════════════════════════════════════════════════
   Helpers
   ═══════════════════════════════════════════════════════════════════════════════*/

func loadPoolConfigs() map[string]model.Pool {
	var pools []model.Pool
	if err := database.DB.Find(&pools).Error; err != nil {
		log.Error().Err(err).Msg("Failed to load pool configs")
		return nil
	}
	configs := make(map[string]model.Pool, len(pools))
	for _, p := range pools {
		configs[p.Address] = p
	}
	log.Info().Int("count", len(configs)).Msg("Pool configs loaded")
	return configs
}

func riskTierMinCollatRatioDecimal(tier string) decimal.Decimal {
	switch tier {
	case "LOW":
		return decimal.NewFromFloat(1.1)
	case "MEDIUM":
		return decimal.NewFromFloat(1.2)
	case "HIGH":
		return decimal.NewFromFloat(1.5)
	default:
		return decimal.NewFromFloat(1.2)
	}
}

func getCachedDecimal(ctx context.Context, rdb *redis.Client, key string) decimal.Decimal {
	val, err := rdb.Get(ctx, "silo:price:"+key).Result()
	if err != nil {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(val)
	if err != nil {
		return decimal.Zero
	}
	return d
}

func cacheDecimal(ctx context.Context, rdb *redis.Client, key string, price decimal.Decimal) {
	redisKey := "silo:price:" + key
	if err := rdb.Set(ctx, redisKey, price.String(), 5*time.Minute).Err(); err != nil {
		log.Warn().Err(err).Str("key", redisKey).Msg("Failed to cache price")
	}
}
