/**
 * Deployed contract addresses.
 * Fill these after deployment. For development, use local hardhat addresses.
 */

export const ADDRESSES: Record<string, Record<string, string>> = {
  // Chain ID → contract → address
  '31337': {
    // hardhat local
    protocolConfig: '',
    poolFactory: '',
    oracle: '',
    governance: '',
  },
  '11155111': {
    // sepolia testnet
    protocolConfig: '',
    poolFactory: '',
    oracle: '',
    governance: '',
  },
};

export function getAddress(chainId: number, contract: string): string {
  return ADDRESSES[String(chainId)]?.[contract] ?? '';
}
