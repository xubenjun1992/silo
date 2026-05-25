-- ============================================================
-- Silo Protocol — Database Schema
-- ============================================================

-- 1. Registered lending pools (created by PoolFactory)
CREATE TABLE IF NOT EXISTS `pools` (
    `id`               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `address`          VARCHAR(42)     NOT NULL COMMENT 'Pool contract address',
    `deposit_asset`    VARCHAR(42)     NOT NULL COMMENT 'Token deposited by lenders & borrowed by borrowers',
    `collateral_asset` VARCHAR(42)     NOT NULL COMMENT 'Token pledged as collateral',
    `risk_tier`        VARCHAR(10)     NOT NULL DEFAULT 'MEDIUM' COMMENT 'LOW | MEDIUM | HIGH',
    `created_at`       DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_address` (`address`),
    KEY `idx_risk_tier` (`risk_tier`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='Isolated lending pools registered by the PoolFactory';


-- 2. On-chain events emitted by pools
CREATE TABLE IF NOT EXISTS `pool_events` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `pool_addr`  VARCHAR(42)     NOT NULL COMMENT 'Pool contract address that emitted the event',
    `event_type` VARCHAR(20)     NOT NULL COMMENT 'DEPOSIT | WITHDRAW | BORROW | REPAY | LIQUIDATE',
    `user_addr`  VARCHAR(42)     NOT NULL COMMENT 'Address of lender / borrower / liquidator',
    `amount`     VARCHAR(78)     NOT NULL COMMENT 'Token amount as decimal string (uint256 compatible)',
    `tx_hash`    VARCHAR(66)     NOT NULL COMMENT 'Transaction hash',
    `block_num`  BIGINT UNSIGNED NOT NULL COMMENT 'Block number',
    `created_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    KEY `idx_pool_addr` (`pool_addr`),
    KEY `idx_event_type` (`event_type`),
    KEY `idx_user_addr` (`user_addr`),
    KEY `idx_pool_type` (`pool_addr`, `event_type`),
    KEY `idx_block_num` (`block_num`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='On-chain events captured by the event listener';


-- 3. Event deduplication index (WS + polling both may see the same event)
ALTER TABLE `pool_events` ADD UNIQUE KEY `uk_tx_event` (`tx_hash`, `event_type`);


-- 4. Sync state — tracks the last processed block for each event source
CREATE TABLE IF NOT EXISTS `sync_states` (
    `source`     VARCHAR(100)    NOT NULL COMMENT 'Event source identifier, e.g. pool_events',
    `last_block` BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Highest block fully processed',
    `updated_at` BIGINT          NOT NULL DEFAULT 0 COMMENT 'Unix timestamp of last update',
    PRIMARY KEY (`source`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='Checkpoint tracking for dual-channel event ingestion';


-- 5. Aggregated pool statistics (updated periodically by indexer)
CREATE TABLE IF NOT EXISTS `pool_stats` (
    `pool_addr`           VARCHAR(42)     NOT NULL COMMENT 'Pool contract address',
    `total_liquidity`     VARCHAR(78)     NOT NULL DEFAULT '0' COMMENT 'Total supplied (uint256 string)',
    `total_debt`          VARCHAR(78)     NOT NULL DEFAULT '0' COMMENT 'Total outstanding debt (uint256 string)',
    `utilization_rate`    DOUBLE          NOT NULL DEFAULT 0 COMMENT 'utilization = debt / liquidity',
    `borrow_rate`         DOUBLE          NOT NULL DEFAULT 0 COMMENT 'Current borrow APY (percentage)',
    `supply_rate`         DOUBLE          NOT NULL DEFAULT 0 COMMENT 'Current supply APY (percentage)',
    `min_collateral_pct`  DOUBLE          NOT NULL DEFAULT 0 COMMENT 'Minimum collateral ratio in percentage',
    `updated_at`          BIGINT          NOT NULL DEFAULT 0 COMMENT 'Unix timestamp of last update',
    PRIMARY KEY (`pool_addr`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='Current state snapshot of each pool, refreshed by the indexer';
