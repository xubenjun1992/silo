// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/access/Ownable.sol";
import "../interfaces/IOracle.sol";

interface IChainlinkAggregator {
    function latestRoundData() external view returns (
        uint80 roundId, int256 answer, uint256 startedAt, uint256 updatedAt, uint80 answeredInRound
    );
    function decimals() external view returns (uint8);
}

/**
 * @title ChainlinkOracle
 * @notice Wraps Chainlink price feeds. Supports multiple assets → aggregator mappings.
 */
contract ChainlinkOracle is IOracle, Ownable {
    uint256 public constant MAX_STALENESS = 1 hours;

    mapping(address => address) public aggregators; // asset → chainlink aggregator

    constructor(address initialOwner) Ownable(initialOwner) {}

    function setAggregator(address asset, address aggregator) external onlyOwner {
        aggregators[asset] = aggregator;
    }

    function getPrice(address asset) external view returns (uint256 price, uint8 decimals) {
        address agg = aggregators[asset];
        require(agg != address(0), "No aggregator");
        (, int256 answer,, uint256 updatedAt,) = IChainlinkAggregator(agg).latestRoundData();
        require(updatedAt + MAX_STALENESS >= block.timestamp, "Price stale");
        require(answer > 0, "Price <= 0");
        decimals = IChainlinkAggregator(agg).decimals();
        price = uint256(answer);
    }

    function isTrustedSource(address source) external pure returns (bool) {
        return source != address(0);
    }
}
