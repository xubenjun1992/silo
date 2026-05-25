import { formatUnits, parseUnits } from 'ethers';

export function fmtUSD(value: number, decimals: number = 2): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  }).format(value);
}

export function fmtToken(amount: bigint, decimals: number = 18, displayDecimals: number = 4): string {
  return Number(formatUnits(amount, decimals)).toLocaleString('en-US', {
    maximumFractionDigits: displayDecimals,
  });
}

export function fmtPct(value: bigint, decimals: number = 2): string {
  // value is WAD (1e18), e.g. 0.05e18 = 5%
  const pct = Number(formatUnits(value, 16)); // 1e18 → 100
  return pct.toFixed(decimals) + '%';
}

export function fmtAddress(addr: string): string {
  return addr.slice(0, 6) + '...' + addr.slice(-4);
}

export function parseToken(amount: string, decimals: number = 18): bigint {
  return parseUnits(amount, decimals);
}
