// 家庭账本 · 手机 app（PWA）
// 一级视图：记账（手动 + 拍照 OCR）、账单（按月查看）、总览（预算/使用/计划/库存等）。
// 二级页面：账单详情、预算、使用情况、计划单、库存、基础数据。
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
  sub: null,
  subParam: null,
  subPushed: false,
  overview: null,
  plans: null,
  stock: null,
  edit: null,          // 正在编辑的流水（含回填所需字段）
  allocations: null,   // 编辑页的预算下拉数据
};

const titles = { add: '记一笔', list: '账单', overview: '总览' };
const subTitles = {
  entry: '账单详情', 'entry-edit': '编辑账单', budget: '预算', usage: '使用情况',
  plans: '计划单', stock: '库存', 'stock-item': '库存详情', base: '基础数据',
};

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

function fmtTime(v) {
  return String(v || '').replace('T', ' ').slice(0, 16) || '—';
}

function pct(r) {
  return ((Number(r) || 0) * 100).toFixed(1) + '%';
}

function fmtQty(n) {
  const v = Number(n || 0);
  return Number.isInteger(v) ? String(v) : v.toFixed(2).replace(/\.?0+$/, '');
}

function sourceLabel(s) {
  if (s === 'screenshot') return '账单截图识别';
  if (s === 'manual') return '手动记账';
  return s ? String(s) : '—';
}

function progressLine(label, rate, amount, cls) {
  const w = Math.max(0, Math.min(100, (Number(rate) || 0) * 100)).toFixed(1);
  return `<div class="d-progress">
    <div><span>${esc(label)}</span><b>${esc(amount == null || amount === '' ? pct(rate) : amount)}</b></div>
    <div class="d-bar"><i class="${cls || ''}" style="width:${w}%"></i></div>
  </div>`;
}

// ---------- 一级视图切换 ----------
function switchView(name) {
  state.view = name;
  $$('.view').forEach((v) => { v.hidden = v.id !== 'view-' + name; });
  $$('.tab').forEach((t) => t.classList.toggle('on', t.dataset.view === name));
  $('#page-title').textContent = titles[name] || '';
  window.scrollTo(0, 0);
  if (name === 'list') loadList();
  if (name === 'overview') loadOverview();
}

// ---------- 二级页面 ----------
function openSub(key, param) {
  state.sub = key;
  state.subParam = param;
  state.subPushed = true;
  $('#m-sub-title').textContent = subTitles[key] || '详情';
  // 「编辑」按钮只在账单详情显示；编辑态及其它二级页隐藏
  $('#m-sub-edit').hidden = key !== 'entry';
  $('#m-sub-body').innerHTML = '<div class="empty">加载中…</div>';
  $('#m-sub-body').scrollTop = 0;
  $('#m-sub').hidden = false;
  history.pushState({ financeSub: key, param }, '');
  loadSub(key, param);
}

function closeSub(fromPop) {
  if (!state.sub) return;
  state.sub = null;
  state.subParam = null;
  $('#m-sub').hidden = true;
  if (!fromPop && state.subPushed) {
    state.subPushed = false;
    history.back();
  } else {
    state.subPushed = false;
  }
}

// 返回键逐层回退：编辑页 → 详情页 → 关闭。
// history 里 entry-edit 是压在 entry 之上的一层，popstate 时按记录的目标重建对应页面。
window.addEventListener('popstate', (ev) => {
  const st = ev.state || {};
  if (st.financeSub) {
    const key = st.financeSub;
    state.sub = key;
    state.subParam = st.param;
    state.subPushed = true;
    $('#m-sub-title').textContent = subTitles[key] || '详情';
    $('#m-sub-edit').hidden = key !== 'entry';
    $('#m-sub').hidden = false;
    loadSub(key, st.param);
  } else if (state.sub) {
    closeSub(true);
  }
  state.subPushed = false;
});

