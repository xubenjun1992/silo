// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IInterestRateModel {
    function getBorrowRate(uint256 utilizationRate) external view returns (uint256);
    function getSupplyRate(uint256 utilizationRate, uint256 borrowRate) external view returns (uint256);
    function utilizationRate(uint256 totalLiquidity, uint256 totalDebt) external pure returns (uint256);
}
