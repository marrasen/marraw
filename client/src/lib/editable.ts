/**
 * Whether a key event landed somewhere the user is typing, and so must not be
 * read as a shortcut.
 *
 * There are two global keydown listeners in the app — the main dispatcher in
 * lib/keyboard.ts, and the theme provider's own, which has to stay separate
 * because it also runs in the pop-out viewer window, where the dispatcher does
 * not. They had a guard each, and the dispatcher's missed `<select>` and only
 * looked at the event target itself, so a keystroke aimed at a nested element
 * inside a field could still fire a shortcut. One implementation now, the
 * stricter of the two.
 */
export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  return target.closest("input, textarea, select, [contenteditable='true']") !== null;
}