async function loadSub(key, param) {
  try {
    if (key === 'entry') return await loadEntryDetail(param);
    if (key === 'entry-edit') return await loadEntryEdit(param);

    if (key === 'budget') {
      const d = (state.overview && state.overview.period === state.month)
        ? state.overview : await api(`api/dashboard?period=${state.month}`);
      $('#m-sub-body').innerHTML = renderBudget(d);
      return;
    }

    if (key === 'usage') {
      const d = (state.overview && state.overview.period === state.month)
        ? state.overview : await api(`api/dashboard?period=${state.month}`);
      $('#m-sub-body').innerHTML = renderUsage(d);
      return;
    }

    if (key === 'plans') {
      const plans = state.plans || await api('api/plans');
      $('#m-sub-body').innerHTML = renderPlans(plans);
      return;
    }

    if (key === 'stock') {
      const period = state.month || currentMonth();
      const [summary, items, consumption] = await Promise.all([
        state.stock || api('api/stock/summary'),
        api('api/stock/items?status=in_stock'),
        api(`api/stock/consumption?period=${period}`),
      ]);
      $('#m-sub-body').innerHTML = renderStock(summary, items, consumption, period);
      return;
    }
    if (key === 'stock-item') return await loadStockItem(param);

    if (key === 'base') {
      const meta = state.meta || await api('api/meta');
      $('#m-sub-body').innerHTML = renderBase(meta);
      return;
    }

    $('#m-sub-body').innerHTML = '<div class="empty">未知页面</div>';
  } catch (err) {
    $('#m-sub-body').innerHTML = `<div class="empty">加载失败：${esc(err.message)}</div>`;
  }
}

// ---------- 账单详情 ----------
async function loadEntryDetail(id) {
  const e = await api(`api/entries/${id}`);
  let alloc = null;
  if (e.allocation_id) {
    try {
      const period = String(e.occurred_at || '').slice(0, 7);
      const list = await api(`api/allocations?period=${period}`);
      alloc = (list || []).find((a) => a.id === e.allocation_id) || null;
    } catch { /* 预算明细拿不到也不影响账单详情 */ }
  }
  $('#m-sub-body').innerHTML = renderEntryDetail(e, alloc);
}

function renderEntryDetail(e, alloc) {
  const tags = e.tags || [];
  const isIncome = e.type === 'income';
  const sign = isIncome ? '+' : '−';
  const tagHtml = tags.length
    ? `<div class="d-tags">${tags.map((t) => `<span class="d-tag">${esc(t.name)}</span>`).join('')}</div>`
    : '<span class="muted">—</span>';

  const allocHtml = alloc
    ? `<b>${esc(alloc.name)}</b>
       <div class="muted">预算 ${esc(alloc.budget)} · 已花 ${esc(alloc.spent)} · 剩余 ${esc(alloc.remaining)}</div>
       ${progressLine('预算使用', alloc.progress, pct(alloc.progress), 'expense')}`
    : (e.allocation_name
        ? `<b>${esc(e.allocation_name)}</b><div class="muted">预算明细暂不可用</div>`
        : '<span class="muted">未关联预算</span>');

  const shotHtml = e.screenshot_id
    ? `<div class="d-card">
         <h3>账单截图</h3>
         <div class="d-shot"><img src="api/screenshots/${e.screenshot_id}/image" alt="账单截图"></div>
         <p class="muted center"><a href="api/screenshots/${e.screenshot_id}/image" target="_blank" rel="noopener">查看原图</a></p>
       </div>`
    : '<div class="d-card"><h3>账单截图</h3><div class="empty">这笔流水没有上传截图</div></div>';

  return `
    <div class="d-card">
      <div class="muted">${isIncome ? '收入' : '支出'} · ${esc(fmtTime(e.occurred_at))}</div>
      <div class="d-big ${e.type}">${sign}${esc(e.amount)}</div>
      <div class="d-row"><span>分类</span><div>${esc(e.category_name || '未分类')}</div></div>
      <div class="d-row"><span>记账人</span><div>${esc(e.member_name || '—')}</div></div>
      <div class="d-row"><span>备注</span><div>${esc(e.note || '—')}</div></div>
      <div class="d-row"><span>来源</span><div>${esc(sourceLabel(e.source))}</div></div>
      <div class="d-row"><span>记录于</span><div>${esc(fmtTime(e.created_at))}</div></div>
    </div>
    <div class="d-card"><h3>预算归属</h3>${allocHtml}</div>
    <div class="d-card"><h3>标签</h3>${tagHtml}</div>
    ${shotHtml}
    <button class="save" id="e-open" type="button" data-entry-id="${e.id}">编辑这笔流水</button>`;
}

