# Silo — 风险隔离借贷协议

去中心化、可组合、风险隔离的借贷协议。核心原则：**不同资产池 / 风险等级完全隔离，单池坏账不传导至其他池**。

---

## 1. 项目结构

```
silo/
├── contracts/          # Solidity 智能合约 (Hardhat + TypeScript)
│   ├── src/core/       # Pool.sol, PoolFactory.sol, ProtocolConfig.sol
│   ├── src/interest/   # InterestRateModel.sol (kink 利率曲线)
│   ├── src/liquidation/# Liquidator.sol
│   ├── src/governance/ # Governance.sol (OpenZeppelin Governor + Timelock)
│   ├── src/oracle/     # ChainlinkOracle.sol, PriceOracleAggregator.sol (多源中位数)
│   ├── src/tokens/     # RToken.sol (存款凭证)
│   ├── src/libraries/  # MathLib.sol, Errors.sol, RiskCalc.sol
│   ├── src/interfaces/ # IPool, IOracle, IInterestRateModel, IRToken 等
│   ├── scripts/        # deploy.ts
│   └── test/           # Pool.test.ts, InterestRate.test.ts, Liquidation.test.ts
│
├── backend/            # Go 后端服务
│   ├── cmd/server/     # main.go — 服务入口
│   ├── internal/
│   │   ├── config/     # 配置加载 (viper, .env)
│   │   ├── model/      # 数据模型 (Pool, PoolEvent, PoolStats, SyncState)
│   │   ├── database/   # GORM + MySQL
│   │   ├── event/      # 链上事件管道: Listener → Kafka → Consumer → DB + Redis
│   │   ├── indexer/    # 链上数据索引进 DB
│   │   ├── liquidation/# 清算引擎: Monitor, TWAP, CircuitBreaker, Executor
│   │   ├── api/        # REST API (Gin)
│   │   └── service/    # 业务服务层
│   └── migrations/     # 数据库迁移
│
└── frontend/           # React + TypeScript + Vite + Tailwind
    └── src/
        ├── pages/      # Home, Pools, PoolDetail, Dashboard, Governance
        ├── components/ # Layout
        ├── hooks/      # useWallet, usePool, useContract
        ├── contracts/  # 合约地址配置
        └── utils/      # format, web3 工具
```

---

## 2. 核心设计

### 2.1 池化隔离

- 每个 `(存款资产, 抵押资产)` 对部署一个独立 `Pool` 合约
- 池之间无共享资金、无债务关联、清算互不影响
- 单池被攻击或坏账 → 其他池完全不受影响

### 2.2 风险分层

`ProtocolConfig` 管理三级风险配置：

| 等级 | 最低抵押率 | 清算阈值 | 清算激励 |
|------|-----------|---------|---------|
| LOW  | 120%      | 110%    | 5%      |
| MEDIUM | 150%   | 125%    | 8%      |
| HIGH | 200%      | 150%    | 10%     |

### 2.3 动态利率 (kink 模型)

`InterestRateModel` 实现分段线性利率曲线：
- 利用率 ≤ kink (80%)：`利率 = baseRate + utilization × slope1`
- 利用率 > kink：`利率 = baseRate + kink×slope1 + (util-kink)×slope2`
- 利用率低 → 利率低鼓励借贷；利用率高 → 利率飙升抑制挤兑
- reserveFactor 控制准备金提取比例

### 2.4 超额抵押 + 清算

- 借款前检查健康因子 `≥ 1.0`（基于抵押率）
- 健康因子 `< 1.0` → 清算人可触发清算
- 清算奖励归清算人，剩余抵押品返还借款人

---

## 3. 智能合约

| 合约 | 功能 |
|------|------|
| `Pool` | 独立借贷池：deposit / withdraw / borrow / repay / liquidate，含 RToken 和利息累积 |
| `PoolFactory` | 仅治理可创建新池，注册到 ProtocolConfig |
| `ProtocolConfig` | 全局参数：风险等级配置、池注册、全局借款上限、紧急暂停 |
| `InterestRateModel` | kink 分段利率曲线，利用率驱动 |
| `Liquidator` | 独立清算执行器，支持单池清算和跨池批量清算（互不影响） |
| `Governance` | OpenZeppelin Governor + Timelock：投票延迟、投票期、法定人数、时间锁 |
| `PriceOracleAggregator` | 多源中位数预言机，防止单源操纵 |
| `ChainlinkOracle` | Chainlink AggregatorV3 适配器 |
| `RToken` | 每池独立的存款凭证代币，汇率随利息累积增长 |

