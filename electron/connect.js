// Connect screen logic. Plain DOM — this page must exist before (and without)
// any daemon, so it cannot be part of the React client.
/* global connectApi */
const $ = (id) => document.getElementById(id);

let editingId = null;

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

async function refresh() {
  const list = await connectApi.listRemotes();
  const box = $('remoteList');
  box.innerHTML = '';
  if (!list.length) {
    box.innerHTML = '<div class="empty">No remote connections yet.</div>';
    return;
  }
  for (const conn of list) {
    const card = document.createElement('div');
    card.className = 'card clickable row';
    card.innerHTML =
      `<div class="grow"><div class="name">${esc(conn.name)}</div>` +
      `<div class="host">${esc(conn.host)}</div></div>` +
      `<span class="status wait">checking…</span>` +
      `<button class="iconbtn" data-act="edit" title="Edit">✎</button>` +
      `<button class="iconbtn" data-act="del" title="Remove">🗑</button>`;
    card.addEventListener('click', (e) => {
      const act = e.target?.dataset?.act;
      if (act === 'edit') {
        openForm(conn);
      } else if (act === 'del') {
        void connectApi.deleteRemote(conn.id).then(refresh);
      } else {
        void connectApi.openRemote(conn.id);
      }
    });
    box.appendChild(card);
    // Live reachability per card, without blocking the list render.
    void connectApi.testRemote(conn.host, conn.token).then((res) => {
      const s = card.querySelector('.status');
      if (!s) return;
      if (res.ok) {
        s.textContent = res.version ? `online · ${res.version}` : 'online';
        s.className = 'status ok';
      } else {
        s.textContent = res.error;
        s.className = 'status err';
      }
    });
  }
}

function openForm(conn) {
  editingId = conn?.id ?? null;
  $('fName').value = conn?.name ?? '';
  $('fHost').value = conn?.host ?? '';
  $('fToken').value = conn?.token ?? '';
  $('fError').textContent = '';
  $('fTest').textContent = '';
  $('editForm').classList.add('open');
  $('fName').focus();
}

$('addBtn').addEventListener('click', () => openForm(null));
$('fCancel').addEventListener('click', () => $('editForm').classList.remove('open'));
$('localCard').addEventListener('click', () => void connectApi.openLocal());
$('closeBtn').addEventListener('click', () => connectApi.close());

$('editForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const host = $('fHost').value.trim();
  const token = $('fToken').value.trim();
  if (!host) {
    $('fError').textContent = 'Host is required.';
    return;
  }
  $('fError').textContent = '';
  $('fTest').textContent = 'testing…';
  $('fTest').className = 'status wait';
  const res = await connectApi.testRemote(host, token);
  if (!res.ok) {
    // Save anyway? No — a saved-but-wrong token would just bounce at connect.
    // The host may legitimately be asleep though, so allow saving on
    // network-unreachable, only hard-block on an auth failure.
    if (res.error === 'invalid token') {
      $('fTest').textContent = '';
      $('fError').textContent = 'The daemon answered, but rejected this token.';
      return;
    }
    $('fTest').textContent = `saved (offline: ${res.error})`;
    $('fTest').className = 'status err';
  } else {
    $('fTest').textContent = res.version ? `online · ${res.version}` : 'online';
    $('fTest').className = 'status ok';
  }
  await connectApi.saveRemote({ id: editingId, name: $('fName').value, host, token });
  $('editForm').classList.remove('open');
  void refresh();
});

void connectApi.getLaunchMode().then((m) => {
  $('startupPick').checked = m === 'picker';
});
$('startupPick').addEventListener('change', (e) => {
  void connectApi.setLaunchMode(e.target.checked ? 'picker' : 'local');
});

void refresh();