// ---------- 账单编辑 ----------
async function loadEntryEdit(id) {
  const [e, allocations] = await Promise.all([
    api(`api/entries/${id}`),
    api('api/allocations'),
  ]);
  state.edit = e;
  state.allocations = allocations || [];
  $('#m-sub-body').innerHTML = renderEntryEdit(e);
}

function renderEntryEdit(e) {
  const meta = state.meta || {};
  const type = e.type === 'income' ? 'income' : 'expense';
  const cats = (meta.categories || []).filter((c) => (c.kind || 'expense') === type);
  const selCat = (v) => (e.category_id === v ? ' selected' : '');
  const opt = (c) => `<option value="${c.id}"${selCat(c.id)}>${esc(c.name)}</option>`;
  const tops = cats.filter((c) => !c.parent_id);
  let catHtml = '<option value="">未分类</option>';
  for (const t of tops) {
    const kids = cats.filter((c) => c.parent_id === t.id);
    if (!kids.length) catHtml += opt(t);
    else catHtml += `<optgroup label="${esc(t.name)}">${kids.map(opt).join('')}</optgroup>`;
  }

  const members = meta.members || [];
  const memberHtml = '<option value="">未指定</option>' +
    members.map((m) => `<option value="${m.id}"${e.member_id === m.id ? ' selected' : ''}>${esc(m.name)}</option>`).join('');

  const allocs = state.allocations || [];
  const allocHtml = '<option value="">不关联预算</option>' +
    allocs.map((a) => `<option value="${a.id}"${e.allocation_id === a.id ? ' selected' : ''}>${esc(a.period)} · ${esc(a.name)}</option>`).join('');

  const selectedTagIds = new Set((e.tags || []).map((t) => t.id));
  const tags = meta.tags || [];
  const tagHtml = tags.length
    ? tags.map((t) => `<button type="button" class="tag-chip${selectedTagIds.has(t.id) ? ' on' : ''}" data-tag-id="${t.id}">${esc(t.name)}</button>`).join('')
    : '<span class="tag-empty">暂无可选标签</span>';

  const localTime = String(e.occurred_at || '').replace('T', ' ').slice(0, 16);

  return `
    <div class="form edit-form" id="e-form" data-entry-id="${e.id}" data-source="${esc(e.source || 'manual')}" data-shot-id="${e.screenshot_id || ''}">
      <div class="seg" id="e-type">
        <button type="button" data-type="expense" class="${type === 'expense' ? 'on' : ''}">支出</button>
        <button type="button" data-type="income" class="${type === 'income' ? 'on' : ''}">收入</button>
      </div>
      <label class="field amount">
        <span>金额</span>
        <div class="amount-box"><em>¥</em><input id="e-amount" type="number" inputmode="decimal" step="0.01" min="0" value="${esc(e.amount)}"></div>
      </label>
      <label class="field"><span>分类</span><select id="e-category">${catHtml}</select></label>
      <label class="field"><span>时间</span><input id="e-time" type="datetime-local" value="${esc(localTime)}"></label>
      <label class="field"><span>备注</span><input id="e-note" type="text" placeholder="商户 / 说明" value="${esc(e.note || '')}"></label>
      <label class="field"><span>记账人</span><select id="e-member">${memberHtml}</select></label>
      <label class="field"><span>关联预算</span><select id="e-allocation">${allocHtml}</select></label>
      <div class="field"><span>标签（可多选）</span><div class="tag-chips" id="e-tags">${tagHtml}</div></div>
      <p class="hint">编辑不会改动自动入库的库存；截图保持原样。</p>
      <button class="save" id="e-save" type="button">保存修改</button>
    </div>`;
}

