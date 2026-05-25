import { useMemo } from 'react';
import { Contract, Interface } from 'ethers';
import { useWallet } from './useWallet';

/**
 * Hook to get a typed contract instance.
 * Returns null if wallet is not connected.
 */
export function useContract(address: string, abi: Interface): Contract | null {
  const { signer } = useWallet();
  return useMemo(() => {
    if (!signer || !address) return null;
    return new Contract(address, abi, signer);
  }, [signer, address, abi]);
}