安全措施：OpenZeppelin AccessControl + ReentrancyGuard + Pausable，重入防护，仅白名单清算人可执行清算。

---

## 4. 后端架构

### 4.1 事件管道

```
链上事件 → Listener(WS实时 + polling回填) → Kafka → Consumer(safe-block确认) → MySQL + Redis
```

- **Listener**: WebSocket 订阅 + eth_getLogs 定时轮询，双通道互补
- **Kafka**: 按事件类型分 topic（silo.deposit / silo.withdraw / silo.borrow / silo.repay / silo.liquidate）
- **Consumer**: 等待 N 个区块确认后写入 DB，同步更新 Redis 持仓缓存
- **ReorgHandler**: 链重组检测 + 4 层回滚（Redis → DB → Consumer 缓冲 → Listener checkpoint）

### 4.2 清算引擎

每轮扫描周期（可配置，默认 15s）：

1. 获取预言机现货价格
2. 记录 TWAP 滚动窗口 → 计算 TWAP + 偏差
3. CAS 保护的断路器评估市场状态：NORMAL / EXTREME / PAUSED
4. 用 TWAP 价格重算所有 Redis 持仓的健康因子
5. 扫描可清算仓位（过滤过期价格）
6. 盈利性模拟（预估 Gas vs 预期奖励）
7. 按优先级排序执行清算

特性：
- **TWAP**: 30 分钟滑动窗口，防闪电贷价格操纵
- **断路器**: CAS 乐观锁，偏差 ≥ 15% 暂停清算，冷却后可自动恢复
- **速率限制**: EXTREME 模式限制每批清算数量
- **MEV 保护**: Flashbots / MEV-boost 私有交易提交
- **去重锁**: Redis SetNX 防止多实例重复清算
- **盈利性门槛**: 最低利润率可配置（默认 10%）

### 4.3 技术栈

| 组件 | 技术 |
|------|------|
| 语言 | Go 1.22 |
| HTTP | Gin |
| 数据库 | MySQL (GORM) |
| 缓存/状态 | Redis (持仓、TWAP 窗口、断路器、价格缓存) |
| 消息队列 | Kafka (segmentio/kafka-go) |
| 链交互 | go-ethereum (ethclient) |
| 预言机 | Chainlink AggregatorV3 |
| 日志 | zerolog |
| 配置 | viper (.env) |

---

## 5. 前端

- **框架**: React + TypeScript + Vite
- **样式**: Tailwind CSS
- **路由**: react-router-dom (Home / Pools / PoolDetail / Dashboard / Governance)
- **钱包**: 自定义 hook 连接以太坊钱包
- **合约交互**: ethers.js

---

## 6. 风险控制

| 层级 | 机制 |
|------|------|
| 池级 | 单池借款上限、单资产抵押上限、动态抵押率 |
| 全局 | 风险等级隔离、紧急暂停（单池非全局）、全局借款上限 |
| 预言机 | Chainlink + 多源中位数聚合、TWAP 价格清算、过期价格过滤 |
| 坏账 | 单池坏账仅消耗该池储备金，其他池零传染 |
| MEV | Flashbots 私有交易、盈利性门槛 |
| 重组 | 4 层回滚机制保证事件一致性 |

---

## 7. 系统角色

- **存款人**: 存入资产获取 rToken，按利用率动态利率自动计息
- **借款人**: 超额抵押借出资产，实时监控健康因子
- **清算人**: 监控链上/链下，清算不健康仓位获取奖励
- **治理者**: 通过 Governance 合约投票调整参数、新增池、升级合约

---

## 8. 部署与运行

### 合约

```bash
cd contracts
npm install
npx hardhat compile
npx hardhat run scripts/deploy.ts --network <network>
```

### 后端

```bash
cd backend
cp .env.example .env  # 编辑配置
go run cmd/server/main.go
```

### 前端

```bash
cd frontend
npm install
npm run dev
```