function setEditType(type) {
  const t = type === 'income' ? 'income' : 'expense';
  $$('#e-type button').forEach((b) => b.classList.toggle('on', b.dataset.type === t));
  const e = state.edit;
  const cats = ((state.meta || {}).categories || []).filter((c) => (c.kind || 'expense') === t);
  const tops = cats.filter((c) => !c.parent_id);
  let html = '<option value="">未分类</option>';
  for (const c of tops) {
    const kids = cats.filter((k) => k.parent_id === c.id);
    if (!kids.length) html += `<option value="${c.id}">${esc(c.name)}</option>`;
    else html += `<optgroup label="${esc(c.name)}">${kids.map((k) => `<option value="${k.id}">${esc(k.name)}</option>`).join('')}</optgroup>`;
  }
  $('#e-category').innerHTML = html;
}

async function onSaveEdit() {
  if (state.busy) return;
  const form = $('#e-form');
  if (!form) return;
  const id = Number(form.dataset.entryId);
  const amount = Number($('#e-amount').value);
  if (!(amount > 0)) { toast('请填写金额', true); $('#e-amount').focus(); return; }

  const catVal = $('#e-category').value;
  const memberVal = $('#e-member').value;
  const allocVal = $('#e-allocation').value;
  const tagIds = $$('#e-tags .tag-chip.on').map((b) => Number(b.dataset.tagId));
  const body = {
    type: $('#e-type button.on').dataset.type,
    amount: amount.toFixed(2),
    occurred_at: $('#e-time').value || nowLocal(),
    member_id: memberVal ? Number(memberVal) : null,
    category_id: catVal ? Number(catVal) : null,
    allocation_id: allocVal ? Number(allocVal) : null,
    // 编辑保留原截图与原来源，避免把截图关联清掉
    screenshot_id: form.dataset.shotId ? Number(form.dataset.shotId) : null,
    source: form.dataset.source || 'manual',
    note: $('#e-note').value.trim(),
    tag_ids: tagIds,
    // 关键：编辑不传 sync_stock，后端不会碰库存（自动项与手工项都保持原样）
  };

  state.busy = true;
  const btn = $('#e-save');
  btn.disabled = true;
  btn.textContent = '保存中…';
  try {
    await api(`api/entries/${id}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    });
    toast('账单已更新');
    // 缓存失效，返回详情后重新拉取
    state.allocations = null;
    state.overview = null;
    state.plans = null;
    state.stock = null;
    loadList();
    closeSub(false); // 回到账单详情
  } catch (err) {
    toast('保存失败：' + err.message, true);
  } finally {
    state.busy = false;
    if (btn && btn.isConnected) { btn.disabled = false; btn.textContent = '保存修改'; }
  }
}

// ---------- 预算 ----------
function renderBudget(d) {
  const allocs = d.allocations || [];
  const list = allocs.length
    ? `<div class="d-list">${allocs.map((a) => {
        const cls = a.status === '超额' ? 'warn' : a.status === '已用完' ? 'done' : '';
        return `<div class="d-item">
          <div class="d-item-head"><b>${esc(a.name)}</b><span class="d-pill ${cls}">${esc(a.status || '进行中')}</span></div>
          <div class="muted">${esc(a.period)} · ${a.batch === 'first' ? '月初' : '月中'} · ${esc(a.category_name || '未设分类')}</div>
          ${progressLine('已花 ' + a.spent + ' · 剩余 ' + a.remaining, a.progress, pct(a.progress), a.status === '超额' ? 'warn' : 'expense')}
        </div>`;
      }).join('')}</div>`
    : '<div class="empty">本期还没有预分配预算</div>';

  return `
    <div class="d-hero">
      <div class="d-stat"><span>本期收入</span><b class="income">${esc(d.income)}</b></div>
      <div class="d-stat"><span>本期支出</span><b class="expense">${esc(d.expense)}</b></div>
      <div class="d-stat"><span>已分配</span><b>${esc(d.allocated)}</b></div>
      <div class="d-stat"><span>预算剩余</span><b>${esc(d.remaining)}</b></div>
    </div>
    <div class="d-card">
      <h3>资金预分配 · ${esc(d.period)}</h3>
      ${progressLine('已预先分配', d.allocation_rate, d.allocated + ' · ' + pct(d.allocation_rate))}
      ${progressLine('预算已使用', d.usage_rate, d.used + ' · ' + pct(d.usage_rate), d.usage_rate > 1 ? 'warn' : 'expense')}
      <div class="muted">未分配收入 ${esc(d.unallocated)}</div>
    </div>
    <div class="d-card"><h3>预算明细</h3>${list}</div>`;
}

// ---------- 使用情况 ----------
function renderUsage(d) {
  const rows = d.category_breakdown || [];
  const max = Math.max(...rows.map((r) => r.total_cents), 1);
  const breakdown = rows.length
    ? `<div class="d-list">${rows.map((r) => `<div class="d-item">
        <div class="d-item-head"><b>${esc(r.label)}</b><span class="muted">${esc(r.total)} · ${r.count} 笔</span></div>
        ${progressLine('', r.total_cents / max, '', 'expense')}
      </div>`).join('')}</div>`
    : '<div class="empty">本期还没有支出记录</div>';

  return `
    <div class="d-hero">
      <div class="d-stat"><span>收入</span><b class="income">${esc(d.income)}</b></div>
      <div class="d-stat"><span>支出</span><b class="expense">${esc(d.expense)}</b></div>
      <div class="d-stat"><span>结余</span><b>${esc(d.balance)}</b></div>
      <div class="d-stat"><span>账单笔数</span><b>${d.entry_count || 0}</b></div>
    </div>
    <div class="d-card">
      <h3>预算执行 · ${esc(d.period)}</h3>
      ${progressLine('分配率', d.allocation_rate, d.allocated + ' / ' + d.income + ' · ' + pct(d.allocation_rate))}
      ${progressLine('使用率', d.usage_rate, d.used + ' / ' + d.allocated + ' · ' + pct(d.usage_rate), d.usage_rate > 1 ? 'warn' : 'expense')}
      <div class="muted">预算剩余 ${esc(d.remaining)} · 未分类流水 ${d.uncategorized_count || 0} 笔</div>
    </div>
    <div class="d-card"><h3>分类支出</h3>${breakdown}</div>`;
}

// ---------- 计划单 ----------
function renderPlans(plans) {
  const active = plans.filter((p) => p.status === 'active').length;
  const done = plans.length - active;
  const list = plans.length
    ? `<div class="d-list">${plans.map((p) => {
        const finished = p.status === 'completed';
        const allocs = p.allocations || [];
        return `<div class="d-item">
          <div class="d-item-head"><b>${esc(p.name)}</b><span class="d-pill ${finished ? 'done' : ''}">${finished ? '已完成' : '进行中'}</span></div>
          <div class="muted">${esc(p.period || '')} · 预算 ${esc(p.budget)} · 已花 ${esc(p.spent)} · 剩余 ${esc(p.remaining)}</div>
          ${allocs.length
            ? `<div class="d-meta">${allocs.map((a) => `<span>${esc(a.name)} ${esc(a.budget)}</span>`).join('')}</div>`
            : '<div class="muted">未关联预算</div>'}
          ${progressLine('预算使用', p.progress, pct(p.progress), finished ? '' : 'expense')}
          ${p.note ? `<div class="muted">${esc(p.note)}</div>` : ''}
          ${finished && p.completed_at ? `<div class="muted">完成于 ${esc(fmtTime(p.completed_at))}</div>` : ''}
        </div>`;
      }).join('')}</div>`
    : '<div class="empty">还没有计划单，可在电脑版新建并关联预算</div>';

  return `
    <div class="d-card">
      <h3>计划单</h3>
      <div class="muted">共 ${plans.length} 张 · 进行中 ${active} · 已完成 ${done}</div>
      <div class="muted">流水挂到关联预算时，整张计划单会自动标记完成。</div>
    </div>
    ${list}`;
}

// ---------- 库存 ----------
function renderStock(summary, items, consumption, period) {
  const cats = summary.categories || [];
  const alerts = summary.alerts || [];
  const catList = cats.length
    ? `<div class="d-list">${cats.map((c) => `<div class="d-item">
        <div class="d-item-head"><b>${esc(c.name)}</b><span class="d-pill">${c.item_count} 项</span></div>
        <div class="muted">数量 ${fmtQty(c.total_qty)} · 金额 ${esc(c.amount)}</div>
        ${c.expired || c.expiring_soon ? `<div class="muted">已过期 ${c.expired} · 临期 ${c.expiring_soon}</div>` : ''}
      </div>`).join('')}</div>`
    : '<div class="empty">库存为空</div>';

  const alertList = alerts.length
    ? `<div class="d-list">${alerts.map((s) => `<div class="d-item">
        <div class="d-item-head"><b>${esc(s.name)}</b><span class="d-pill danger">${s.expiry_status === 'expired' ? '已过期' : '临期'}</span></div>
        <div class="muted">${esc(s.expiry_date || '未设保质期')} · ${s.days_left == null ? '—' : '剩 ' + s.days_left + ' 天'}</div>
      </div>`).join('')}</div>`
    : '<div class="empty">暂无临期或过期物品</div>';

  // 在库物品可点开详情（含消耗/损耗操作）
  const itemList = items.length
    ? `<div class="d-list">${items.map((s) => `<button type="button" class="d-item d-item-btn" data-stock-id="${s.id}">
        <div class="d-item-head"><b>${esc(s.name)}</b><span class="d-pill ${s.expiry_status === 'expired' ? 'danger' : s.expiry_status === 'soon' ? 'warn' : ''}">${s.expiry_status === 'expired' ? '已过期' : s.expiry_status === 'soon' ? '临期' : '在库'}</span></div>
        <div class="muted">${esc(s.category_name)} · ${fmtQty(s.qty)}${esc(s.unit || '')} · ${esc(s.amount)} · 点开详情 ›</div>
      </button>`).join('')}</div>`
    : '<div class="empty">没有在库物品</div>';

  const stat = renderStockConsumption(consumption, period);

  return `
    <div class="d-hero">
      <div class="d-stat"><span>在库种类</span><b>${summary.total_items || 0}</b></div>
      <div class="d-stat"><span>库存金额</span><b>${esc(summary.total_amount)}</b></div>
      <div class="d-stat"><span>临期</span><b class="expense">${summary.expiring_soon || 0}</b></div>
      <div class="d-stat"><span>已过期</span><b class="expense">${summary.expired || 0}</b></div>
    </div>
    ${stat}
    <div class="d-card"><h3>分类汇总</h3>${catList}</div>
    <div class="d-card"><h3>临期提醒</h3>${alertList}</div>
    <div class="d-card"><h3>在库物品</h3>${itemList}</div>`;
}

function renderStockConsumption(d, period) {
  const c = (d && d.consume) || {}, w = (d && d.waste) || {};
  const rankList = (list) => (list || []).length
    ? `<div class="d-list">${list.slice(0, 8).map((r) => `<div class="d-item">
        <div class="d-item-head"><b>${esc(r.name)}</b><span class="muted">消 ${fmtQty(r.consume_qty)} · 损 ${fmtQty(r.waste_qty)}</span></div>
      </div>`).join('')}</div>`
    : '';
  return `<div class="d-card">
    <h3>消耗 / 损耗统计 · ${esc(period || '全部')}</h3>
    <div class="d-hero" style="grid-template-columns:1fr 1fr">
      <div class="d-stat"><span>消耗</span><b class="expense">${fmtQty(c.qty || 0)}</b><small class="muted">${c.count || 0} 笔 · ${c.items || 0} 种</small></div>
      <div class="d-stat"><span>损耗</span><b class="expense">${fmtQty(w.qty || 0)}</b><small class="muted">${w.count || 0} 笔 · ${w.items || 0} 种</small></div>
    </div>
    ${(d && d.by_item && d.by_item.length) ? `<h4 style="margin:2px 0 0;font-size:13px">按物品</h4>${rankList(d.by_item)}` : '<div class="empty">该月没有消耗 / 损耗记录</div>'}
  </div>`;
}

// ---------- 库存项详情 + 消耗/损耗 ----------
async function loadStockItem(id) {
  const [all, movements] = await Promise.all([
    api('api/stock/items'),
    api(`api/stock/movements?item_id=${id}`),
  ]);
  const s = (all || []).find((x) => x.id === Number(id));
  if (!s) { $('#m-sub-body').innerHTML = '<div class="empty">库存项不存在或已删除</div>'; return; }
  $('#m-sub-body').innerHTML = renderStockItem(s, movements || []);
}

function renderStockItem(s, movements) {
  const kindLabel = { in: '入库', consume: '消耗', waste: '损耗' };
  const mvHtml = movements.length
    ? `<div class="d-list">${movements.map((m) => `<div class="d-item">
        <div class="d-item-head"><b>${esc(kindLabel[m.kind] || m.kind)}</b><span class="muted">${esc(fmtTime(m.created_at))}</span></div>
        <div class="muted">数量 ${fmtQty(m.qty)} · ${esc(m.note || '—')}</div>
      </div>`).join('')}</div>`
    : '<div class="empty">暂无出入库记录</div>';

  return `
    <div class="d-card">
      <div class="d-big">${esc(s.name)}</div>
      <div class="d-row"><span>分类</span><div>${esc(s.category_name)}</div></div>
      <div class="d-row"><span>在库数量</span><div>${fmtQty(s.qty)} ${esc(s.unit || '')}</div></div>
      <div class="d-row"><span>金额</span><div>${esc(s.amount)}</div></div>
      <div class="d-row"><span>保质期</span><div>${esc(s.expiry_date || '未设')}${s.days_left == null ? '' : ' · 剩 ' + s.days_left + ' 天'}</div></div>
      <div class="d-row"><span>来源</span><div>${s.auto_created ? '账单自动入库' : '手工录入'}</div></div>
    </div>
    <div class="d-card">
      <h3>操作</h3>
      <div class="seg">
        <button type="button" class="btn-consume" data-stock-id="${s.id}" data-kind="consume">消耗</button>
        <button type="button" class="btn-waste" data-stock-id="${s.id}" data-kind="waste">损耗</button>
      </div>
      <p class="muted">留空表示全部用完。消耗=正常用量，损耗=报废丢弃。</p>
    </div>
    <div class="d-card"><h3>出入库记录</h3>${mvHtml}</div>`;
}

async function onStockMovement(kind, id) {
  const label = kind === 'consume' ? '消耗' : '损耗';
  const raw = prompt(`${label}数量（留空表示全部${label}）`, '');
  if (raw === null) return;
  const qty = Number(String(raw).trim());
  const body = { note: label };
  if (String(raw).trim() && qty > 0) body.qty = qty;
  state.busy = true;
  try {
    await api(`api/stock/items/${id}/${kind}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    });
    toast('已登记' + label);
    state.stock = null; // 汇总缓存失效
    loadStockItem(id);
  } catch (err) {
    toast(label + '失败：' + err.message, true);
  } finally {
    state.busy = false;
  }
}

