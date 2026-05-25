export default function Governance() {
  return (
    <div className="max-w-4xl mx-auto px-4 py-12">
      <h1 className="text-3xl font-bold mb-2">Governance</h1>
      <p className="text-gray-500 mb-8">Propose and vote on protocol parameter changes.</p>

      {/* Placeholder */}
      <div className="grid gap-4">
        {[
          { id: 1, title: 'Adjust min collateral ratio for MEDIUM tier to 140%', status: 'Active', for: '82%' },
          { id: 2, title: 'Add new pool: USDT / MATIC (LOW risk)', status: 'Passed', for: '95%' },
          { id: 3, title: 'Update liquidation bonus to 8%', status: 'Defeated', for: '32%' },
        ].map((p) => (
          <div key={p.id} className="card flex items-center justify-between">
            <div>
              <p className="text-sm font-medium">#{p.id} — {p.title}</p>
              <span className={`text-xs ${
                p.status === 'Active' ? 'text-blue-400' :
                p.status === 'Passed' ? 'text-green-400' : 'text-red-400'
              }`}>
                {p.status} • {p.for} in favor
              </span>
            </div>
            <button className="btn-outline text-sm" disabled={p.status !== 'Active'}>Vote</button>
          </div>
        ))}
      </div>
    </div>
  );
}
