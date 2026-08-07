// Platform-aware shortcut labels: the palette shortcut is Cmd+K on macOS
// and Ctrl+K everywhere else (the handler accepts either modifier).
export const isMac = /Mac|iPhone|iPad/.test(navigator.userAgent);
// The modifier on its own, for labelling other shortcuts in tooltips.
export const mod = isMac ? '⌘' : 'Ctrl';
export const modK = `${mod}${isMac ? '' : '+'}K`;
