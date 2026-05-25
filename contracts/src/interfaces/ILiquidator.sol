// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface ILiquidator {
    function liquidate(address pool, address borrower) external returns (uint256 debtRepaid, uint256 collateralSeized, uint256 reward);
    function calculateLiquidationAmount(address pool, address borrower) external view returns (uint256 maxDebtToRepay, uint256 collateralToSeize, uint256 reward);
}