// ---------- 基础数据 ----------
function renderBase(meta) {
  const members = meta.members || [];
  const tags = meta.tags || [];
  const rules = meta.stock_rules || [];
  const cats = meta.categories || [];
  const roots = cats.filter((c) => !c.parent_id);
  const chips = (arr, fn) => arr.length
    ? `<div class="d-meta">${arr.map((x) => `<span>${fn(x)}</span>`).join('')}</div>`
    : '<div class="muted">暂无</div>';

  return `
    <div class="d-hero">
      <div class="d-stat"><span>成员</span><b>${members.length}</b></div>
      <div class="d-stat"><span>一级分类</span><b>${roots.length}</b></div>
      <div class="d-stat"><span>标签</span><b>${tags.length}</b></div>
      <div class="d-stat"><span>库存品类</span><b>${rules.length}</b></div>
    </div>
    <div class="d-card"><h3>成员</h3>${chips(members, (m) => esc(m.name))}</div>
    <div class="d-card"><h3>一级分类</h3>${chips(roots, (c) => esc(c.name))}</div>
    <div class="d-card"><h3>标签</h3>${chips(tags, (t) => esc(t.name))}</div>
    <div class="d-card"><h3>库存品类</h3>${chips(rules, (r) => esc(r.name) + ' · ' + r.shelf_days + ' 天')}</div>`;
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
    list.innerHTML = '<div class="empty">这个月还没有账单</div>';
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
    html += `<button class="entry" type="button" data-entry-id="${e.id}">
      <span class="entry-ico">${ico}</span>
      <span class="entry-main">
        <span class="entry-name">${esc(name)}</span>
        <span class="entry-meta">${esc(metaParts.join(' · '))}</span>
      </span>
      <span class="entry-amt ${e.type}">${isIncome ? '+' : '−'}${esc(e.amount)}</span>
    </button>`;
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

// ---------- 总览 ----------
async function loadOverview() {
  const period = state.month || currentMonth();
  ['ov-budget', 'ov-usage', 'ov-plans', 'ov-stock'].forEach((id) => {
    const el = $('#' + id);
    if (el) el.textContent = '加载中…';
  });
  try {
    const [dash, plans, stock] = await Promise.all([
      api(`api/dashboard?period=${period}`),
      api('api/plans'),
      api('api/stock/summary'),
    ]);
    state.overview = dash;
    state.plans = plans || [];
    state.stock = stock;
    $('#ov-budget').textContent = `已分配 ${dash.allocated} · 已用 ${dash.used}`;
    $('#ov-usage').textContent = `收入 ${dash.income} · 支出 ${dash.expense}`;
    const active = state.plans.filter((p) => p.status === 'active').length;
    $('#ov-plans').textContent = `${active} 进行中 · 共 ${state.plans.length} 张`;
    $('#ov-stock').textContent = `${stock.total_items || 0} 项 · ${stock.expiring_soon || 0} 项临期`;
  } catch (err) {
    ['ov-budget', 'ov-usage', 'ov-plans', 'ov-stock'].forEach((id) => {
      const el = $('#' + id);
      if (el) el.textContent = '加载失败';
    });
    toast('总览加载失败：' + err.message, true);
  }
}

function updateNet() {
  const el = $('#m-net');
  if (!el) return;
  if (navigator.onLine) el.textContent = '网络正常 · 数据实时同步';
  else el.textContent = '当前离线 · 可查看已缓存数据';
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
  $$('[data-sub]').forEach((b) => b.addEventListener('click', () => openSub(b.dataset.sub)));
  $$('#m-type button').forEach((b) => b.addEventListener('click', () => setType(b.dataset.type)));
  $$('#m-filter button').forEach((b) => b.addEventListener('click', () => setFilter(b.dataset.filter)));

  $('#m-pick').addEventListener('click', () => $('#m-file').click());
  $('#m-file').addEventListener('change', onPick);
  $('#m-preview-x').addEventListener('click', clearPhoto);
  $('#m-save').addEventListener('click', onSave);

  $('#m-prev').addEventListener('click', () => setMonth(-1));
  $('#m-next').addEventListener('click', () => setMonth(1));
  $('#m-list').addEventListener('click', (ev) => {
    const btn = ev.target.closest('[data-entry-id]');
    if (btn) openSub('entry', Number(btn.dataset.entryId));
  });

  $('#m-sub-back').addEventListener('click', () => closeSub(false));
  // 账单编辑：顶部按钮 + 详情底部按钮 + 表单内的类型/标签/保存（事件委托）
  $('#m-sub-edit').addEventListener('click', () => {
    if (state.sub === 'entry' && state.subParam != null) openSub('entry-edit', state.subParam);
  });
  $('#m-sub-body').addEventListener('click', (ev) => {
    const openBtn = ev.target.closest('#e-open');
    if (openBtn) { openSub('entry-edit', Number(openBtn.dataset.entryId)); return; }
    const typeBtn = ev.target.closest('#e-type button');
    if (typeBtn) { setEditType(typeBtn.dataset.type); return; }
    const chip = ev.target.closest('.tag-chip');
    if (chip) { chip.classList.toggle('on'); return; }
    const saveBtn = ev.target.closest('#e-save');
    if (saveBtn) { onSaveEdit(); return; }
    const stockBtn = ev.target.closest('[data-stock-id]');
    if (stockBtn && !stockBtn.dataset.kind) { openSub('stock-item', Number(stockBtn.dataset.stockId)); return; }
    const mvBtn = ev.target.closest('[data-kind]');
    if (mvBtn && (mvBtn.dataset.kind === 'consume' || mvBtn.dataset.kind === 'waste')) {
      onStockMovement(mvBtn.dataset.kind, Number(mvBtn.dataset.stockId));
      return;
    }
  });
  $('#m-reload').addEventListener('click', () => { loadOverview(); loadList(); updateToday(); toast('已刷新'); });
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
