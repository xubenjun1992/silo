import { create } from 'zustand';
import { BrowserProvider, JsonRpcSigner } from 'ethers';

declare global {
  interface Window {
    ethereum?: any;
  }
}

interface WalletState {
  address: string | null;
  chainId: number | null;
  provider: BrowserProvider | null;
  signer: JsonRpcSigner | null;
  isConnecting: boolean;
  connect: () => Promise<void>;
  disconnect: () => void;
}

export const useWallet = create<WalletState>((set) => ({
  address: null,
  chainId: null,
  provider: null,
  signer: null,
  isConnecting: false,

  connect: async () => {
    if (!window.ethereum) throw new Error('Please install MetaMask');
    set({ isConnecting: true });
    try {
      const provider = new BrowserProvider(window.ethereum);
      const accounts = await provider.send('eth_requestAccounts', []);
      const network = await provider.getNetwork();
      const signer = await provider.getSigner();
      set({
        address: accounts[0],
        chainId: Number(network.chainId),
        provider,
        signer,
        isConnecting: false,
      });
    } catch (e) {
      set({ isConnecting: false });
      throw e;
    }
  },

  disconnect: () => {
    set({ address: null, chainId: null, provider: null, signer: null });
  },
}));
