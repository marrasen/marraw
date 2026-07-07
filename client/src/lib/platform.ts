// Platform-aware shortcut labels: the palette shortcut is Cmd+K on macOS
// and Ctrl+K everywhere else (the handler accepts either modifier).
export const isMac = /Mac|iPhone|iPad/.test(navigator.userAgent);
export const modK = isMac ? '⌘K' : 'Ctrl+K';
