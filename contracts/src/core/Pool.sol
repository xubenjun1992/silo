// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/access/AccessControl.sol";
import "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/utils/Pausable.sol";
import "../interfaces/IPool.sol";
import "../interfaces/IRToken.sol";
import "../interfaces/IOracle.sol";
import "../interfaces/IInterestRateModel.sol";
import "../libraries/MathLib.sol";
import "../libraries/Errors.sol";

/**
 * @title Pool
 * @notice Isolated lending pool — one per asset/risk-tier.
 *         Deposits, borrows, liquidations all contained within this contract.
 *         Risks do NOT propagate across pools.
 */
contract Pool is IPool, AccessControl, ReentrancyGuard, Pausable {
    using SafeERC20 for IERC20;
    using MathLib for uint256;

    bytes32 public constant LIQUIDATOR_ROLE = keccak256("LIQUIDATOR_ROLE");

    /*═══════════════════════════════════ POOL CONFIG ═══════════════════════════════════*/
    address public immutable depositAsset;    // asset lenders deposit & borrowers borrow
    address public immutable collateralAsset; // asset borrowers pledge
    IRToken public immutable rToken;
    IOracle public oracle;
    IInterestRateModel public rateModel;

    /// @notice Minimum collateral ratio (WAD, e.g. 1.2e18 = 120%)
    uint256 public minCollateralRatio = 1.2e18;
    /// @notice Health factor below which liquidation is allowed (WAD, e.g. 1.1e18 = 110%)
    uint256 public liquidationThreshold = 1.1e18;
    /// @notice Bonus for liquidators (WAD, e.g. 0.05e18 = 5%)
    uint256 public liquidationBonus = 0.05e18;

    /*═══════════════════════════════════ STATE ═══════════════════════════════════════*/
    uint256 public totalLiquidity;
    uint256 public totalDebt;
    uint256 public totalDebtShares;
    uint256 public lastUpdateTime;
    uint256 public borrowIndex = 1e18;      // accrual tracker, scaled per second
    uint256 public reserveBalance;

    // borrower → collateral balance
    mapping(address => uint256) public collateral;
    // borrower → debt shares (proportional to borrowIndex)
    mapping(address => uint256) public debtShares;
    // borrower → normalized debt (= shares * borrowIndex)
    // (derived inline, not stored)

    /*═══════════════════════════════════ CONSTRUCTOR ══════════════════════════════════*/
    constructor(
        address _depositAsset,
        address _collateralAsset,
        address _oracle,
        address _rateModel,
        string memory rTokenName,
        string memory rTokenSymbol
    ) {
        depositAsset = _depositAsset;
        collateralAsset = _collateralAsset;
        oracle = IOracle(_oracle);
        rateModel = IInterestRateModel(_rateModel);

        rToken = IRToken(address(new RTokenImpl(rTokenName, rTokenSymbol, _depositAsset)));
        _grantRole(DEFAULT_ADMIN_ROLE, msg.sender);
        _grantRole(LIQUIDATOR_ROLE, msg.sender);
    }

    /*═══════════════════════════════════ MODIFIERS ════════════════════════════════════*/
    modifier accrue() {
        _accrueInterest();
        _;
    }

    /*═══════════════════════════════════ DEPOSIT ══════════════════════════════════════*/
    function deposit(uint256 amount, address onBehalfOf)
        external nonReentrant accrue returns (uint256 rTokens)
    {
        require(amount > 0, "Amount=0");
        IERC20(depositAsset).safeTransferFrom(msg.sender, address(this), amount);

        uint256 exchangeRate = rToken.exchangeRate();
        rTokens = exchangeRate > 0 ? (amount * 1e18) / exchangeRate : amount;

        totalLiquidity += amount;
        rToken.mint(onBehalfOf, rTokens);
        emit Deposited(onBehalfOf, amount, rTokens);
    }

    function withdraw(uint256 rTokenAmount, address to)
        external nonReentrant accrue returns (uint256 amount)
    {
        uint256 exchangeRate = rToken.exchangeRate();
        amount = (rTokenAmount * exchangeRate) / 1e18;
        require(amount <= totalLiquidity, "Insufficient liquidity");

        rToken.burn(msg.sender, rTokenAmount);
        totalLiquidity -= amount;
        IERC20(depositAsset).safeTransfer(to, amount);
        emit Withdrawn(msg.sender, amount, rTokenAmount);
    }

    /*═══════════════════════════════════ BORROW ═══════════════════════════════════════*/
    function borrow(uint256 amount, address to) external nonReentrant accrue {
        require(amount <= totalLiquidity, "Insufficient liquidity");

        _updateDebt(msg.sender);
        uint256 newDebt = _debtOf(msg.sender) + amount;
        require(_healthFactorAfter(msg.sender, newDebt) >= 1e18, "Undercollateralized");

        uint256 newShares = (amount * 1e18) / borrowIndex;
        debtShares[msg.sender] += newShares;
        totalDebtShares += newShares;
        totalDebt += amount;
        totalLiquidity -= amount;

        IERC20(depositAsset).safeTransfer(to, amount);
        emit Borrowed(msg.sender, amount, newShares);
    }

    function repay(uint256 amount, address onBehalfOf)
        external nonReentrant accrue returns (uint256 repaid)
    {
        _updateDebt(onBehalfOf);
        uint256 owed = _debtOf(onBehalfOf);
        repaid = amount > owed ? owed : amount;
        require(repaid > 0, "Repay=0");

        uint256 sharesToBurn = (repaid * 1e18) / borrowIndex;
        if (msg.sender != onBehalfOf) {
            debtShares[onBehalfOf] -= sharesToBurn;
        } else {
            debtShares[msg.sender] -= sharesToBurn;
        }
        totalDebtShares -= sharesToBurn;
        totalDebt -= repaid;

        IERC20(depositAsset).safeTransferFrom(msg.sender, address(this), repaid);
        totalLiquidity += repaid;
        emit Repaid(onBehalfOf, repaid, sharesToBurn);
    }

    /*═══════════════════════════════════ LIQUIDATION ══════════════════════════════════*/
    function liquidate(address borrower)
        external nonReentrant accrue onlyRole(LIQUIDATOR_ROLE)
        returns (uint256 debtRepaid, uint256 collateralSeized, uint256 reward)
    {
        uint256 hf = getHealthFactor(borrower);
        require(hf < 1e18 && hf != type(uint256).max, "Not liquidatable");

        uint256 debtToRepay = _debtOf(borrower);

        // Calculate collateral to seize = debt value + liquidation bonus
        (uint256 debtPrice,) = oracle.getPrice(depositAsset);
        (uint256 colPrice,) = oracle.getPrice(collateralAsset);
        uint256 debtValue = debtToRepay * debtPrice;
        uint256 bonusValue = debtValue + (debtValue * liquidationBonus) / 1e18;
        collateralSeized = bonusValue / colPrice;
        reward = (debtValue * liquidationBonus) / (1e18 * debtPrice); // in deposit asset

        if (collateralSeized > collateral[borrower]) {
            collateralSeized = collateral[borrower];
        }

        collateral[borrower] -= collateralSeized;
        debtShares[borrower] = 0;
        totalDebtShares -= (debtToRepay * 1e18) / borrowIndex;
        totalDebt -= debtToRepay;

        IERC20(depositAsset).safeTransferFrom(msg.sender, address(this), debtRepaid);
        IERC20(collateralAsset).safeTransfer(msg.sender, collateralSeized);

        totalLiquidity += debtRepaid;
        emit Liquidated(msg.sender, borrower, debtRepaid, collateralSeized, reward);
    }

    /*═══════════════════════════════════ VIEWS ════════════════════════════════════════*/
    function getUtilizationRate() public view returns (uint256) {
        return rateModel.utilizationRate(totalLiquidity, totalDebt);
    }

    function getBorrowRate() public view returns (uint256) {
        return rateModel.getBorrowRate(getUtilizationRate());
    }

    function getSupplyRate() public view returns (uint256) {
        return rateModel.getSupplyRate(getUtilizationRate(), getBorrowRate());
    }

    function getHealthFactor(address borrower) public view returns (uint256) {
        uint256 debt = _debtOf(borrower);
        if (debt == 0) return type(uint256).max;
        (uint256 colPrice,) = oracle.getPrice(collateralAsset);
        (uint256 debtPrice,) = oracle.getPrice(depositAsset);
        uint256 colValue = (collateral[borrower] * colPrice) / 1e18;
        uint256 debtValue = (debt * debtPrice) / 1e18;
        if (debtValue == 0) return type(uint256).max;
        return (colValue * 1e18) / (debtValue * minCollateralRatio / 1e18);
    }

    /*═══════════════════════════════════ INTERNAL ═════════════════════════════════════*/
    function _accrueInterest() internal {
        if (lastUpdateTime == 0) {
            lastUpdateTime = block.timestamp;
            return;
        }
        uint256 elapsed = block.timestamp - lastUpdateTime;
        if (elapsed == 0) return;
        lastUpdateTime = block.timestamp;

        uint256 borrowRate = getBorrowRate();
        uint256 ratePerSec = borrowRate.ratePerSecond();
        uint256 accrual = (borrowIndex * ratePerSec * elapsed) / 1e18;
        borrowIndex += accrual;

        // Update rToken exchange rate to reflect interest
        uint256 supplyRate = getSupplyRate();
        uint256 supplyAccrual = (supplyRate * elapsed) / 365 days;
        uint256 newExchangeRate = rToken.exchangeRate() + (rToken.exchangeRate() * supplyAccrual) / 1e18;
        rToken.setExchangeRate(newExchangeRate); // simplified; would need perms
    }

    function _updateDebt(address borrower) internal {
        uint256 currentDebt = _debtOf(borrower);
        totalDebt = totalDebt - currentDebt + (debtShares[borrower] * borrowIndex) / 1e18;
    }

    function _debtOf(address borrower) internal view returns (uint256) {
        return (debtShares[borrower] * borrowIndex) / 1e18;
    }

    function _healthFactorAfter(address borrower, uint256 hypotheticalDebt) internal view returns (uint256) {
        if (hypotheticalDebt == 0) return type(uint256).max;
        (uint256 colPrice,) = oracle.getPrice(collateralAsset);
        (uint256 debtPrice,) = oracle.getPrice(depositAsset);
        uint256 colValue = (collateral[borrower] * colPrice) / 1e18;
        uint256 debtValue = (hypotheticalDebt * debtPrice) / 1e18;
        if (debtValue == 0) return type(uint256).max;
        return (colValue * 1e18) / (debtValue * minCollateralRatio / 1e18);
    }

    /*═══════════════════════════════════ ADMIN ════════════════════════════════════════*/
    function setOracle(address _oracle) external onlyRole(DEFAULT_ADMIN_ROLE) {
        oracle = IOracle(_oracle);
    }

    function setRateModel(address _rateModel) external onlyRole(DEFAULT_ADMIN_ROLE) {
        rateModel = IInterestRateModel(_rateModel);
    }

    function pause() external onlyRole(DEFAULT_ADMIN_ROLE) { _pause(); }
    function unpause() external onlyRole(DEFAULT_ADMIN_ROLE) { _unpause(); }
}

/// @notice Minimal RToken implementation deployed per-pool.
contract RTokenImpl is ERC20, AccessControl {
    bytes32 public constant MINTER_ROLE = keccak256("MINTER_ROLE");
    bytes32 public constant BURNER_ROLE = keccak256("BURNER_ROLE");

    address public immutable underlyingAsset;
    uint256 public exchangeRate = 1e18;

    constructor(string memory name, string memory symbol, address _underlyingAsset) ERC20(name, symbol) {
        underlyingAsset = _underlyingAsset;
        _grantRole(DEFAULT_ADMIN_ROLE, msg.sender);
        _grantRole(MINTER_ROLE, msg.sender);
        _grantRole(BURNER_ROLE, msg.sender);
    }

    function mint(address to, uint256 amount) external onlyRole(MINTER_ROLE) { _mint(to, amount); }
    function burn(address from, uint256 amount) external onlyRole(BURNER_ROLE) { _burn(from, amount); }
    function setExchangeRate(uint256 newRate) external onlyRole(DEFAULT_ADMIN_ROLE) { exchangeRate = newRate; }
}
