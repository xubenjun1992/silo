import { useWallet } from '@/hooks/useWallet';
import { fmtAddress } from '@/utils/format';

export default function Dashboard() {
  const { address } = useWallet();

  if (!address) {
    return (
      <div className="max-w-2xl mx-auto px-4 py-20 text-center">
        <h1 className="text-2xl font-bold mb-4">Dashboard</h1>
        <p className="text-gray-400 mb-6">Connect your wallet to view your positions.</p>
      </div>
    );
  }

  return (
    <div className="max-w-6xl mx-auto px-4 py-12">
      <h1 className="text-3xl font-bold mb-2">Dashboard</h1>
      <p className="text-gray-500 mb-8">{fmtAddress(address)}</p>

      {/* Your positions */}
      <section className="mb-12">
        <h2 className="text-xl font-semibold mb-4">Your Positions</h2>
        <div className="card text-center text-gray-500 py-12">
          <p>No active positions.</p>
          <p className="text-sm mt-1">Deposit or borrow to get started.</p>
        </div>
      </section>

      {/* Recent activity */}
      <section>
        <h2 className="text-xl font-semibold mb-4">Recent Activity</h2>
        <div className="card text-center text-gray-500 py-12">
          <p>No recent activity.</p>
        </div>
      </section>
    </div>
  );
}
