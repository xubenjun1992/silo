// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/access/AccessControl.sol";
import "./Pool.sol";
import "./ProtocolConfig.sol";

/**
 * @title PoolFactory
 * @notice Deploys isolated lending pools. Only governance can create new pools.
 *         Each pool is a standalone contract — no shared state, no cross-pool risk.
 */
contract PoolFactory is AccessControl {
    bytes32 public constant POOL_CREATOR_ROLE = keccak256("POOL_CREATOR_ROLE");

    ProtocolConfig public protocolConfig;
    address[] public allPools;
    mapping(address => bool) public isPool;

    event PoolCreated(
        address indexed pool,
        address depositAsset,
        address collateralAsset,
        ProtocolConfig.RiskTier riskTier
    );

    constructor(address _protocolConfig) {
        protocolConfig = ProtocolConfig(_protocolConfig);
        _grantRole(DEFAULT_ADMIN_ROLE, msg.sender);
        _grantRole(POOL_CREATOR_ROLE, msg.sender);
    }

    /**
     * @notice Deploy a new isolated pool.
     * @param depositAsset  The asset lenders deposit & borrowers borrow
     * @param collateralAsset The asset borrowers must pledge
     * @param oracle        Price oracle for collateral + deposit asset
     * @param rateModel     Interest rate model contract
     * @param rTokenName    Name for the receipt token
     * @param rTokenSymbol  Symbol for the receipt token
     */
    function createPool(
        address depositAsset,
        address collateralAsset,
        address oracle,
        address rateModel,
        string calldata rTokenName,
        string calldata rTokenSymbol
    ) external onlyRole(POOL_CREATOR_ROLE) returns (address pool) {
        pool = address(new Pool(depositAsset, collateralAsset, oracle, rateModel, rTokenName, rTokenSymbol));
        allPools.push(pool);
        isPool[pool] = true;
        protocolConfig.registerPool(pool);
        emit PoolCreated(pool, depositAsset, collateralAsset, ProtocolConfig.RiskTier.LOW);
    }

    function poolCount() external view returns (uint256) {
        return allPools.length;
    }
}
