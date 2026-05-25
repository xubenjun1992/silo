// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/**
 * @title Errors
 * @notice Centralized error definitions for the entire protocol.
 */
library Errors {
    error Unauthorized(address caller);
    error InsufficientCollateral(address borrower, uint256 required, uint256 actual);
    error BelowLiquidationThreshold(uint256 healthFactor);
    error AboveMinCollateralRatio(uint256 ratio);
    error InsufficientLiquidity(uint256 requested, uint256 available);
    error InvalidAmount(uint256 amount);
    error InvalidAddress(address addr);
    error PoolPaused();
    error PoolNotPaused();
    error UtilizationTooHigh(uint256 utilization);
    error PriceOutdated(address asset, uint256 lastUpdate);
    error PriceDeviationTooHigh(address asset, uint256 deviation);
    error TransferFailed(address token, address from, address to, uint256 amount);
    error RepayTooMuch(uint256 maxRepay, uint256 attempted);
    error NothingToLiquidate(address borrower);
}
