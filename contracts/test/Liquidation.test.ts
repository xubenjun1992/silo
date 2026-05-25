import { expect } from "chai";
import { ethers } from "hardhat";

describe("Liquidator", function () {
  it("should liquidate an unhealthy position", async function () {
    // Implement: set up pool, create position, drop price, liquidate
  });

  it("should batch liquidate across pools without cross-contamination", async function () {
    // Core isolation test: one pool liquidation failure must not affect another
  });
});
