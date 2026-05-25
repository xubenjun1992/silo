// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "../interfaces/IPool.sol";

/**
 * @title Liquidator
 * @notice Standalone liquidation executor. Monitors pools and liquidates under-collateralized positions.
 *         Liquidation bonus incentivizes third-party liquidators.
 */
contract Liquidator is ReentrancyGuard {
    event LiquidationExecuted(address indexed pool, address indexed borrower, uint256 debtRepaid, uint256 collateralSeized, uint256 reward);

    /**
     * @notice Liquidate an under-collateralized borrower in a specific pool.
     * @param pool Address of the isolated pool
     * @param borrower Address of the underwater borrower
     */
    function liquidate(address pool, address borrower) external nonReentrant returns (uint256 debtRepaid, uint256 collateralSeized, uint256 reward) {
        IPool target = IPool(pool);
        require(target.getHealthFactor(borrower) < 1e18, "Not liquidatable");

        (debtRepaid, collateralSeized, reward) = target.liquidate(borrower);
        emit LiquidationExecuted(pool, borrower, debtRepaid, collateralSeized, reward);
    }

    /**
     * @notice Batch liquidate across multiple pools (risk-isolated: each liquidation is independent).
     */
    function batchLiquidate(address[] calldata pools, address[] calldata borrowers)
        external nonReentrant
    {
        require(pools.length == borrowers.length, "Length mismatch");
        for (uint256 i = 0; i < pools.length; i++) {
            IPool target = IPool(pools[i]);
            if (target.getHealthFactor(borrowers[i]) < 1e18) {
                try target.liquidate(borrowers[i]) returns (uint256 repaid, uint256 seized, uint256 reward) {
                    emit LiquidationExecuted(pools[i], borrowers[i], repaid, seized, reward);
                } catch {
                    continue; // skip failed liquidations, next pool is unaffected
                }
            }
        }
    }
}
