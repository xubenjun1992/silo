// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IOracle {
    function getPrice(address asset) external view returns (uint256 price, uint8 decimals);
    function isTrustedSource(address source) external view returns (bool);
    event PriceUpdated(address indexed asset, uint256 price, uint256 timestamp);
    event SourceAdded(address indexed source);
    event SourceRemoved(address indexed source);
}
