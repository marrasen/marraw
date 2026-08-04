/**
 * "3 minutes ago" — coarse on purpose. Used for the last time an approved
 * computer connected and the last time a shared album was opened; neither is
 * worth a second-by-second reading, and both are refreshed by a subscription
 * rather than a timer, so a precise value would go stale on screen anyway.
 */
/**
 * When a share link stops working. A date alone is useless for the short
 * lifetimes ("expires 4 Aug" on a link that dies in four hours), so anything
 * inside two days carries the clock time as well.
 */
export function expiryLabel(ms: number): string {
  if (!ms) return 'no expiry';
  const d = new Date(ms);
  if (ms - Date.now() < 48 * 60 * 60 * 1000) {
    return `expires ${d.toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })}`;
  }
  return `expires ${d.toLocaleDateString()}`;
}

export function relativeTime(ms: number): string {
  if (!ms) return 'never';
  const secs = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (secs < 90) return 'just now';
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.round(hours / 24)} days ago`;
}
