import { Routes, Route } from 'react-router-dom';
import Layout from '@/components/Layout/Layout';
import Home from '@/pages/Home';
import Pools from '@/pages/Pools';
import PoolDetail from '@/pages/PoolDetail';
import Dashboard from '@/pages/Dashboard';
import Governance from '@/pages/Governance';

export default function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/pools" element={<Pools />} />
        <Route path="/pool/:poolAddress" element={<PoolDetail />} />
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/governance" element={<Governance />} />
      </Routes>
    </Layout>
  );
}
