import { ethers } from "hardhat";

async function main() {
  const [deployer] = await ethers.getSigners();
  console.log("Deploying with:", deployer.address);

  // 1. Protocol Config
  const ProtocolConfig = await ethers.getContractFactory("ProtocolConfig");
  const config = await ProtocolConfig.deploy();
  await config.waitForDeployment();
  console.log("ProtocolConfig:", await config.getAddress());

  // 2. Interest Rate Model (USDT low-risk pool)
  const InterestRateModel = await ethers.getContractFactory("InterestRateModel");
  const rateModel = await InterestRateModel.deploy(
    ethers.parseEther("0.02"),  // 2% base
    ethers.parseEther("0.80"),  // 80% kink
    ethers.parseEther("0.10"),  // 10% slope1
    ethers.parseEther("1.00"),  // 100% slope2
    ethers.parseEther("0.15")   // 15% reserve factor
  );
  await rateModel.waitForDeployment();
  console.log("InterestRateModel:", await rateModel.getAddress());

  // 3. Oracle (Chainlink-based, with mock for dev)
  const ChainlinkOracle = await ethers.getContractFactory("ChainlinkOracle");
  const oracle = await ChainlinkOracle.deploy(deployer.address);
  await oracle.waitForDeployment();
  console.log("ChainlinkOracle:", await oracle.getAddress());

  // 4. Pool Factory
  const PoolFactory = await ethers.getContractFactory("PoolFactory");
  const factory = await PoolFactory.deploy(await config.getAddress());
  await factory.waitForDeployment();
  console.log("PoolFactory:", await factory.getAddress());

  // 5. Create first pool (USDT deposit, WBTC collateral)
  // Requires mock token addresses — replace with real addresses on mainnet
  console.log("\nReady to create pools via factory.createPool()");
  console.log("Example: factory.createPool(USDT, WBTC, oracle, rateModel, 'rUSDT', 'rUSDT')");
}

main().catch((err) => { console.error(err); process.exitCode = 1; });
