# Silo — 风险隔离借贷协议

去中心化、可组合的借贷协议。核心原则：**每个资产对和风险等级运行在独立池中，单池坏账零传染**。

---

## 项目介绍

Silo 是一套超额抵押借贷系统，业务覆盖 **存款生息、抵押借款、清算、利率调节、链上治理** 五大场景。系统由三层构成：

| 层 | 技术栈 | 职责 |
|---|--------|------|
| 合约 | Solidity + Hardhat + OpenZeppelin | 核心借贷逻辑、利率模型、清算、治理 |
| 后端 | Go + Gin + Kafka + Redis + MySQL | 链上事件索引、清算引擎、API 服务 |
| 前端 | React + TypeScript + Vite + Tailwind | 用户交互界面、钱包连接、仓位管理 |

### 核心特性

- **资金池完全隔离**：每个 `(存款资产, 抵押资产)` 对部署独立合约，无共享流动性，无跨池债务
- **三级风险分层**：LOW / MEDIUM / HIGH，各自独立抵押率、清算阈值、借款上限
- **动态利率曲线**：kink 模型，利用率驱动 — 低利用率鼓励借贷，高利用率抑制挤兑
- **超额抵押清算**：健康因子实时监控，TWAP 防价格操纵，断路器 + 盈利性过滤
- **链上治理**：OpenZeppelin Governor + Timelock，参数调整、新池创建、合约升级

---

## 业务流程

### 存款

```
用户选择目标池 → 存入资产（USDT/ETH 等）→ 获得 rToken 存款凭证 → 按池利率自动计息
```

- rToken 汇率随利息累积增长，赎回时按最新汇率兑换
- 存入资产进入池流动性，供借款人借出

### 借款

```
用户选择抵押资产 & 借款池 → 存入抵押品 → 系统按抵押率计算可借额度 → 借出目标资产
```

- 借款前校验健康因子 ≥ 1.0，不满足则拒绝
- 借款后实时监控抵押率，利率按利用率动态调整
- 债务以 shares + borrowIndex 方式累积计息

### 还款

```
用户归还借款资产 + 利息 → 系统销毁对应债务 shares → 恢复可借额度 → 可取回抵押品
```

---

## 清算流程

清算引擎是系统的核心风控模块，运行在后端服务中，流程如下：

### 整体架构

```
链上事件 → Listener(WS + polling) → Kafka → Consumer → DB + Redis 持仓缓存
                                                          ↓
                                              清算 Monitor 按周期扫描
```

### 单轮扫描流程

```
┌─────────────────────────────────────────────────────────┐
│  1. 获取预言机现货价格 (Chainlink AggregatorV3)          │
│     ↓                                                   │
│  2. 记录 TWAP 滚动窗口 → 计算 TWAP + 偏差 (默认30min)    │
│     ↓                                                   │
│  3. CAS 断路器评估市场状态                               │
│     ├── NORMAL  (偏差 < 5%)  → 正常清算                  │
│     ├── EXTREME (偏差 5%~15%) → 限速清算                 │
│     └── PAUSED  (偏差 ≥ 15%)  → 暂停清算，等待冷却       │
│     ↓                                                   │
│  4. 用 TWAP 价格重算所有持仓健康因子                     │
│     ↓                                                   │
│  5. 扫描健康因子 < 1.0 的仓位（过滤过期价格）            │
│     ↓                                                   │
│  6. 盈利性模拟：预估 Gas 成本 vs 预期清算奖励            │
│     ↓                                                   │
│  7. 按优先级排序 → Redis 去重锁 → 执行链上清算           │
│     └── 支持 Flashbots 私有交易防 MEV                    │
└─────────────────────────────────────────────────────────┘
```

### 清算触发条件

- 健康因子 = `(抵押品价值 × 抵押率) / 债务价值 < 1.0`
- 价格使用 TWAP 而非现货价，防止闪电贷瞬时操纵
- 价格超过配置的过期时间（默认 1h）视为过期，跳过该仓位

### 断路器机制

