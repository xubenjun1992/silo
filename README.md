# Silo — Risk-Isolated Lending Protocol

A decentralized, composable lending protocol where **every asset pair and risk tier lives in its own isolated pool**. If one pool fails, others are completely unaffected — zero risk contagion.

## Key Features

- **Pool Isolation** — each `(deposit, collateral)` pair is a standalone contract; no shared liquidity, no cross-pool debt
- **Risk Tiering** — LOW / MEDIUM / HIGH tiers with independent collateral ratios, liquidation thresholds, and borrow caps
- **Dynamic Interest Rate** — kink-model curve driven by utilization; low utilization = cheap borrows, high utilization = steep rates
- **Over-Collateralized + Liquidation** — health factor monitoring, TWAP-based liquidation with circuit breaker and MEV protection
- **On-Chain Governance** — OpenZeppelin Governor + Timelock for parameter changes and pool creation

## Project Structure

| Layer | Stack | Directory |
|-------|-------|-----------|
| Contracts | Solidity + Hardhat + TypeScript | `contracts/` |
| Backend | Go + Gin + Kafka + Redis + MySQL | `backend/` |
| Frontend | React + TypeScript + Vite + Tailwind | `frontend/` |

## Quick Start

```bash
# Contracts
cd contracts && npm install && npx hardhat compile

# Backend
cd backend && cp .env.example .env && go run cmd/server/main.go

# Frontend
cd frontend && npm install && npm run dev
```

## Docs

See [silo.md](./silo.md) for full protocol documentation (中文).
