import { expect } from "chai";
import { ethers } from "hardhat";
import { loadFixture } from "@nomicfoundation/hardhat-toolbox/network-helpers";

describe("Pool - Isolated Lending", function () {
  async function deployFixture() {
    const [admin, lender, borrower, liquidator] = await ethers.getSigners();

    // Mock ERC20 tokens for deposit & collateral
    const MockToken = await ethers.getContractFactory("ERC20Mock");
    const depositToken = await MockToken.deploy("USDT", "USDT", 18);
    const collateralToken = await MockToken.deploy("WBTC", "WBTC", 8);

    // Interest rate model
    const InterestModel = await ethers.getContractFactory("InterestRateModel");
    const rateModel = await InterestModel.deploy(
      ethers.parseEther("0.02"),  // base rate 2%
      ethers.parseEther("0.8"),   // kink 80%
      ethers.parseEther("0.1"),   // slope1 10%
      ethers.parseEther("1.0"),   // slope2 100%
      ethers.parseEther("0.15")   // reserve factor 15%
    );

    // Mock oracle
    const MockOracle = await ethers.getContractFactory("MockOracle");
    const oracle = await MockOracle.deploy();

    // Pool
    const Pool = await ethers.getContractFactory("Pool");
    const pool = await Pool.deploy(
      await depositToken.getAddress(),
      await collateralToken.getAddress(),
      await oracle.getAddress(),
      await rateModel.getAddress(),
      "rUSDT",
      "rUSDT"
    );

    return { admin, lender, borrower, liquidator, depositToken, collateralToken, pool, oracle };
  }

  it("should allow deposit and mint rTokens", async function () {
    const { pool, depositToken, lender } = await loadFixture(deployFixture);
    const amount = ethers.parseEther("1000");
    await depositToken.mint(lender.address, amount);
    await depositToken.connect(lender).approve(await pool.getAddress(), amount);
    await pool.connect(lender).deposit(amount, lender.address);
    const rTokenAddr = await pool.rToken();
    const rToken = await ethers.getContractAt("ERC20", rTokenAddr);
    expect(await rToken.balanceOf(lender.address)).to.equal(amount);
  });

  it("should allow borrow with sufficient collateral", async function () {
    // Implement: deposit collateral, borrow, verify health factor
  });

  it("should allow liquidation when health factor < 1", async function () {
    // Implement: deposit, borrow, price drop, liquidate
  });

  it("should prevent borrowing beyond collateral ratio", async function () {
    // Implement: attempt over-borrow, expect revert
  });
});