| 状态 | 触发条件 | 行为 |
|------|---------|------|
| NORMAL | 现货 vs TWAP 偏差 < 5% | 正常清算，每批最多 20 个 |
| EXTREME | 偏差 5% ~ 15% | 限速清算，每批最多 3 个 |
| PAUSED | 偏差 ≥ 15% | 完全暂停，等待冷却期（5min）后重评估 |

断路器使用 Redis Lua 脚本实现 CAS 乐观锁，防止多实例并发冲突。冷却期过后偏差恢复正常则自动关闭断路器。

### 盈利性过滤

- 每笔清算执行前模拟：预期奖励 / 预估 Gas 成本 ≥ 最低利润率（默认 1.1 = 10%）
- 不满足则跳过，避免清算人亏损

### 去重与并发

- Redis SetNX 锁（TTL 5min）：同一借款人同一时刻只允许一个清算在执行
- 成功后保留锁 30s 防止事件消费者竞态

---

## 风控体系

### 池级风控

- 单池借款上限
- 单资产抵押上限
- 动态抵押率（治理可调整）
- 单池紧急暂停（Pausable），不影响其他池

### 全局风控

- 三级风险分层隔离，高风险池额度受限
- 全局借款上限
- 仅治理可创建新池、修改参数

### 预言机保护

| 机制 | 说明 |
|------|------|
| Chainlink | 去中心化预言机，AggregatorV3 接口 |
| 多源聚合 | PriceOracleAggregator 取多源中位数，防单源故障 |
| TWAP | 30 分钟滑动窗口，防止闪电贷瞬时价格操纵 |
| 过期过滤 | 超过配置时间（默认 1h）的价格视为过期 |

### 坏账处理

- 单池坏账仅消耗该池储备金
- 储备金不足时仅影响该池存款人
- **其他池完全隔离，零传染** — 这是 Silo 区别于 Compound/AAVE 的核心优势

### 链重组保护

4 层回滚机制：
1. Redis 持仓缓存 → 删除重组区块数据
2. MySQL 事件表 → 按 blockHash 删除
3. Consumer 内存缓冲 → 清空未确认事件
4. Listener checkpoint → 回退到分叉点重新拉取

---

## 利率模型

采用 kink 分段线性曲线：

```
利用率 ≤ 80%: borrowRate = baseRate + utilization × slope1
利用率 > 80%: borrowRate = baseRate + 80%×slope1 + (util-80%)×slope2
supplyRate  = borrowRate × utilization × (1 - reserveFactor)
```

- 低利用率 → 低利率鼓励借贷
- 高利用率 → 利率飙升抑制挤兑、吸引存款
- reserveFactor 截留部分利息进入准备金

---

## 系统角色

| 角色 | 职责 |
|------|------|
| 存款人 | 存入资产获取 rToken 赚取利息 |
| 借款人 | 超额抵押借出资产，维持健康因子 |
| 清算人 | 监控并清算不健康仓位，获取清算奖励 |
| 治理者 | 投票调整参数、创建新池、升级合约 |

---

## 项目结构

```
silo/
├── contracts/src/
│   ├── core/           Pool.sol, PoolFactory.sol, ProtocolConfig.sol
│   ├── interest/       InterestRateModel.sol
│   ├── liquidation/    Liquidator.sol
│   ├── governance/     Governance.sol
│   ├── oracle/         ChainlinkOracle.sol, PriceOracleAggregator.sol
│   ├── tokens/         RToken.sol
│   ├── libraries/      MathLib.sol, Errors.sol, RiskCalc.sol
│   └── interfaces/     IPool, IOracle, IInterestRateModel 等
│
├── backend/internal/
│   ├── event/          Listener → Kafka → Consumer 事件管道
│   ├── liquidation/    Monitor, TWAP, CircuitBreaker, Executor
│   ├── indexer/        链上数据索引
│   ├── api/            REST API (Gin)
│   └── config/         配置管理 (viper)
│
└── frontend/src/
    ├── pages/          Home, Pools, PoolDetail, Dashboard, Governance
    ├── hooks/          useWallet, usePool, useContract
    └── components/     Layout
```

---

## 快速开始

```bash
# 合约
cd contracts && npm install && npx hardhat compile

# 后端
cd backend && cp .env.example .env  # 编辑配置后
go run cmd/server/main.go

# 前端
cd frontend && npm install && npm run dev
```
