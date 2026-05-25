// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "../interfaces/IInterestRateModel.sol";
import "../libraries/MathLib.sol";

/**
 * @title InterestRateModel
 * @notice Dynamic interest rate driven by utilization.
 *
 *   Curve: rate = baseRate + (utilization * slopeMultiplier)
 *   When utilization > kink (e.g. 80%), slope becomes steeper.
 *
 *   borrowRate = baseRate + utilization * slope1          (util ≤ kink)
 *   borrowRate = baseRate + kink*slope1 + (util-kink)*slope2  (util > kink)
 */
contract InterestRateModel is IInterestRateModel {
    using MathLib for uint256;

    uint256 public constant WAD = 1e18;
    uint256 public constant YEAR_IN_SECONDS = 365 days;

    /// @notice Base borrow rate (annual, WAD-precision). e.g. 0.02e18 = 2%
    uint256 public baseRate;
    /// @notice Utilization at which the slope changes (WAD-precision). e.g. 0.8e18 = 80%
    uint256 public kink;
    /// @notice Slope below kink (annual, WAD-precision)
    uint256 public slope1;
    /// @notice Slope above kink (annual, WAD-precision)
    uint256 public slope2;
    /// @notice Reserve factor: fraction of interest that goes to reserves (WAD-precision)
    uint256 public reserveFactor;

    constructor(uint256 _baseRate, uint256 _kink, uint256 _slope1, uint256 _slope2, uint256 _reserveFactor) {
        baseRate = _baseRate;
        kink = _kink;
        slope1 = _slope1;
        slope2 = _slope2;
        reserveFactor = _reserveFactor;
    }

    function utilizationRate(uint256 totalLiquidity, uint256 totalDebt) public pure returns (uint256) {
        if (totalLiquidity == 0) return 0;
        return (totalDebt * WAD) / totalLiquidity;
    }

    function getBorrowRate(uint256 util) public view returns (uint256) {
        if (util <= kink) {
            return baseRate + (util * slope1) / WAD;
        } else {
            uint256 normalComponent = baseRate + (kink * slope1) / WAD;
            uint256 excess = util - kink;
            return normalComponent + (excess * slope2) / WAD;
        }
    }

    function getSupplyRate(uint256 util, uint256 borrowRate) public view returns (uint256) {
        uint256 rateToPool = borrowRate * (WAD - reserveFactor) / WAD;
        return (util * rateToPool) / WAD;
    }
}
