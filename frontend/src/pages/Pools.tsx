import { Link } from 'react-router-dom';
import { fmtAddress } from '@/utils/format';

/**
 * Pools list page.
 * Replace mock data with on-chain data from PoolFactory.allPools.
 */
const MOCK_POOLS = [
  { address: '0xabcd...0001', depositAsset: 'USDT', collateralAsset: 'WBTC', tvl: '$12.5M', apy: '3.2%', risk: 'LOW' },
  { address: '0xabcd...0002', depositAsset: 'USDC', collateralAsset: 'ETH', tvl: '$8.1M', apy: '2.8%', risk: 'LOW' },
  { address: '0xabcd...0003', depositAsset: 'DAI', collateralAsset: 'LINK', tvl: '$2.3M', apy: '5.1%', risk: 'MEDIUM' },
];

export default function Pools() {
  return (
    <div className="max-w-6xl mx-auto px-4 py-12">
      <h1 className="text-3xl font-bold mb-8">Lending Pools</h1>

      <div className="grid gap-4">
        {MOCK_POOLS.map((pool) => (
          <Link
            key={pool.address}
            to={`/pool/${pool.address}`}
            className="card flex items-center justify-between hover:border-gray-600 transition-colors"
          >
            <div className="flex items-center gap-6">
              <div>
                <p className="font-medium">{pool.depositAsset} / {pool.collateralAsset}</p>
                <p className="text-xs text-gray-500">{fmtAddress(pool.address)}</p>
              </div>
              <span className={`text-xs px-2 py-0.5 rounded-full ${
                pool.risk === 'LOW' ? 'bg-green-900/50 text-green-400' :
                pool.risk === 'MEDIUM' ? 'bg-yellow-900/50 text-yellow-400' :
                'bg-red-900/50 text-red-400'
              }`}>
                {pool.risk}
              </span>
            </div>
            <div className="flex items-center gap-8 text-right">
              <div>
                <p className="stat-label">TVL</p>
                <p className="font-medium">{pool.tvl}</p>
              </div>
              <div>
                <p className="stat-label">APY</p>
                <p className="font-medium text-green-400">{pool.apy}</p>
              </div>
            </div>
          </Link>
        ))}
      </div>
    </div>
  );
}
