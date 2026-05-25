import { Link } from 'react-router-dom';

export default function Home() {
  return (
    <div>
      {/* Hero */}
      <section className="max-w-4xl mx-auto px-4 pt-20 pb-16 text-center">
        <h1 className="text-5xl font-bold tracking-tight mb-4">
          Risk-<span className="text-primary">Isolated</span> Lending
        </h1>
        <p className="text-xl text-gray-400 mb-2 max-w-2xl mx-auto">
          Every pool is an independent island — zero risk contagion.
        </p>
        <p className="text-sm text-gray-500 mb-8">
          Isolated pools • Dynamic interest rates • Over-collateralized • Permissionless liquidation
        </p>
        <Link to="/pools" className="btn-primary inline-block text-lg px-8 py-3">
          Explore Pools
        </Link>
      </section>

      {/* Features */}
      <section className="max-w-6xl mx-auto px-4 py-16 grid md:grid-cols-3 gap-6">
        {[
          {
            title: 'Pool Isolation',
            desc: 'Each asset / risk tier has its own independent pool. Bad debt in one pool never affects another.',
            icon: '🔒',
          },
          {
            title: 'Dynamic Interest',
            desc: 'Utilization-driven interest rates balance supply and demand. Rate spikes disincentivize hoarding.',
            icon: '📈',
          },
          {
            title: 'Permissionless Liquidation',
            desc: 'Anyone can liquidate under-collateralized positions. Liquidators earn a bonus for maintaining solvency.',
            icon: '⚡',
          },
        ].map((f) => (
          <div key={f.title} className="card">
            <div className="text-2xl mb-3">{f.icon}</div>
            <h3 className="text-lg font-semibold mb-2">{f.title}</h3>
            <p className="text-sm text-gray-400">{f.desc}</p>
          </div>
        ))}
      </section>

      {/* Risk tiers */}
      <section className="max-w-6xl mx-auto px-4 py-16">
        <h2 className="text-2xl font-bold mb-6 text-center">Risk Tiers</h2>
        <div className="grid md:grid-cols-3 gap-4">
          {[
            { tier: 'Low Risk', assets: 'Stablecoins, Blue-chip tokens', minCR: '120%', color: 'risk-low' },
            { tier: 'Medium Risk', assets: 'Native assets, liquid tokens', minCR: '150%', color: 'risk-medium' },
            { tier: 'High Risk', assets: 'Long-tail, NFT, exotic assets', minCR: '200%', color: 'risk-high' },
          ].map((t) => (
            <div key={t.tier} className="card border-l-2" style={{ borderLeftColor: `var(--tw-${t.color})` }}>
              <span className={`text-xs uppercase tracking-wider text-${t.color}`}>{t.tier}</span>
              <p className="text-sm text-gray-400 mt-1">{t.assets}</p>
              <p className="text-sm mt-2">Min collateral: <span className="text-white font-medium">{t.minCR}</span></p>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}
