import { create } from 'zustand';

// Connection rejection state, fed by the ApiClient's onConnectionRejected
// callback in main.tsx. Kept outside the client because aprot stops
// auto-reconnecting after an auth rejection — the banner needs to show why
// and offer a manual retry.
interface ConnectionRejectedState {
  rejectedMessage: string | null;
  setRejected: (message: string | null) => void;
}

export const useConnectionRejected = create<ConnectionRejectedState>((set) => ({
  rejectedMessage: null,
  setRejected: (message) => set({ rejectedMessage: message }),
}));
