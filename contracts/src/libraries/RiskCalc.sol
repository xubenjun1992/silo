// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "../libraries/MathLib.sol";

/**
 * @title RiskCalc
 * @notice Risk parameter calculations: health factor, max borrowable, liquidation amounts.
 */
library RiskCalc {
    using MathLib for uint256;

    /**
     * @dev healthFactor = (collateralValue * WAD) / (debt * minCollateralRatio)
     *  healthFactor < 1e18 → under-collateralized
     */
    function healthFactor(
        uint256 collateralValue,
        uint256 debt,
        uint256 minCollateralRatio
    ) internal pure returns (uint256) {
        if (debt == 0) return type(uint256).max;
        return (collateralValue * MathLib.WAD) / (debt * minCollateralRatio / MathLib.WAD);
    }

    function maxBorrowable(
        uint256 collateralValue,
        uint256 minCollateralRatio
    ) internal pure returns (uint256) {
        return (collateralValue * MathLib.WAD) / minCollateralRatio;
    }

    function liquidationCollateral(
        uint256 debtToRepay,
        uint256 collateralPrice,
        uint256 debtPrice,
        uint256 liquidationBonus
    ) internal pure returns (uint256) {
        uint256 debtValue = (debtToRepay * debtPrice) / MathLib.WAD;
        uint256 collateralValue = debtValue + (debtValue * liquidationBonus) / MathLib.WAD;
        return (collateralValue * MathLib.WAD) / collateralPrice;
    }
}
