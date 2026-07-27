// Runs inside the marraw renderer (see electron/main.cjs MARRAW_UITEST).
// Acceptance test for cross-stack undo ordering in Library mode: culling
// actions (flag/rating) and edit actions (auto, sliders) live on separate
// undo stacks, and Ctrl+Z must undo whichever action happened most recently
// — not statically prefer the cull stack. Redo replays in forward order.
const R = {};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms = 15000, what = 'condition') => {
  const t = Date.now();
  for (;;) {
    let v;
    try { v = fn(); } catch { v = null; }
    if (v) return v;
    if (Date.now() - t > ms) throw new Error(`timeout: ${what}`);
    await sleep(100);
  }
};
const key = (k, mods = {}) =>
  window.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, ...mods }));

try {
  const mw = await until(() => window.__marraw, 15000, '__marraw hooks');
  const ui = () => mw.useUIStore.getState();
  const es = () => mw.useEditSession.getState();
  const ch = () => mw.useCullHistory.getState();

  await until(() => ui().visibleIds.length > 0, 30000, 'photos loaded');

  // The scenario lives in Library mode with the edit aside open — that is
  // what mounts EditPanel and loads the edit session for the focused photo,
  // making Ctrl+U reachable without leaving Library.
  ui().setMode('library');
  mw.useUIStore.setState({ showEditPanel: true });
  const target = ui().visibleIds[0];
  ui().focus(target);
  await until(() => es().photoId === target && es().draft != null, 20000, 'edit session on focused photo');
  R.libraryMode = ui().mode === 'library' ? true : `mode=${ui().mode}`;

  // Clean baseline even after an aborted run: reset edits (round-trips and
  // reloads the seeded params), clear any leftover flag ('u' unflag is a
  // true no-op when already unflagged — nothing enters the cull history).
  key('0', { ctrlKey: true });
  await sleep(900);
  key('u');
  await sleep(300);
  const flagOf = () => (ui().overrides.get(target) || {}).flag ?? 'none';
  const draftJSON = () => JSON.stringify(es().draft);
  const baseJSON = draftJSON();

  // --- the reported repro: exclude, then auto, then Ctrl+Z ------------------
  key('x');
  await until(() => flagOf() === 'exclude', 5000, 'photo excluded');
  const chLen = ch().stack.length;
  R.excludeStampedSeq = ch().stack[chLen - 1].seq > 0 ? true : `seq=${ch().stack[chLen - 1].seq}`;

  key('u', { ctrlKey: true });
  await until(() => draftJSON() !== baseJSON, 20000, 'auto tone landed');
  const autoJSON = draftJSON();

  key('z', { ctrlKey: true });
  await until(() => draftJSON() === baseJSON, 5000, 'first undo reverts the auto').catch(() => {});
  R.firstUndoRevertsAuto = draftJSON() === baseJSON ? true : 'draft still carries the auto';
  R.firstUndoKeepsExclude = flagOf() === 'exclude' ? true : `flag=${flagOf()}`;

  key('z', { ctrlKey: true });
  await until(() => flagOf() !== 'exclude', 5000, 'second undo reverts the exclude').catch(() => {});
  R.secondUndoRevertsExclude = flagOf() === 'none' ? true : `flag=${flagOf()}`;
  R.secondUndoKeepsDraft = draftJSON() === baseJSON ? true : 'draft moved on a cull undo';

  // --- redo replays forward: exclude first (older), then the auto -----------
  key('z', { ctrlKey: true, shiftKey: true });
  await until(() => flagOf() === 'exclude', 5000, 'first redo re-excludes').catch(() => {});
  R.firstRedoReappliesExclude = flagOf() === 'exclude' ? true : `flag=${flagOf()}`;
  R.firstRedoKeepsDraft = draftJSON() === baseJSON ? true : 'draft moved on a cull redo';

  key('y', { ctrlKey: true });
  await until(() => draftJSON() === autoJSON, 5000, 'second redo reapplies the auto').catch(() => {});
  R.secondRedoReappliesAuto = draftJSON() === autoJSON ? true : 'draft did not return to the auto';

  // --- reverse interleave: auto first, then exclude, Ctrl+Z pops the exclude
  key('z', { ctrlKey: true }); // back to baseline (undo auto…)
  await sleep(400);
  key('z', { ctrlKey: true }); // …and exclude
  await until(() => flagOf() === 'none' && draftJSON() === baseJSON, 5000, 'rewound to baseline');

  key('u', { ctrlKey: true });
  await until(() => draftJSON() !== baseJSON, 20000, 'auto tone landed (reverse order)');
  const auto2JSON = draftJSON();
  key('x');
  await until(() => flagOf() === 'exclude', 5000, 'photo excluded (reverse order)');

  key('z', { ctrlKey: true });
  await until(() => flagOf() !== 'exclude', 5000, 'undo pops the exclude first').catch(() => {});
  R.reverseUndoPopsExclude = flagOf() === 'none' ? true : `flag=${flagOf()}`;
  R.reverseUndoKeepsAuto = draftJSON() === auto2JSON ? true : 'draft moved on a cull undo';

  key('z', { ctrlKey: true });
  await until(() => draftJSON() === baseJSON, 5000, 'second undo reverts the auto').catch(() => {});
  R.reverseSecondUndoRevertsAuto = draftJSON() === baseJSON ? true : 'draft still carries the auto';

  // Leave the fixture as found: baseline draft, no flag.
  return R;
} catch (err) {
  R.fatal = String(err && err.stack ? err.stack : err);
  return R;
}
