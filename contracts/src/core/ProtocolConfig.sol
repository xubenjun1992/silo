// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/access/AccessControl.sol";

/**
 * @title ProtocolConfig
 * @notice Global protocol parameters. Governance-controlled.
 *         Manages risk tiers, global limits, and emergency controls.
 */
contract ProtocolConfig is AccessControl {
    bytes32 public constant GOVERNOR_ROLE = keccak256("GOVERNOR_ROLE");
    bytes32 public constant GUARDIAN_ROLE = keccak256("GUARDIAN_ROLE");

    /*═══════════════════════════════════ RISK TIERS ═══════════════════════════════════*/
    enum RiskTier { LOW, MEDIUM, HIGH }

    struct TierConfig {
        uint256 minCollateralRatio;   // e.g. 1.2e18 = 120%
        uint256 liquidationThreshold;  // e.g. 1.1e18 = 110%
        uint256 liquidationBonus;      // e.g. 0.05e18 = 5%
        uint256 maxPoolBorrow;         // borrow cap per pool
        bool active;
    }

    mapping(RiskTier => TierConfig) public tierConfigs;
    mapping(address => bool) public registeredPools;
    uint256 public globalBorrowCap;

    event TierUpdated(RiskTier indexed tier, TierConfig cfg);
    event PoolRegistered(address indexed pool);
    event GlobalBorrowCapSet(uint256 cap);
    event EmergencyPause(address indexed pool);
    event EmergencyUnpause(address indexed pool);

    constructor() {
        _grantRole(DEFAULT_ADMIN_ROLE, msg.sender);
        _grantRole(GOVERNOR_ROLE, msg.sender);
        _grantRole(GUARDIAN_ROLE, msg.sender);

        // Default tier configs
        tierConfigs[RiskTier.LOW]    = TierConfig(1.2e18, 1.1e18, 0.05e18, type(uint256).max, true);
        tierConfigs[RiskTier.MEDIUM] = TierConfig(1.5e18, 1.25e18, 0.08e18, type(uint256).max, true);
        tierConfigs[RiskTier.HIGH]   = TierConfig(2.0e18, 1.5e18, 0.10e18, type(uint256).max, true);
    }

    function setTierConfig(RiskTier tier, TierConfig calldata cfg) external onlyRole(GOVERNOR_ROLE) {
        tierConfigs[tier] = cfg;
        emit TierUpdated(tier, cfg);
    }

    function registerPool(address pool) external onlyRole(GOVERNOR_ROLE) {
        registeredPools[pool] = true;
        emit PoolRegistered(pool);
    }

    function setGlobalBorrowCap(uint256 cap) external onlyRole(GOVERNOR_ROLE) {
        globalBorrowCap = cap;
        emit GlobalBorrowCapSet(cap);
    }

    function emergencyPause(address pool) external onlyRole(GUARDIAN_ROLE) {
        require(registeredPools[pool], "Not a registered pool");
        emit EmergencyPause(pool);
    }
}
