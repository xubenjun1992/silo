// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/**
 * @title MathLib
 * @notice Fixed-point math utilities with Wad (1e18) and Ray (1e27) precision.
 */
library MathLib {
    uint256 internal constant WAD = 1e18;
    uint256 internal constant RAY = 1e27;
    uint256 internal constant YEAR_IN_SECONDS = 365 days;

    function wadMul(uint256 a, uint256 b) internal pure returns (uint256) {
        return (a * b) / WAD;
    }

    function wadDiv(uint256 a, uint256 b) internal pure returns (uint256) {
        return (a * WAD) / b;
    }

    function rayMul(uint256 a, uint256 b) internal pure returns (uint256) {
        return (a * b) / RAY;
    }

    function rayDiv(uint256 a, uint256 b) internal pure returns (uint256) {
        return (a * RAY) / b;
    }

    function ratePerSecond(uint256 annualRate) internal pure returns (uint256) {
        return annualRate / YEAR_IN_SECONDS;
    }
}
