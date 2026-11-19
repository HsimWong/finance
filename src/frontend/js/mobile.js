// 家庭账本 · 手机 app（PWA）
// 三个视图：记账（手动 + 拍照 OCR）、账单（按月）、我的（安装）。
// 约束：所有 API 用相对路径，兼容 /finance/ 子路径反代。

const $ = (s) => document.querySelector(s);
const $$ = (s) => Array.from(document.querySelectorAll(s));

const state = {
  meta: null,
  shotId: null,
  rec: null,
  type: 'expense',
  busy: false,
  view: 'add',
  month: '',
  filter: 'all',
  listLoading: false,
  deferredPrompt: null,
};

const titles = { add: '记一笔', list: '账单', me: '我的' };

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

function currentMonth() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
}

function monthLabel(ym) {
  const [y, m] = ym.split('-');
  const now = currentMonth();
  if (ym === now) return '本月';
  if (ym === shiftMonth(now, -1)) return '上月';
  return `${y} 年 ${Number(m)} 月`;
}

function shiftMonth(ym, delta) {
  let [y, m] = ym.split('-').map(Number);
  m += delta;
  if (m < 1) { m += 12; y -= 1; }
  if (m > 12) { m -= 12; y += 1; }
  return `${y}-${String(m).padStart(2, '0')}`;
}

// ---------- 视图切换 ----------
function switchView(name) {
  state.view = name;
  $$('.view').forEach((v) => { v.hidden = v.id !== 'view-' + name; });
  $$('.tab').forEach((t) => t.classList.toggle('on', t.dataset.view === name));
  $('#page-title').textContent = titles[name] || '';
  window.scrollTo(0, 0);
  if (name === 'list') loadList();
  if (name === 'me') updateNet();
}

// ---------- 记账表单 ----------
function fillCategories(selectedId) {
  const sel = $('#m-category');
  const cats = (state.meta.categories || []).filter((c) => (c.kind || 'expense') === state.type);
  const tops = cats.filter((c) => !c.parent_id);
  let html = '<option value="">未分类</option>';
  for (const t of tops) {
    const kids = cats.filter((c) => c.parent_id === t.id);
    if (!kids.length) {
      html += `<option value="${t.id}">${esc(t.name)}</option>`;
    } else {
      html += `<optgroup label="${esc(t.name)}">` +
        kids.map((k) => `<option value="${k.id}">${esc(k.name)}</option>`).join('') + '</optgroup>';
    }
  }
  sel.innerHTML = html;
  if (selectedId) sel.value = String(selectedId);
}

function fillMembers() {
  const sel = $('#m-member');
  const members = state.meta.members || [];
  sel.innerHTML = members.map((m) => `<option value="${m.id}">${esc(m.name)}</option>`).join('');
}

function setType(type) {
  state.type = type === 'income' ? 'income' : 'expense';
  $$('#m-type button').forEach((b) => b.classList.toggle('on', b.dataset.type === state.type));
  fillCategories();
}

function clearPhoto() {
  state.shotId = null;
  state.rec = null;
  $('#m-preview').hidden = true;
  $('#m-img').removeAttribute('src');
  $('#m-file').value = '';
  status('');
}

async function onPick(e) {
  const file = e.target.files && e.target.files[0];
  if (!file) return;
  if (file.size > 10 * 1024 * 1024) { toast('图片不能超过 10 MB', true); return; }
  $('#m-img').src = URL.createObjectURL(file);
  $('#m-preview').hidden = false;
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
  status(rec.error ? '自动识别不可用：' + rec.error : '识别完成，请核对');
  if (s.type === 'income' || s.type === 'expense') setType(s.type);
  if (s.amount) $('#m-amount').value = s.amount;
  if (s.occurred_at) $('#m-time').value = String(s.occurred_at).slice(0, 16);
  if (s.note) $('#m-note').value = s.note;
  if (s.category_id) $('#m-category').value = String(s.category_id);
  const items = rec.items || [];
  $('#m-items-wrap').hidden = items.length === 0;
  $('#m-items-count').textContent = items.length ? items.length + ' 项' : '';
  $('#m-items').innerHTML = items
    .map((i) => `<li><span>${esc(i.name)} × ${esc(i.qty)}${esc(i.unit || '')}</span><span>${esc(i.category_name || '')}</span></li>`)
    .join('');
}

