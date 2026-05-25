import { BrowserProvider, JsonRpcSigner, Contract, Interface } from 'ethers';

let provider: BrowserProvider | null = null;
let signer: JsonRpcSigner | null = null;

export async function connectWallet(): Promise<string> {
  if (!window.ethereum) throw new Error('MetaMask not installed');
  provider = new BrowserProvider(window.ethereum);
  await provider.send('eth_requestAccounts', []);
  signer = await provider.getSigner();
  return signer.address;
}

export function getProvider(): BrowserProvider {
  if (!provider) throw new Error('Wallet not connected');
  return provider;
}

export function getSigner(): JsonRpcSigner {
  if (!signer) throw new Error('Wallet not connected');
  return signer;
}

export function getContract(address: string, abi: Interface): Contract {
  return new Contract(address, abi, getSigner());
}
