// Runs inside the marraw renderer (see electron/main.cjs MARRAW_UITEST).
// Acceptance test for library-rail folder sort & time grouping: the sort/group
// dropdown reorders and buckets the shoots of a managed parent, opening a
// shoot dates its bucket once the metadata pass lands, group headers collapse,
// and "Collapse previous years" closes every bucket from earlier years.
//
// Driven by scripts/railgroups-verify.mjs, which seeds the parent (shoots
// Alpha/Bravo/Charlie + a loose RAW) and resets sort/group before launch.
const R = {};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms, what) => {
  const t = Date.now();
  for (;;) {
    let v;
    try {
      v = fn();
    } catch {
      v = null;
    }
    if (v) return v;
    if (Date.now() - t > ms) throw new Error(`timeout: ${what}`);
    await sleep(200);
  }
};

// Everything is scoped to the fixture parent's block: the sort/group settings
// are global, so the user's real library folders group and collapse too, and
// an unscoped query would grab their headers.
const block = () => {
  const h = [...document.querySelectorAll('[data-testid="rail-parent"]')].find((el) =>
    el.textContent.includes('marraw-railgroups'),
  );
  return h ? h.parentElement : null;
};
const shootRows = () => [...(block()?.querySelectorAll('[data-testid="rail-shoot"]') ?? [])];
const FIXTURE = ['Alpha', 'Bravo', 'Charlie'];
const childNames = () =>
  shootRows()
    .map((el) => el.dataset.name)
    .filter((n) => FIXTURE.includes(n));
const shootByName = (n) => shootRows().find((el) => el.dataset.name === n);
const groupHeaders = () => [
  ...(block()?.querySelectorAll('[data-testid="rail-timegroup"]') ?? []),
];
const groupById = (id) => groupHeaders().find((el) => el.dataset.group === id);
const yearGroup = () => groupHeaders().find((el) => /^\d{4}$/.test(el.dataset.group));

const menuItems = () =>
  [...document.querySelectorAll('[role="menuitem"], [role="menuitemradio"]')];
const menuItem = (text) => menuItems().find((el) => el.innerText.includes(text));
const openSortMenu = async () => {
  const trigger = document.querySelector('[data-testid="rail-sort-menu"]');
  if (!trigger) throw new Error('no rail-sort-menu trigger');
  trigger.click(); // base-ui menu triggers open on click (ui-verify's sortPick)
  try {
    return await until(() => (menuItems().length ? menuItems() : null), 5000, 'sort menu');
  } catch {
    const roles = [...document.querySelectorAll('[role]')]
      .map((el) => el.getAttribute('role'))
      .filter((r) => r.includes('menu'))
      .join('|');
    throw new Error(
      `sort menu did not open: tag=${trigger.tagName} expanded=${trigger.getAttribute('aria-expanded')} ` +
        `haspopup=${trigger.getAttribute('aria-haspopup')} disabled=${trigger.hasAttribute('disabled')} ` +
        `popups=${document.querySelectorAll('[data-slot="dropdown-menu-content"]').length} roles=${roles}`,
    );
  }
};
const pickMenuItem = async (text) => {
  await openSortMenu();
  const item = await until(() => menuItem(text), 5000, `menu item ${text}`);
  item.click();
  await sleep(300);
  // base-ui radio items keep the menu open (closeOnClick defaults to false);
  // dismiss it so it never occludes the rows the assertions look at.
  if (menuItems().length) {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await until(() => (menuItems().length === 0 ? true : null), 5000, 'menu closed');
  }
  await sleep(200);
};

try {
  await until(block, 30000, 'library-folder header');
  await until(() => (childNames().length === 3 ? true : null), 30000, 'discovered children');

  // Default order is the server's: name ascending.
  R.defaultNameAsc = childNames().join(',') === 'Alpha,Bravo,Charlie' || childNames().join(',');

  // Sort flip through the real dropdown (also proves the optimistic setting).
  await pickMenuItem('Name · Z to A');
  R.nameDescFlips = childNames().join(',') === 'Charlie,Bravo,Alpha' || childNames().join(',');

  // Group by year. Nothing is scanned yet, so every shoot sits in "No date".
  await pickMenuItem('Year');
  const noDate = await until(() => groupById('no-date'), 10000, 'no-date group');
  R.noDateGroupShown = noDate.textContent.toLowerCase().includes('no date');
  R.selfRowAboveGroups = (() => {
    const self = shootRows().find((el) => el.dataset.self === '1');
    if (!self) return 'no self row';
    const firstHeader = groupHeaders()[0];
    return firstHeader &&
      self.compareDocumentPosition(firstHeader) & Node.DOCUMENT_POSITION_FOLLOWING
      ? true
      : 'self row not above the group headers';
  })();

  // Opening a shoot scans it; once the metadata pass lands, the daemon must
  // re-list the parent on its own and Alpha moves into a dated year bucket.
  shootByName('Alpha').click();
  const dated = await until(yearGroup, 90000, 'dated year group after metadata pass');
  R.scanDatesTheShoot = true;
  R.datedGroupHoldsAlpha = [
    ...dated.parentElement.querySelectorAll('[data-testid="rail-shoot"]'),
  ].some((el) => el.dataset.name === 'Alpha');

  // Collapse: clicking the year header hides its rows (and the state is the
  // persisted railGroups map, so it would survive a restart).
  const yid = dated.dataset.group;
  groupById(yid).click();
  await until(
    () => (groupById(yid) && !groupById(yid).dataset.open ? true : null),
    5000,
    'year group closes',
  );
  R.groupCollapses = ![
    ...groupById(yid).parentElement.querySelectorAll('[data-testid="rail-shoot"]'),
  ].length;
  groupById(yid).click();
  await until(() => (groupById(yid)?.dataset.open ? true : null), 5000, 'year group reopens');
  R.groupReopens = [
    ...groupById(yid).parentElement.querySelectorAll('[data-testid="rail-shoot"]'),
  ].some((el) => el.dataset.name === 'Alpha');

  // "Collapse previous years": the fixture's photos are from the current year,
  // so pretend it is next year. Zero-arg Date is how the collapse action asks
  // "what year is it"; epoch-constructed dates (group ids, labels) stay real.
  const RealDate = Date;
  // eslint-disable-next-line no-global-assign
  Date = class extends RealDate {
    constructor(...args) {
      if (args.length === 0) super(RealDate.now() + 366 * 24 * 3600 * 1000);
      else super(...args);
    }
  };
  Date.now = RealDate.now;
  try {
    await pickMenuItem('Collapse previous years');
    await until(
      () => (groupById(yid) && !groupById(yid).dataset.open ? true : null),
      10000,
      'previous-year group closes',
    );
    R.collapsePrevYearsCloses = true;
    R.noDateUntouched = groupById('no-date')?.dataset.open === '1' || 'no-date group closed';
  } finally {
    // eslint-disable-next-line no-global-assign
    Date = RealDate;
  }

  // Date sort, flat: back to no grouping, newest first — the dated shoot
  // (Alpha) leads, the two undated ones trail in name order.
  await pickMenuItem('None');
  await pickMenuItem('Date · newest first');
  await until(() => (childNames()[0] === 'Alpha' ? true : null), 5000, 'dated shoot first');
  R.dateDescDatedFirst =
    childNames().join(',') === 'Alpha,Bravo,Charlie' || childNames().join(',');

  R.finalRows = childNames().join(',');
  return R;
} catch (err) {
  R.fatal = String(err && err.stack ? err.stack : err);
  return R;
}
