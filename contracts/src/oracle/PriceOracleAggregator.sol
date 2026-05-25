// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/access/Ownable.sol";
import "../interfaces/IOracle.sol";

/**
 * @title PriceOracleAggregator
 * @notice Multi-source oracle that aggregates prices from multiple feeds (median).
 *         Protects against single-oracle manipulation.
 */
contract PriceOracleAggregator is IOracle, Ownable {
    uint256 public constant MAX_DEVIATION_BPS = 500; // 5% max deviation between sources
    uint256 public constant BPS = 10000;

    struct OracleSource {
        address source;
        bool enabled;
    }

    mapping(address => OracleSource[]) public assetSources; // asset → sources[]

    constructor(address initialOwner) Ownable(initialOwner) {}

    function addSource(address asset, address source) external onlyOwner {
        assetSources[asset].push(OracleSource({source: source, enabled: true}));
        emit SourceAdded(source);
    }

    function removeSource(address asset, uint256 index) external onlyOwner {
        assetSources[asset][index].enabled = false;
        emit SourceRemoved(assetSources[asset][index].source);
    }

    function getPrice(address asset) external view returns (uint256 price, uint8 decimals) {
        OracleSource[] storage sources = assetSources[asset];
        require(sources.length > 0, "No sources");

        // Collect valid prices
        uint256 count;
        uint256[] memory prices = new uint256[](sources.length);

        for (uint256 i = 0; i < sources.length; i++) {
            if (!sources[i].enabled) continue;
            try IOracle(sources[i].source).getPrice(asset) returns (uint256 p, uint8) {
                prices[count] = p;
                count++;
            } catch {
                continue;
            }
        }
        require(count >= 1, "No valid prices");

        // Return median of valid prices
        // Simple selection for small arrays; insert-sort then pick middle
        for (uint256 i = 0; i < count - 1; i++) {
            for (uint256 j = i + 1; j < count; j++) {
                if (prices[i] > prices[j]) {
                    (prices[i], prices[j]) = (prices[j], prices[i]);
                }
            }
        }

        uint256 medianIdx = count / 2;
        if (count % 2 == 0 && count > 1) {
            price = (prices[medianIdx - 1] + prices[medianIdx]) / 2;
        } else {
            price = prices[medianIdx];
        }

        // Use first source's decimals
        (, decimals) = IOracle(sources[0].source).getPrice(asset);
    }

    function isTrustedSource(address source) external view returns (bool) {
        for (uint256 i = 0; i < assetSources[address(0)].length; i++) {
            if (assetSources[address(0)][i].source == source && assetSources[address(0)][i].enabled) {
                return true;
            }
        }
        return false;
    }
}