async function onSave() {
  if (state.busy) return;
  const amount = Number($('#m-amount').value);
  if (!(amount > 0)) { toast('请填写金额', true); $('#m-amount').focus(); return; }
  const catVal = $('#m-category').value;
  const memberVal = $('#m-member').value;
  const items = (state.rec && state.rec.items) || [];
  const body = {
    type: state.type,
    amount: amount.toFixed(2),
    occurred_at: $('#m-time').value || nowLocal(),
    member_id: memberVal ? Number(memberVal) : null,
    category_id: catVal ? Number(catVal) : null,
    screenshot_id: state.shotId,
    source: state.shotId ? 'screenshot' : 'manual',
    note: $('#m-note').value.trim(),
    tag_ids: (state.rec && state.rec.suggestion && state.rec.suggestion.tag_ids) || [],
  };
  // 显式声明是否同步库存：仅拍照识别的账单重建本流水的自动入库项；
  // 手动记账 sync_stock=false，后端完全不碰库存（不再靠空数组/缺字段表达意图）。
  body.sync_stock = !!state.shotId;
  body.stock_items = state.shotId
    ? items.map((i) => ({
        name: i.name, qty: i.qty, unit: i.unit, amount: i.amount,
        category_key: i.category_key, category_name: i.category_name, expiry_date: i.expiry_date,
      }))
    : [];

  state.busy = true;
  const btn = $('#m-save');
  btn.disabled = true;
  btn.textContent = '保存中…';
  try {
    await api('api/entries', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
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
  clearPhoto();
  $('#m-amount').value = '';
  $('#m-note').value = '';
  $('#m-time').value = nowLocal();
  $('#m-items-wrap').hidden = true;
  $('#m-items').innerHTML = '';
  fillCategories();
}

async function updateToday() {
  try {
    const day = nowLocal().slice(0, 10);
    const r = await api(`api/entries?from=${day}&to=${day}&limit=1`);
    $('#m-today').textContent = r.total ? `今天已记录 ${r.total} 笔` : '今天还没记账';
  } catch { /* 统计失败不影响记账 */ }
}

// ---------- 账单列表 ----------
function dayKey(occurredAt) {
  return String(occurredAt || '').slice(0, 10);
}

function renderEntries(items) {
  const list = $('#m-list');
  if (!items.length) {
    list.innerHTML = '<div class="empty">这个月还没有账单<br>去「记账」添加第一笔吧</div>';
    return;
  }
  let html = '';
  let lastDay = '';
  for (const e of items) {
    const day = dayKey(e.occurred_at);
    if (day !== lastDay) {
      html += `<div class="day-head">${esc(day)}${day === nowLocal().slice(0, 10) ? ' · 今天' : ''}</div>`;
      lastDay = day;
    }
    const isIncome = e.type === 'income';
    const time = String(e.occurred_at || '').slice(11, 16);
    const name = e.note || e.category_name || (isIncome ? '收入' : '支出');
    const metaParts = [e.category_name, e.member_name, time].filter(Boolean);
    const ico = isIncome ? '↓' : '¥';
    html += `<div class="entry">
      <div class="entry-ico">${ico}</div>
      <div class="entry-main">
        <div class="entry-name">${esc(name)}</div>
        <div class="entry-meta">${esc(metaParts.join(' · '))}</div>
      </div>
      <div class="entry-amt ${e.type}">${isIncome ? '+' : '−'}${esc(e.amount)}</div>
    </div>`;
  }
  list.innerHTML = html;
}

function updateSummary(stat) {
  $('#m-sum-expense').textContent = '¥' + expenseOf(stat);
  $('#m-sum-income').textContent = '¥' + incomeOf(stat);
  $('#m-sum-count').textContent = stat.count;
}

function expenseOf(stat) { return (stat.expense / 100).toFixed(2); }
function incomeOf(stat) { return (stat.income / 100).toFixed(2); }

async function loadList() {
  if (state.listLoading) return;
  state.listLoading = true;
  const list = $('#m-list');
  list.innerHTML = '<div class="empty">加载中…</div>';

  const from = state.month + '-01';
  const to = state.month + '-31';
  const q = `api/entries?from=${from}&to=${to}&limit=500` +
    (state.filter === 'all' ? '' : `&type=${state.filter}`);

  try {
    // 列表 + 收支汇总并行拉取，汇总始终按整月（不受筛选影响）
    const [r, exp, inc] = await Promise.all([
      api(q),
      api(`api/entries?from=${from}&to=${to}&type=expense&limit=1`),
      api(`api/entries?from=${from}&to=${to}&type=income&limit=1`),
    ]);
    renderEntries(r.items || []);
    updateSummary({
      expense: exp.sum_cents || 0,
      income: inc.sum_cents || 0,
      count: (exp.total || 0) + (inc.total || 0),
    });
  } catch (err) {
    list.innerHTML = `<div class="empty">加载失败：${esc(err.message)}</div>`;
  } finally {
    state.listLoading = false;
  }
}

function setMonth(delta) {
  state.month = shiftMonth(state.month, delta);
  $('#m-month').textContent = monthLabel(state.month);
  loadList();
}

function setFilter(filter) {
  state.filter = filter;
  $$('#m-filter button').forEach((b) => b.classList.toggle('on', b.dataset.filter === filter));
  loadList();
}

// ---------- 我的 / 安装 ----------
function updateNet() {
  const el = $('#m-net');
  if (!el) return;
  if (navigator.onLine) el.textContent = '网络正常 · 数据实时同步';
  else el.textContent = '当前离线 · 可查看已缓存账单';
}

function promptInstall() {
  if (state.deferredPrompt) {
    state.deferredPrompt.prompt();
    state.deferredPrompt.userChoice.then(() => { state.deferredPrompt = null; });
  } else {
    const ios = /iPad|iPhone|iPod/.test(navigator.userAgent);
    toast(ios ? '用 Safari 的「分享 → 添加到主屏幕」安装' : '用浏览器菜单里的「安装应用」添加', false);
  }
}

function showInstallButtons() {
  if (!state.deferredPrompt) return;
  $('#m-install').hidden = false;
  $('#m-install2').hidden = false;
}

// ---------- 绑定 ----------
function bind() {
  $$('.tab').forEach((t) => t.addEventListener('click', () => switchView(t.dataset.view)));
  $$('#m-type button').forEach((b) => b.addEventListener('click', () => setType(b.dataset.type)));
  $$('#m-filter button').forEach((b) => b.addEventListener('click', () => setFilter(b.dataset.filter)));

  $('#m-pick').addEventListener('click', () => $('#m-file').click());
  $('#m-file').addEventListener('change', onPick);
  $('#m-preview-x').addEventListener('click', clearPhoto);
  $('#m-save').addEventListener('click', onSave);

  $('#m-prev').addEventListener('click', () => setMonth(-1));
  $('#m-next').addEventListener('click', () => setMonth(1));

  $('#m-reload').addEventListener('click', () => { loadList(); updateToday(); toast('已刷新'); });
  $('#m-install').addEventListener('click', promptInstall);
  $('#m-install2').addEventListener('click', promptInstall);

  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault();
    state.deferredPrompt = e;
    showInstallButtons();
  });
  window.addEventListener('online', updateNet);
  window.addEventListener('offline', updateNet);
}

// ---------- 启动 ----------
async function init() {
  $('#m-time').value = nowLocal();
  $('#m-month').textContent = monthLabel(currentMonth());
  state.month = currentMonth();
  updateNet();

  // 注册 Service Worker（相对路径，兼容子路径）
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('sw.js').catch(() => { /* 离线能力不可用不影响主功能 */ });
  }

  try {
    state.meta = await api('api/meta');
    fillCategories();
    fillMembers();
    bind();
    updateToday();
  } catch (err) {
    status('加载失败：' + err.message);
  }
}

init();
