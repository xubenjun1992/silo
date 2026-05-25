package database

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/silo-protocol/backend/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Init(dsn string) error {
	var err error
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}

	sqlDB, _ := DB.DB()
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)

	log.Info().Msg("Database connected, running auto-migration...")

	if err := DB.AutoMigrate(
		&model.Pool{},
		&model.PoolEvent{},
		&model.PoolStats{},
		&model.SyncState{},
	); err != nil {
		return fmt.Errorf("auto-migration failed: %w", err)
	}

	log.Info().Msg("Auto-migration completed")
	return nil
}

func Close() {
	if DB != nil {
		sqlDB, _ := DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
}
