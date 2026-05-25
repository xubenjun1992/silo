import { useState } from 'react';
import { useParams } from 'react-router-dom';

type Tab = 'deposit' | 'withdraw' | 'borrow' | 'repay';

export default function PoolDetail() {
  const { poolAddress } = useParams<{ poolAddress: string }>();
  const [tab, setTab] = useState<Tab>('deposit');
  const [amount, setAmount] = useState('');

  const tabs: { key: Tab; label: string }[] = [
    { key: 'deposit', label: 'Deposit' },
    { key: 'withdraw', label: 'Withdraw' },
    { key: 'borrow', label: 'Borrow' },
    { key: 'repay', label: 'Repay' },
  ];

  return (
    <div className="max-w-6xl mx-auto px-4 py-12">
      {/* Pool header */}
      <div className="mb-8">
        <p className="text-sm text-gray-500 mb-1">Pool</p>
        <h1 className="text-2xl font-bold">USDT / WBTC</h1>
        <p className="text-xs text-gray-600 font-mono mt-1">{poolAddress}</p>
      </div>

      <div className="grid lg:grid-cols-3 gap-6">
        {/* Action panel */}
        <div className="lg:col-span-2 card">
          {/* Tab bar */}
          <div className="flex gap-1 mb-6 bg-gray-900 rounded-lg p-1">
            {tabs.map(({ key, label }) => (
              <button
                key={key}
                onClick={() => setTab(key)}
                className={`flex-1 py-2 text-sm font-medium rounded-md transition-colors ${
                  tab === key ? 'bg-surface text-white' : 'text-gray-400 hover:text-white'
                }`}
              >
                {label}
              </button>
            ))}
          </div>

          {/* Action form */}
          <div>
            <label className="label">Amount</label>
            <input
              type="number"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              placeholder="0.00"
              className="input mb-4"
            />
            <button className="btn-primary w-full py-3" disabled={!amount}>
              {tab === 'deposit' && 'Deposit'}
              {tab === 'withdraw' && 'Withdraw'}
              {tab === 'borrow' && 'Borrow'}
              {tab === 'repay' && 'Repay'}
            </button>
          </div>
        </div>

        {/* Stats panel */}
        <div className="card space-y-4">
          <h3 className="text-lg font-semibold mb-4">Pool Stats</h3>
          <div className="flex justify-between">
            <span className="stat-label">Total Liquidity</span>
            <span className="font-medium">$12,500,000</span>
          </div>
          <div className="flex justify-between">
            <span className="stat-label">Total Debt</span>
            <span className="font-medium">$8,200,000</span>
          </div>
          <div className="flex justify-between">
            <span className="stat-label">Utilization</span>
            <span className="font-medium">65.6%</span>
          </div>
          <div className="flex justify-between">
            <span className="stat-label">Borrow APY</span>
            <span className="font-medium text-yellow-400">4.8%</span>
          </div>
          <div className="flex justify-between">
            <span className="stat-label">Supply APY</span>
            <span className="font-medium text-green-400">3.2%</span>
          </div>
          <div className="flex justify-between">
            <span className="stat-label">Min Collateral Ratio</span>
            <span className="font-medium">120%</span>
          </div>
        </div>
      </div>
    </div>
  );
}
