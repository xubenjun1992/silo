import { Link, useLocation } from 'react-router-dom';
import { useWallet } from '@/hooks/useWallet';
import { fmtAddress } from '@/utils/format';

const NAV = [
  { path: '/', label: 'Home' },
  { path: '/pools', label: 'Pools' },
  { path: '/dashboard', label: 'Dashboard' },
  { path: '/governance', label: 'Governance' },
];

export default function Layout({ children }: { children: React.ReactNode }) {
  const location = useLocation();
  const { address, connect, disconnect, isConnecting } = useWallet();

  return (
    <div className="min-h-screen flex flex-col">
      {/* Header */}
      <header className="border-b border-gray-800 bg-gray-950/80 backdrop-blur sticky top-0 z-50">
        <div className="max-w-7xl mx-auto px-4 h-16 flex items-center justify-between">
          <div className="flex items-center gap-8">
            <Link to="/" className="text-xl font-bold text-primary tracking-tight">
              SILO
            </Link>
            <nav className="hidden md:flex gap-1">
              {NAV.map(({ path, label }) => (
                <Link
                  key={path}
                  to={path}
                  className={`px-3 py-2 rounded-lg text-sm transition-colors ${
                    location.pathname === path
                      ? 'bg-surface text-white'
                      : 'text-gray-400 hover:text-white hover:bg-surface/50'
                  }`}
                >
                  {label}
                </Link>
              ))}
            </nav>
          </div>
          <div>
            {address ? (
              <div className="flex items-center gap-3">
                <span className="text-sm text-gray-400 bg-surface px-3 py-1.5 rounded-lg">
                  {fmtAddress(address)}
                </span>
                <button onClick={disconnect} className="btn-outline text-sm py-1.5">
                  Disconnect
                </button>
              </div>
            ) : (
              <button onClick={connect} disabled={isConnecting} className="btn-primary text-sm">
                {isConnecting ? 'Connecting...' : 'Connect Wallet'}
              </button>
            )}
          </div>
        </div>
      </header>

      {/* Main */}
      <main className="flex-1">
        {children}
      </main>

      {/* Footer */}
      <footer className="border-t border-gray-800 py-8 text-center text-sm text-gray-500">
        Silo Protocol — Risk-Isolated Lending. Each pool is an island.
      </footer>
    </div>
  );
}
