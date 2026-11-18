// 手机端极简页：只做「拍照 → 识别 → 保存」，其余功能留在电脑版。
const $ = (s) => document.querySelector(s);
const state = { meta: null, shotId: null, rec: null, type: 'expense', busy: false };

const esc = (v) =>
  String(v == null ? '' : v).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

async function api(path, opts = {}) {
  const res = await fetch(path, opts);
  const body = await res.json().catch(() => ({ ok: false, error: '服务响应异常（HTTP ' + res.status + '）' }));
  if (res.status === 401) throw new Error('访问口令已失效，请刷新页面重新输入');
  if (!res.ok || body.ok === false) throw new Error(body.error || 'HTTP ' + res.status);
  return body.data;
}

let toastTimer;
function toast(msg, bad) {
  const el = $('#m-toast');
  el.textContent = msg;
  el.className = 'toast show' + (bad ? ' bad' : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = 'toast'; }, 2800);
}

function status(text, busy) {
  const el = $('#m-status');
  el.textContent = text || '';
  el.className = 'status' + (busy ? ' busy' : '');
}

function nowLocal() {
  const d = new Date(Date.now() - new Date().getTimezoneOffset() * 60000);
  return d.toISOString().slice(0, 16);
}

function fillCategories(selectedId) {
  const sel = $('#m-category');
  const cats = (state.meta.categories || []).filter((c) => (c.kind || 'expense') === state.type);
  const tops = cats.filter((c) => !c.parent_id);
  let html = '<option value="">未分类（会进报警表）</option>';
  for (const t of tops) {
    const kids = cats.filter((c) => c.parent_id === t.id);
    if (!kids.length) {
      html += `<option value="${t.id}">${esc(t.name)}</option>`;
    } else {
      html +=
        `<optgroup label="${esc(t.name)}">` +
        kids.map((k) => `<option value="${k.id}">${esc(k.name)}</option>`).join('') +
        '</optgroup>';
    }
  }
  sel.innerHTML = html;
  if (selectedId) sel.value = String(selectedId);
}

function setType(type) {
  state.type = type === 'income' ? 'income' : 'expense';
  document.querySelectorAll('#m-type button').forEach((b) => {
    b.classList.toggle('on', b.dataset.type === state.type);
  });
  fillCategories();
}

async function onPick(e) {
  const file = e.target.files && e.target.files[0];
  if (!file) return;
  if (file.size > 10 * 1024 * 1024) {
    toast('图片不能超过 10 MB', true);
    return;
  }
  $('#m-img').src = URL.createObjectURL(file);
  $('#m-preview').hidden = false;
  $('#m-form').hidden = false;
  $('#m-time').value = nowLocal();
  status('上传中…', true);

  try {
    const fd = new FormData();
    fd.append('file', file);
    const shot = await api('api/screenshots', { method: 'POST', body: fd });
    state.shotId = shot.id;

    status('识别中…首次可能要几秒', true);
    const rec = await api('api/ocr/recognize', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ screenshot_id: shot.id }),
    });
    state.rec = rec;
    applyRecognition(rec);
  } catch (err) {
    status('自动识别失败：' + err.message);
    toast('可以直接手填金额后保存', true);
  }
}

function applyRecognition(rec) {
  const s = rec.suggestion || {};
  if (rec.error) {
    status('自动识别不可用：' + rec.error);
  } else {
    status('识别完成，请核对金额与分类');
  }
  if (s.type === 'income' || s.type === 'expense') setType(s.type);
  if (s.amount) $('#m-amount').value = s.amount;
  if (s.occurred_at) $('#m-time').value = String(s.occurred_at).slice(0, 16);
  if (s.note) $('#m-note').value = s.note;
  if (s.category_id) $('#m-category').value = String(s.category_id);

  const items = rec.items || [];
  $('#m-items-wrap').hidden = items.length === 0;
  $('#m-items-count').textContent = items.length ? items.length + ' 项' : '';
  $('#m-items').innerHTML = items
    .map(
      (i) =>
        `<li><span>${esc(i.name)} × ${esc(i.qty)}${esc(i.unit || '')}</span><span>${esc(i.category_name || '')}</span></li>`
    )
    .join('');
}

async function onSave() {
  if (state.busy) return;
  const amount = Number($('#m-amount').value);
  if (!(amount > 0)) {
    toast('请填写金额', true);
    $('#m-amount').focus();
    return;
  }
  const catVal = $('#m-category').value;
  const items = (state.rec && state.rec.items) || [];
  const body = {
    type: state.type,
    amount: amount.toFixed(2),
    occurred_at: $('#m-time').value || nowLocal(),
    member_id: state.meta.members && state.meta.members[0] ? state.meta.members[0].id : null,
    category_id: catVal ? Number(catVal) : null,
    screenshot_id: state.shotId,
    source: 'screenshot',
    note: $('#m-note').value.trim(),
    tag_ids: (state.rec && state.rec.suggestion && state.rec.suggestion.tag_ids) || [],
    stock_items: items.map((i) => ({
      name: i.name,
      qty: i.qty,
      unit: i.unit,
      amount: i.amount,
      category_key: i.category_key,
      category_name: i.category_name,
      expiry_date: i.expiry_date,
    })),
  };

  state.busy = true;
  const btn = $('#m-save');
  btn.disabled = true;
  btn.textContent = '保存中…';
  try {
    await api('api/entries', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    toast('已记录 ¥' + amount.toFixed(2));
    resetForm();
    updateToday();
  } catch (err) {
    toast('保存失败：' + err.message, true);
  } finally {
    state.busy = false;
    btn.disabled = false;
    btn.textContent = '保存这笔';
  }
}

function resetForm() {
  state.shotId = null;
  state.rec = null;
  $('#m-form').hidden = true;
  $('#m-preview').hidden = true;
  $('#m-img').removeAttribute('src');
  $('#m-file').value = '';
  $('#m-amount').value = '';
  $('#m-note').value = '';
  $('#m-time').value = nowLocal();
  $('#m-items-wrap').hidden = true;
  $('#m-items').innerHTML = '';
  status('');
  fillCategories();
}

async function updateToday() {
  try {
    const d = new Date();
    const day = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    const r = await api(`api/entries?from=${day}&to=${day}`);
    $('#m-today').textContent = r.total ? `今天已记录 ${r.total} 笔` : '';
  } catch {
    /* 统计失败不影响记账 */
  }
}

function bind() {
  $('#m-pick').addEventListener('click', () => $('#m-file').click());
  $('#m-file').addEventListener('change', onPick);
  $('#m-save').addEventListener('click', onSave);
  document.querySelectorAll('#m-type button').forEach((b) => {
    b.addEventListener('click', () => setType(b.dataset.type));
  });
}

async function init() {
  $('#m-time').value = nowLocal();
  try {
    state.meta = await api('api/meta');
    fillCategories();
    bind();
    updateToday();
  } catch (err) {
    status('加载失败：' + err.message);
  }
}

init();
