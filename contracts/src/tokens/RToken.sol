// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import "@openzeppelin/contracts/access/AccessControl.sol";
import "../interfaces/IRToken.sol";

/**
 * @title RToken
 * @notice Receipt token representing a deposit in a lending pool.
 *         rToken appreciates as interest accrues: exchangeRate increases over time.
 */
contract RToken is ERC20, AccessControl, IRToken {
    bytes32 public constant MINTER_ROLE = keccak256("MINTER_ROLE");
    bytes32 public constant BURNER_ROLE = keccak256("BURNER_ROLE");

    address public immutable underlyingAsset;

    /// @notice exchangeRate = (totalUnderlying * WAD) / totalSupply
    uint256 public exchangeRate = 1e18;

    constructor(string memory name, string memory symbol, address _underlyingAsset)
        ERC20(name, symbol)
    {
        underlyingAsset = _underlyingAsset;
        _grantRole(DEFAULT_ADMIN_ROLE, msg.sender);
    }

    function mint(address to, uint256 amount) external onlyRole(MINTER_ROLE) {
        _mint(to, amount);
    }

    function burn(address from, uint256 amount) external onlyRole(BURNER_ROLE) {
        _burn(from, amount);
    }

    function balanceOfUnderlying(address account) external view returns (uint256) {
        return (balanceOf(account) * exchangeRate) / 1e18;
    }

    function setExchangeRate(uint256 newRate) external onlyRole(DEFAULT_ADMIN_ROLE) {
        exchangeRate = newRate;
    }
}
