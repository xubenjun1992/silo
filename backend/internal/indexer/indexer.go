package indexer

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/silo-protocol/backend/internal/config"
	"github.com/silo-protocol/backend/internal/database"
	"github.com/silo-protocol/backend/internal/model"
)

type Indexer struct {
	cfg      *config.Config
	interval time.Duration
}

func New(cfg *config.Config) *Indexer {
	return &Indexer{
		cfg:      cfg,
		interval: 15 * time.Second,
	}
}

func (idx *Indexer) Start(ctx context.Context) {
	log.Info().Dur("interval", idx.interval).Msg("Indexer starting")
	ticker := time.NewTicker(idx.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Indexer stopped")
			return
		case <-ticker.C:
			idx.reindex()
		}
	}
}

func (idx *Indexer) reindex() {
	log.Debug().Msg("Reindexing pool stats...")

	// Net liquidity = DEPOSIT - WITHDRAW + REPAY
	// Net debt      = BORROW - REPAY - LIQUIDATE
	type aggRow struct {
		PoolAddr     string
		DepositTotal string
		DebtTotal    string
	}

	var rows []aggRow
	database.DB.Model(&model.PoolEvent{}).
		Select(`
			pool_addr AS pool_addr,
			COALESCE(SUM(CASE
				WHEN event_type = 'DEPOSIT'  THEN amount
				WHEN event_type = 'WITHDRAW' THEN CONCAT('-', amount)
				WHEN event_type = 'REPAY'    THEN amount
				ELSE '0' END), '0') AS deposit_total,
			COALESCE(SUM(CASE
				WHEN event_type = 'BORROW'    THEN amount
				WHEN event_type = 'REPAY'     THEN CONCAT('-', amount)
				WHEN event_type = 'LIQUIDATE' THEN CONCAT('-', amount)
				ELSE '0' END), '0') AS debt_total
		`).
		Group("pool_addr").
		Scan(&rows)

	for _, row := range rows {
		stats := &model.PoolStats{
			PoolAddr:       row.PoolAddr,
			TotalLiquidity: row.DepositTotal,
			TotalDebt:      row.DebtTotal,
			UpdatedAt:      time.Now().Unix(),
		}
		database.DB.Save(stats)
	}

	log.Debug().Int("pools", len(rows)).Msg("Reindex complete")
}
