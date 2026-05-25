// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";

interface IRToken is IERC20 {
    function mint(address to, uint256 amount) external;
    function burn(address from, uint256 amount) external;
    function underlyingAsset() external view returns (address);
    function exchangeRate() external view returns (uint256);
    function balanceOfUnderlying(address account) external view returns (uint256);
    function setExchangeRate(uint256 newRate) external;
}
