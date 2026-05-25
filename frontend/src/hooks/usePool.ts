import { useState, useCallback } from 'react';
import { Contract, formatUnits, parseUnits } from 'ethers';
import { useWallet } from './useWallet';

export interface PoolStats {
  totalLiquidity: string;
  totalDebt: string;
  utilizationRate: string;
  borrowRate: string;
  supplyRate: string;
  exchangeRate: string;
}

/**
 * Hook for pool interactions: deposit, borrow, repay, withdraw.
 * All pool state updates are handled via callback refetch.
 */
export function usePool(poolContract: Contract | null) {
  const { signer, address } = useWallet();
  const [isLoading, setIsLoading] = useState(false);

  const deposit = useCallback(async (amount: string, decimals: number) => {
    if (!poolContract || !signer) throw new Error('Not connected');
    setIsLoading(true);
    try {
      const tx = await poolContract.deposit(parseUnits(amount, decimals), address);
      await tx.wait();
    } finally {
      setIsLoading(false);
    }
  }, [poolContract, signer, address]);

  const withdraw = useCallback(async (rTokenAmount: string) => {
    if (!poolContract || !signer) throw new Error('Not connected');
    setIsLoading(true);
    try {
      const tx = await poolContract.withdraw(parseUnits(rTokenAmount, 18), address);
      await tx.wait();
    } finally {
      setIsLoading(false);
    }
  }, [poolContract, signer, address]);

  const borrow = useCallback(async (amount: string, decimals: number) => {
    if (!poolContract || !signer) throw new Error('Not connected');
    setIsLoading(true);
    try {
      const tx = await poolContract.borrow(parseUnits(amount, decimals), address);
      await tx.wait();
    } finally {
      setIsLoading(false);
    }
  }, [poolContract, signer, address]);

  const repay = useCallback(async (amount: string, decimals: number) => {
    if (!poolContract || !signer) throw new Error('Not connected');
    setIsLoading(true);
    try {
      const tx = await poolContract.repay(parseUnits(amount, decimals), address);
      await tx.wait();
    } finally {
      setIsLoading(false);
    }
  }, [poolContract, signer, address]);

  const fetchStats = useCallback(async (): Promise<PoolStats> => {
    if (!poolContract) throw new Error('No pool contract');
    const [liquidity, debt, util, borrowRate, supplyRate] = await Promise.all([
      poolContract.totalLiquidity(),
      poolContract.totalDebt(),
      poolContract.getUtilizationRate(),
      poolContract.getBorrowRate(),
      poolContract.getSupplyRate(),
    ]);
    return {
      totalLiquidity: formatUnits(liquidity, 18),
      totalDebt: formatUnits(debt, 18),
      utilizationRate: (Number(formatUnits(util, 16))).toFixed(2) + '%',
      borrowRate: (Number(formatUnits(borrowRate, 16))).toFixed(2) + '%',
      supplyRate: (Number(formatUnits(supplyRate, 16))).toFixed(2) + '%',
      exchangeRate: '1.00',
    };
  }, [poolContract]);

  return { deposit, withdraw, borrow, repay, fetchStats, isLoading };
}
