import { expect } from "chai";
import { ethers } from "hardhat";

describe("InterestRateModel", function () {
  it("should return base rate at 0% utilization", async function () {
    const factory = await ethers.getContractFactory("InterestRateModel");
    const model = await factory.deploy(
      ethers.parseEther("0.02"),
      ethers.parseEther("0.8"),
      ethers.parseEther("0.1"),
      ethers.parseEther("1.0"),
      ethers.parseEther("0.15")
    );
    const rate = await model.getBorrowRate(0);
    expect(rate).to.equal(ethers.parseEther("0.02"));
  });

  it("should spike rate above kink (80%)", async function () {
    const factory = await ethers.getContractFactory("InterestRateModel");
    const model = await factory.deploy(
      ethers.parseEther("0.02"),
      ethers.parseEther("0.8"),
      ethers.parseEther("0.1"),
      ethers.parseEther("1.0"),
      ethers.parseEther("0.15")
    );
    const rateAt100 = await model.getBorrowRate(ethers.parseEther("1.0"));
    // base(2%) + kink*slope1(8%) + (1.0-0.8)*slope2(20%) = 30%
    expect(rateAt100).to.equal(ethers.parseEther("0.30"));
  });
});
