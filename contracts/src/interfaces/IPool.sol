// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "./IRToken.sol";

interface IPool {
    /*═══════════════════════════════════════════ EVENTS ═══════════════════════════════════════════*/
    event Deposited(address indexed lender, uint256 amount, uint256 rTokensMinted);
    event Withdrawn(address indexed lender, uint256 amount, uint256 rTokensBurned);
    event Borrowed(address indexed borrower, uint256 amount, uint256 debtShares);
    event Repaid(address indexed borrower, uint256 amount, uint256 debtSharesBurned);
    event Liquidated(address indexed liquidator, address indexed borrower, uint256 debtRepaid, uint256 collateralSeized, uint256 reward);
    event InterestRateUpdated(uint256 oldRate, uint256 newRate);

    /*═══════════════════════════════════════════ LENDER ═══════════════════════════════════════════*/
    function deposit(uint256 amount, address onBehalfOf) external returns (uint256 rTokens);
    function withdraw(uint256 rTokenAmount, address to) external returns (uint256 amount);

    /*═══════════════════════════════════════════ BORROWER ════════════════════════════════════════*/
    function borrow(uint256 amount, address to) external;
    function repay(uint256 amount, address onBehalfOf) external returns (uint256 repaid);

    /*═══════════════════════════════════════════ LIQUIDATION ═════════════════════════════════════*/
    function liquidate(address borrower) external returns (uint256 debtRepaid, uint256 collateralSeized, uint256 reward);

    /*═══════════════════════════════════════════ VIEWS ═══════════════════════════════════════════*/
    function depositAsset() external view returns (address);
    function collateralAsset() external view returns (address);
    function rToken() external view returns (IRToken);
    function getUtilizationRate() external view returns (uint256);
    function getBorrowRate() external view returns (uint256);
    function getSupplyRate() external view returns (uint256);
    function getHealthFactor(address borrower) external view returns (uint256);
    function minCollateralRatio() external view returns (uint256);
    function liquidationThreshold() external view returns (uint256);
    function liquidationBonus() external view returns (uint256);
    function totalLiquidity() external view returns (uint256);
    function totalDebt() external view returns (uint256);
}
