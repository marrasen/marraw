// A one-value store for "the daemon refused this link", so the page can say
// so instead of spinning. The app has stores/connectionStore for this; the
// share page carries neither zustand nor the rest of that module's concerns,
// and one boolean does not need them.

let rejected: string | null = null;
const listeners = new Set<() => void>();

export const connection = {
  subscribe(fn: () => void) {
    listeners.add(fn);
    return () => {
      listeners.delete(fn);
    };
  },
  get: () => rejected,
  reject(message: string) {
    rejected = message;
    for (const fn of listeners) fn();
  },
};
