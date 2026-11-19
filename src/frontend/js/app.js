import { get, post, put, patch, del, upload, imageUrl, esc, localDateTime, currentMonth } from './api.js';

const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));

const state = {
  meta: { members: [], categories: [], tags: [] },
  stockCategories: [],
  period: currentMonth(),
  tab: 'dashboard',
  allocations: [],
  plans: [],
  screenshots: [],
  recognition: null,
  ocrAvailable: false,
};

const money = (s) => (String(s).startsWith('-') ? `-¥${String(s).slice(1)}` : `¥${s}`);
const pct = (x) => `${Math.round((x || 0) * 100)}%`;

function toast(msg, error = false) {
  const el = $('#toast');
  el.textContent = msg;
  el.className = `toast show${error ? ' error' : ''}`;
  clearTimeout(el._timer);
  el._timer = setTimeout(() => { el.className = 'toast'; }, 2800);
}

async function guard(fn) {
  try {
    return await fn();
  } catch (e) {
    console.error(e);
    toast(e.message || '操作失败', true);
  }
}

// ---------- 下拉与标签 ----------

function categoryLabel(c) {
  if (c.parent_id == null) return c.name;
  const parent = state.meta.categories.find((x) => x.id === c.parent_id);
  return parent ? `${parent.name} › ${c.name}` : c.name;
}

function categoryOptions(placeholder) {
  const cats = state.meta.categories;
  let html = `<option value="">${esc(placeholder)}</option>`;
  for (const p of cats.filter((c) => c.parent_id == null)) {
    const children = cats.filter((c) => c.parent_id === p.id);
    html += `<optgroup label="${esc(p.name)}">`;
    html += `<option value="${p.id}">${esc(p.name)}（一级）</option>`;
    for (const c of children) html += `<option value="${c.id}">${esc(c.name)}</option>`;
    html += '</optgroup>';
  }
  return html;
}

function tagChips(selected = []) {
  const ids = selected.map(Number);
  const html = state.meta.tags.map((t) =>
    `<label class="chip"><input type="checkbox" value="${t.id}"${ids.includes(t.id) ? ' checked' : ''}>${esc(t.name)}</label>`
  ).join('');
  return html || '<span class="muted">还没有标签，可在「基础数据」里添加</span>';
}

const checkedTags = (box) => $$('input:checked', box).map((i) => Number(i.value));

function formVal(form, name) {
  const el = form.elements[name];
  return el ? String(el.value ?? '').trim() : '';
}
const formNum = (form, name) => (formVal(form, name) ? Number(formVal(form, name)) : null);

function fillSelects() {
  $$('select[name="member_id"]').forEach((el) => {
    const isFilter = !!el.closest('#filter-form, #report-form');
    el.innerHTML = `<option value="">${isFilter ? '全部成员' : '未指定'}</option>` +
      state.meta.members.map((m) => `<option value="${m.id}">${esc(m.name)}</option>`).join('');
    if (el.closest('#entry-form') && state.meta.members.length) el.value = String(state.meta.members[0].id);
  });
  $$('select[name="category_id"]').forEach((el) => {
    const id = el.closest('form')?.id || '';
    let ph = '全部分类';
    if (id === 'entry-form') ph = '未分类（进报警表）';
    if (id === 'allocation-form') ph = '不指定分类';
    el.innerHTML = categoryOptions(ph);
  });
  $$('select[name="tag_id"]').forEach((el) => {
    el.innerHTML = '<option value="">全部标签</option>' +
      state.meta.tags.map((t) => `<option value="${t.id}">${esc(t.name)}</option>`).join('');
  });
  const allocSel = $('#entry-form select[name="allocation_id"]');
  if (allocSel) {
    const keep = allocSel.value;
    allocSel.innerHTML = '<option value="">不关联预算</option>' +
      state.allocations.map((a) => `<option value="${a.id}">${esc(a.period)} ${a.batch === 'first' ? '月初' : '月中'} · ${esc(a.name)}</option>`).join('');
    if (keep) allocSel.value = keep;
  }
  const shotSel = $('#entry-form select[name="screenshot_id"]');
  if (shotSel) {
    const keep = shotSel.value;
    shotSel.innerHTML = '<option value="">不关联截图</option>' +
      state.screenshots.map((s) => `<option value="${s.id}">#${s.id} ${esc(s.filename)}</option>`).join('');
    if (keep) shotSel.value = keep;
  }
  const parentSel = $('#category-form select[name="parent_id"]');
  if (parentSel) parentSel.innerHTML = '<option value="">一级分类</option>' +
    state.meta.categories.map((c) => `<option value="${c.id}">${esc(categoryLabel(c))}</option>`).join('');
  const box = $('#entry-tag-box');
  if (box) {
    const kept = checkedTags(box);
    box.innerHTML = tagChips(kept);
  }
  const planAllocBox = $('#plan-alloc-box');
  if (planAllocBox) {
    planAllocBox.innerHTML = state.allocations.length
      ? state.allocations.map((a) => `<label class="chip"><input type="checkbox" value="${a.id}">${esc(a.period)} ${a.batch === 'first' ? '月初' : '月中'} · ${esc(a.name)}（${money(a.budget)}）</label>`).join('')
      : '<span class="muted">还没有预算，可先在「预算」页添加</span>';
  }
  const rules = state.meta.stock_rules || [];
  const ruleOptions = (ph) => `<option value="">${ph}</option>` +
    rules.map((r) => `<option value="${esc(r.key)}">${esc(r.name)}</option>`).join('') +
    '<option value="other">其他</option>';
  $$('select[name="category_key"]').forEach((el) => {
    el.innerHTML = ruleOptions(el.closest('#stock-filter') ? '全部品类' : '自动判断（按名称）');
  });
}

async function refreshBase() {
  [state.meta, state.allocations, state.screenshots, state.plans, state.stockCategories] = await Promise.all([
    get('api/meta'), get('api/allocations'), get('api/screenshots'), get('api/plans'), get('api/stock-categories'),
  ]);
  fillSelects();
  renderSettings();
  refreshPlanBadge();
}

function refreshPlanBadge() {
  const el = $('#plan-badge');
  if (!el) return;
  const n = state.plans.filter((p) => p.status === 'active').length;
  el.textContent = n;
  el.hidden = n === 0;
}

// ---------- 渲染 ----------

const kpi = (label, value, cls, hint) =>
  `<div class="kpi"><div class="label">${label}</div><div class="value ${cls}">${esc(value)}</div><div class="hint">${hint}</div></div>`;

function renderFundProgress(d) {
  $('#fund-progress').innerHTML = `
    <div class="progress-line">
      <div class="progress-head"><span>本期收入</span><b>${money(d.income)}</b></div>
      <div class="bar"><i class="income" style="width:100%"></i></div>
    </div>
    <div class="progress-line">
      <div class="progress-head"><span>已预先分配 · ${pct(d.allocation_rate)}</span><b>${money(d.allocated)}</b></div>
      <div class="bar"><i style="width:${Math.min(d.allocation_rate * 100, 100)}%"></i></div>
    </div>
    <div class="progress-line">
      <div class="progress-head"><span>预算已使用 · ${pct(d.usage_rate)}</span><b>${money(d.used)}</b></div>
      <div class="bar"><i class="${d.usage_rate > 1 ? 'warn' : 'expense'}" style="width:${Math.min(d.usage_rate * 100, 100)}%"></i></div>
    </div>
    <p class="muted" style="margin-top:14px">未分配收入 ${money(d.unallocated)} · 预算剩余 ${money(d.remaining)}</p>`;
}

function renderBreakdown(rows) {
  if (!rows || !rows.length) {
    $('#category-breakdown').innerHTML = '<div class="empty">本期还没有支出记录</div>';
    return;
  }
  const max = Math.max(...rows.map((r) => r.total_cents), 1);
  $('#category-breakdown').innerHTML = rows.slice(0, 8).map((r) => `
    <div class="bar-row">
      <span class="name" title="${esc(r.label)}">${esc(r.label)}</span>
      <div class="bar thin"><i class="expense" style="width:${(r.total_cents / max * 100).toFixed(1)}%"></i></div>
      <span class="amt">${money(r.total)}</span>
    </div>`).join('');
}

function allocationHTML(a, editable = false) {
  const cls = a.status === '超额' ? 'over' : a.status === '已用完' ? 'done' : a.status === '未动用' ? 'todo' : 'doing';
  return `<div class="alloc-item">
    <div class="alloc-head">
      <div><span class="alloc-title">${esc(a.name)}</span>
        <span class="alloc-meta">${esc(a.period)} · ${a.batch === 'first' ? '月初' : '月中'} · 预算 ${money(a.budget)}</span></div>
      <div><span class="pill ${cls}">${a.status}</span>
        ${editable ? `<button class="btn mini" data-editalloc="${a.id}">编辑</button> <button class="btn mini danger" data-delalloc="${a.id}">删除</button>` : ''}</div>
    </div>
    <div class="progress-head"><span>已花 ${money(a.spent)} · 剩余 ${money(a.remaining)}</span><b>${pct(a.progress)}</b></div>
    <div class="bar"><i class="${a.status === '超额' ? 'warn' : ''}" style="width:${Math.min(a.progress * 100, 100)}%"></i></div>
  </div>`;
}

function renderAllocations(list, target, editable = false) {
  $(target).innerHTML = list.length
    ? list.map((a) => allocationHTML(a, editable)).join('')
    : '<div class="empty">本期还没有预分配预算</div>';
}

function planHTML(p) {
  const done = p.status === 'completed';
  const allocNames = p.allocations.length
    ? p.allocations.map((a) => `<span class="tag-mini">${esc(a.name)} ${money(a.budget)}</span>`).join('')
    : '<span class="muted">未关联预算</span>';
  return `<div class="alloc-item plan-item ${done ? 'plan-done' : ''}">
    <div class="alloc-head">
      <div><span class="alloc-title">${esc(p.name)}</span>
        <span class="alloc-meta">${p.period ? esc(p.period) + ' · ' : ''}预算合计 ${money(p.budget)} · 已花 ${money(p.spent)}</span></div>
      <div><span class="pill ${done ? 'done' : 'doing'}">${done ? '已完成' : '进行中'}</span></div>
    </div>
    <div class="plan-allocs">${allocNames}</div>
    ${done && p.completed_at ? `<div class="muted" style="margin-top:6px">完成于 ${esc(p.completed_at.replace('T', ' ').slice(0, 16))}</div>` : ''}
    ${p.note ? `<div class="muted" style="margin-top:4px">${esc(p.note)}</div>` : ''}
    <div class="plan-actions">
      <button class="btn mini" data-editsplan="${p.id}">编辑</button>
      <button class="btn mini danger" data-delplan="${p.id}">删除</button>
      ${done
        ? `<button class="btn mini" data-reopenplan="${p.id}">重新打开</button>`
        : `<button class="btn mini" data-clearplan="${p.id}">标记完成</button>`}
    </div>
  </div>`;
}

function renderPlans() {
  $('#plans-list').innerHTML = state.plans.length
    ? state.plans.map(planHTML).join('')
    : '<div class="empty">还没有计划单，先在左侧建一张并关联预算</div>';
}

function entryTable(items, withActions = false) {
  if (!items || !items.length) return '<div class="empty">暂无流水</div>';
  const head = `<thead><tr><th>时间</th><th>类型</th><th>金额</th><th>成员</th><th>分类</th><th>标签</th><th>预算</th><th>备注</th><th>截图</th>${withActions ? '<th>操作</th>' : ''}</tr></thead>`;
  const body = items.map((e) => `<tr>
    <td>${esc(e.occurred_at.replace('T', ' ').slice(0, 16))}</td>
    <td><span class="pill ${e.type === 'income' ? 'done' : 'over'}">${e.type === 'income' ? '收入' : '支出'}</span></td>
    <td class="amount ${e.type}">${e.type === 'income' ? '+' : '-'}${money(e.amount)}</td>
    <td>${esc(e.member_name || '—')}</td>
    <td>${e.category_name ? esc(e.category_name) : '<span style="color:var(--warn)">未分类</span>'}</td>
    <td>${e.tags.length ? e.tags.map((t) => `<span class="tag-mini">${esc(t.name)}</span>`).join('') : '—'}</td>
    <td>${esc(e.allocation_name || '—')}</td>
    <td class="note">${esc(e.note || '')}</td>
    <td>${e.screenshot_id ? `<a class="link" href="${imageUrl(e.screenshot_id)}" target="_blank" rel="noopener">查看</a>` : '—'}</td>
    ${withActions ? `<td><button class="btn mini" data-edit="${e.id}">编辑</button> <button class="btn mini danger" data-del="${e.id}">删除</button></td>` : ''}
  </tr>`).join('');
  return `<table>${head}<tbody>${body}</tbody></table>`;
}

function renderScreenshots() {
  const el = $('#screenshot-list');
  if (!state.screenshots.length) {
    el.innerHTML = '<div class="empty">还没有上传截图</div>';
    return;
  }
  el.innerHTML = state.screenshots.map((s) => `<div class="thumb-row">
    <img src="${imageUrl(s.id)}" alt="截图">
    <span class="grow" title="${esc(s.filename)}">#${s.id} ${esc(s.filename)}</span>
    <span class="muted">${(s.size / 1024).toFixed(0)} KB</span>
    <button class="btn mini" data-useshot="${s.id}">录入</button>
    <button class="btn mini danger" data-delshot="${s.id}">删除</button>
  </div>`).join('');
}

function renderSettings() {
  $('#settings-members').innerHTML = state.meta.members.length
    ? state.meta.members.map((m) => `<div class="list-item"><span class="grow">${esc(m.name)}</span>
        <button class="btn mini" data-rename="${m.id}">改名</button>
        <button class="btn mini danger" data-delmember="${m.id}">删除</button></div>`).join('')
    : '<div class="empty">还没有成员</div>';

  const cats = state.meta.categories;
  const parents = cats.filter((c) => c.parent_id == null);
  $('#settings-categories').innerHTML = parents.length ? parents.map((p) => `
    <div class="list-item"><span class="grow"><b>${esc(p.name)}</b> <span class="muted">${esc(p.kind)}</span></span>
      <button class="btn mini danger" data-delcat="${p.id}">删除</button></div>` +
    cats.filter((c) => c.parent_id === p.id).map((c) => `
      <div class="list-item tree-child"><span class="grow">${esc(c.name)}</span>
        <button class="btn mini danger" data-delcat="${c.id}">删除</button></div>`).join('')
  ).join('') : '<div class="empty">还没有分类</div>';

  $('#settings-tags').innerHTML = state.meta.tags.length
    ? state.meta.tags.map((t) => `<span class="tag-mini" style="font-size:13px;padding:4px 10px;margin:0 6px 6px 0">${esc(t.name)}</span>`).join('')
    : '<div class="empty">还没有标签</div>';

  const stockCats = state.stockCategories || [];
  const catBox = $('#settings-stock-categories');
  if (catBox) catBox.innerHTML = stockCats.length ? stockCats.map((c) => `<div class="list-item">
      <span class="grow"><b>${esc(c.name)}</b>
        ${c.is_builtin ? '<span class="muted">内置</span>' : ''}
        <span class="muted">${c.shelf_days} 天 · ${c.item_count} 项库存</span>
        ${c.keywords ? `<br><span class="muted" style="font-size:12px">关键词：${esc(c.keywords)}</span>` : ''}
      </span>
      <button class="btn mini" data-editscat="${c.id}">编辑</button>
      <button class="btn mini danger" data-delscat="${c.id}">删除</button>
    </div>`).join('') : '<div class="empty">还没有品类</div>';
}

// ---------- 各页数据 ----------

async function refreshDashboard() {
  const d = await get('api/dashboard', { period: state.period });
  $('#dash-period-label').textContent = `${d.period} · 共 ${d.entry_count} 笔流水`;
  $('#kpi-grid').innerHTML =
    kpi('本期收入', money(d.income), 'income', '所有成员合计') +
    kpi('本期支出', money(d.expense), 'expense', '按账单发生时间') +
    kpi('本期结余', money(d.balance), '', '收入 − 支出') +
    kpi('未分类流水', `${d.uncategorized_count} 笔`, d.uncategorized_count ? 'warn' : '', '需要归类');
  renderFundProgress(d);
  renderBreakdown(d.category_breakdown);
  renderAllocations(d.allocations, '#dash-allocations');
  $('#recent-entries').innerHTML = entryTable(d.recent_entries);
}

async function loadEntries() {
  const params = {};
  for (const [k, v] of new FormData($('#filter-form')).entries()) if (v) params[k] = v;
  const d = await get('api/entries', params);
  $('#entries-summary').textContent = `共 ${d.total} 笔 · 合计 ${money(d.sum)}`;
  $('#entries-table').innerHTML = entryTable(d.items, true);
}

async function loadAlerts() {
  const d = await get('api/alerts');
  $('#alerts-summary').textContent = `待归类 ${d.count} 笔`;
  if (!d.items.length) {
    $('#alerts-table').innerHTML = '<div class="empty">全部流水都已归类</div>';
    return;
  }
  const head = '<thead><tr><th>时间</th><th>金额</th><th>成员</th><th>备注</th><th>截图</th><th>操作</th></tr></thead>';
  $('#alerts-table').innerHTML = `<table>${head}<tbody>${d.items.map((e) => `<tr>
    <td>${esc(e.occurred_at.replace('T', ' ').slice(0, 16))}</td>
    <td class="amount ${e.type}">${e.type === 'income' ? '+' : '-'}${money(e.amount)}</td>
    <td>${esc(e.member_name || '—')}</td>
    <td class="note">${esc(e.note || '')}</td>
    <td>${e.screenshot_id ? `<a class="link" href="${imageUrl(e.screenshot_id)}" target="_blank" rel="noopener">查看</a>` : '—'}</td>
    <td><button class="btn mini primary" data-classify="${e.id}">归类</button></td>
  </tr>`).join('')}</tbody></table>`;
}

async function loadReport() {
  const params = {};
  for (const [k, v] of new FormData($('#report-form')).entries()) if (v) params[k] = v;
  const d = await get('api/report', params);
  if (!d.rows.length) {
    $('#report-result').innerHTML = '<div class="empty">没有符合条件的记录</div>';
    return;
  }
  const max = Math.max(...d.rows.map((r) => r.total_cents), 1);
  const typeNote = $('#report-form select[name="type"]').value === '' ? ' · 收入与支出合并计算' : '';
  $('#report-result').innerHTML = `
    <div class="subhead">合计 ${money(d.sum)} · ${d.rows.length} 个分组${typeNote}</div>
    <div class="table-wrap"><table>
      <thead><tr><th>分组</th><th>笔数</th><th style="width:45%">占比</th><th>金额</th></tr></thead>
      <tbody>${d.rows.map((r) => `<tr>
        <td>${esc(r.label)}</td><td>${r.count}</td>
        <td><div class="bar thin"><i style="width:${(r.total_cents / max * 100).toFixed(1)}%"></i></div></td>
        <td class="amount">${money(r.total)}</td>
      </tr>`).join('')}</tbody>
    </table></div>`;
}

async function refreshAlertBadge() {
  const d = await get('api/entries', { uncategorized: 1, limit: 1 });
  const el = $('#alert-badge');
  el.textContent = d.total;
  el.hidden = d.total === 0;
}

async function refreshTab(tab) {
  switch (tab) {
    case 'dashboard': await refreshDashboard(); break;
    case 'record':
      state.screenshots = await get('api/screenshots');
      fillSelects(); renderScreenshots(); break;
    case 'entries': await loadEntries(); break;
    case 'alerts': await loadAlerts(); break;
    case 'budgets':
      state.allocations = await get('api/allocations');
      fillSelects();
      renderAllocations(state.allocations.filter((a) => a.period === state.period), '#allocations-list', true);
      break;
    case 'plans':
      state.allocations = await get('api/allocations');
      state.plans = await get('api/plans');
      fillSelects(); renderPlans(); refreshPlanBadge();
      break;
    case 'report': await loadReport(); break;
    case 'stock': await loadStock(); break;
    case 'settings': renderSettings(); break;
  }
  await refreshAlertBadge();
}

async function switchTab(tab) {
  state.tab = tab;
  $$('#nav button').forEach((b) => b.classList.toggle('active', b.dataset.tab === tab));
  $$('.tab').forEach((s) => s.classList.toggle('active', s.id === `tab-${tab}`));
  await guard(() => refreshTab(tab));
}

async function reloadCurrent() {
  await guard(async () => {
    await refreshBase();
    await refreshTab(state.tab);
  });
}

// ---------- 弹窗 ----------

function openModal(html) {
  $('#modal-body').innerHTML = html;
  $('#modal').classList.remove('hidden');
}
function closeModal() {
  $('#modal').classList.add('hidden');
  $('#modal-body').innerHTML = '';
}

function entryFormHTML(e) {
  return `<form id="edit-form" autocomplete="off">
    <div class="type-switch">
      <label><input type="radio" name="type" value="expense"> 支出</label>
      <label><input type="radio" name="type" value="income"> 收入</label>
    </div>
    <label>金额（元）<input name="amount" type="text" inputmode="decimal" required></label>
    <label>发生时间<input name="occurred_at" type="datetime-local"></label>
    <div class="row">
      <label>成员<select name="member_id"></select></label>
      <label>分类<select name="category_id"></select></label>
    </div>
    <div class="row">
      <label>关联预算<select name="allocation_id"></select></label>
      <label>关联截图<select name="screenshot_id"></select></label>
    </div>
    <fieldset><legend>标签（可多选）</legend><div id="edit-tag-box" class="tag-box"></div></fieldset>
    <label>备注<input name="note" type="text"></label>
    <div class="modal-actions"><button type="button" class="btn" id="modal-cancel">取消</button><button type="submit" class="btn primary">保存</button></div>
  </form>`;
}

async function openEdit(id) {
  const e = await get(`api/entries/${id}`);
  openModal(`<h3>编辑流水 #${e.id}</h3>${entryFormHTML(e)}`);
  const f = $('#edit-form');
  $(`input[name="type"][value="${e.type}"]`, f).checked = true;
  f.elements['amount'].value = e.amount;
  f.elements['occurred_at'].value = e.occurred_at.slice(0, 16);
  f.elements['member_id'].innerHTML = '<option value="">未指定</option>' +
    state.meta.members.map((m) => `<option value="${m.id}">${esc(m.name)}</option>`).join('');
  f.elements['category_id'].innerHTML = categoryOptions('未分类（进报警表）');
  f.elements['allocation_id'].innerHTML = '<option value="">不关联预算</option>' +
    state.allocations.map((a) => `<option value="${a.id}">${esc(a.period)} · ${esc(a.name)}</option>`).join('');
  f.elements['screenshot_id'].innerHTML = '<option value="">不关联截图</option>' +
    state.screenshots.map((s) => `<option value="${s.id}">#${s.id} ${esc(s.filename)}</option>`).join('');
  f.elements['member_id'].value = e.member_id ?? '';
  f.elements['category_id'].value = e.category_id ?? '';
  f.elements['allocation_id'].value = e.allocation_id ?? '';
  f.elements['screenshot_id'].value = e.screenshot_id ?? '';
  f.elements['note'].value = e.note || '';
  $('#edit-tag-box').innerHTML = tagChips(e.tags.map((t) => t.id));
  $('#modal-cancel').addEventListener('click', closeModal);
  f.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    await guard(async () => {
      await put(`api/entries/${id}`, {
        type: $(`input[name="type"]:checked`, f).value,
        amount: formVal(f, 'amount'),
        occurred_at: formVal(f, 'occurred_at') || localDateTime(),
        member_id: formNum(f, 'member_id'),
        category_id: formNum(f, 'category_id'),
        allocation_id: formNum(f, 'allocation_id'),
        screenshot_id: formNum(f, 'screenshot_id'),
        source: e.source,
        note: formVal(f, 'note'),
        tag_ids: checkedTags($('#edit-tag-box')),
      });
      closeModal();
      toast('流水已更新');
      await reloadCurrent();
    });
  });
}

async function openClassify(id) {
  const e = await get(`api/entries/${id}`);
  openModal(`<h3>归类流水 #${e.id}</h3>
    <p class="muted">${e.type === 'income' ? '收入' : '支出'} ${money(e.amount)} · ${esc(e.occurred_at.replace('T', ' ').slice(0, 16))}</p>
    <form id="classify-form">
      <label>分类<select name="category_id"></select></label>
      <fieldset><legend>标签（可多选）</legend><div id="classify-tag-box" class="tag-box"></div></fieldset>
      <div class="modal-actions"><button type="button" class="btn" id="modal-cancel">取消</button><button type="submit" class="btn primary">保存归类</button></div>
    </form>`);
  const f = $('#classify-form');
  f.elements['category_id'].innerHTML = categoryOptions('未分类（进报警表）');
  f.elements['category_id'].value = e.category_id ?? '';
  $('#classify-tag-box').innerHTML = tagChips(e.tags.map((t) => t.id));
  $('#modal-cancel').addEventListener('click', closeModal);
  f.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    await guard(async () => {
      await patch(`api/entries/${id}/classify`, {
        category_id: formNum(f, 'category_id'),
        tag_ids: checkedTags($('#classify-tag-box')),
      });
      closeModal();
      toast('归类完成');
      await reloadCurrent();
    });
  });
}

async function openEditAllocation(id) {
  const a = state.allocations.find((x) => x.id === id);
  if (!a) return;
  openModal(`<h3>编辑预算 #${a.id}</h3>
    <form id="edit-alloc-form">
      <div class="row">
        <label>月份<input name="period" type="month" value="${esc(a.period)}" required></label>
        <label>批次<select name="batch"><option value="first">月初</option><option value="mid">月中</option></select></label>
      </div>
      <label>名称<input name="name" value="${esc(a.name)}" required></label>
      <label>金额（元）<input name="budget" value="${esc(a.budget)}" required></label>
      <label>建议分类<select name="category_id"></select></label>
      <label>备注<input name="note" value="${esc(a.note)}"></label>
      <div class="modal-actions"><button type="button" class="btn" id="modal-cancel">取消</button><button type="submit" class="btn primary">保存</button></div>
    </form>`);
  const f = $('#edit-alloc-form');
  f.elements['batch'].value = a.batch;
  f.elements['category_id'].innerHTML = categoryOptions('不指定分类');
  f.elements['category_id'].value = a.category_id ?? '';
  $('#modal-cancel').addEventListener('click', closeModal);
  f.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    await guard(async () => {
      await put(`api/allocations/${id}`, {
        period: formVal(f, 'period'), batch: formVal(f, 'batch'),
        name: formVal(f, 'name'), budget: formVal(f, 'budget'),
        category_id: formNum(f, 'category_id'), note: formVal(f, 'note'),
      });
      closeModal();
      toast('预算已更新');
      await reloadCurrent();
    });
  });
}

function openEditPlan(id) {
  const p = state.plans.find((x) => x.id === id);
  if (!p) return;
  const selected = p.allocations.map((a) => a.id);
  openModal(`<h3>编辑计划单 #${p.id}</h3>
    <form id="edit-plan-form">
      <label>名称<input name="name" value="${esc(p.name)}" required></label>
      <label>月份（可留空）<input name="period" type="month" value="${esc(p.period)}"></label>
      <fieldset><legend>关联预算（可多选）</legend>
        <div class="scroll-list">${state.allocations.length
          ? state.allocations.map((a) => `<label class="chip"><input type="checkbox" value="${a.id}"${selected.includes(a.id) ? ' checked' : ''}>${esc(a.period)} ${a.batch === 'first' ? '月初' : '月中'} · ${esc(a.name)}</label>`).join('')
          : '<span class="muted">还没有预算</span>'}</div>
      </fieldset>
      <label>备注<input name="note" value="${esc(p.note)}"></label>
      <div class="modal-actions"><button type="button" class="btn" id="modal-cancel">取消</button><button type="submit" class="btn primary">保存</button></div>
    </form>`);
  const f = $('#edit-plan-form');
  $('#modal-cancel').addEventListener('click', closeModal);
  f.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    await guard(async () => {
      await put(`api/plans/${id}`, {
        name: formVal(f, 'name'), period: formVal(f, 'period'), note: formVal(f, 'note'),
        allocation_ids: $$('input[type="checkbox"]:checked', f).map((i) => Number(i.value)),
      });
      closeModal();
      toast('计划单已更新');
      await reloadCurrent();
    });
  });
}

// ---------- 表单动作 ----------

function resetEntryDefaults() {
  const f = $('#entry-form');
  f.elements['occurred_at'].value = localDateTime();
  if (state.meta.members.length) f.elements['member_id'].value = String(state.meta.members[0].id);
  f.elements['screenshot_id'].value = '';
}

async function handleFile(file) {
  if (!file) return;
  await guard(async () => {
    const s = await upload('api/screenshots', file);
    $('#upload-preview').innerHTML = `<img src="${imageUrl(s.id)}" alt="上传预览">`;
    state.screenshots = await get('api/screenshots');
    fillSelects();
    renderScreenshots();
    $('#entry-form select[name="screenshot_id"]').value = String(s.id);
    toast(`截图已保存，编号 #${s.id}，开始自动识别`);
    await runRecognition({ screenshot_id: s.id });
  });
}

function bindEvents() {
  $('#nav').addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-tab]');
    if (btn) switchTab(btn.dataset.tab);
  });

  $('#global-period').addEventListener('change', async (e) => {
    state.period = e.target.value || currentMonth();
    $('#allocation-form input[name="period"]').value = state.period;
    await switchTab(state.tab);
  });

  // 记一笔
  const entryForm = $('#entry-form');
  entryForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.currentTarget;
    await guard(async () => {
      // 只有 OCR 识别出的账单才显式要求重建自动入库项；
      // 手动记账 sync_stock=false，后端完全不碰库存，不再依赖"字段传没传"的隐式语义。
      const stockItems = state.recognition ? collectStockItems() : [];
      await post('api/entries', {
        type: $(`input[name="type"]:checked`, f).value,
        amount: formVal(f, 'amount'),
        occurred_at: formVal(f, 'occurred_at') || localDateTime(),
        member_id: formNum(f, 'member_id'),
        category_id: formNum(f, 'category_id'),
        allocation_id: formNum(f, 'allocation_id'),
        screenshot_id: formNum(f, 'screenshot_id'),
        source: formVal(f, 'screenshot_id') ? 'screenshot' : 'manual',
        note: formVal(f, 'note'),
        tag_ids: checkedTags($('#entry-tag-box')),
        sync_stock: !!state.recognition,
        stock_items: stockItems,
      });
      const stockCount = stockItems.length;
      toast(stockCount ? `流水已保存，${stockCount} 项家庭用品已自动入库` : '流水已保存');
      f.reset();
      resetEntryDefaults();
      $('#entry-tag-box').innerHTML = tagChips();
      $('#upload-preview').innerHTML = '';
      clearRecognition();
      await refreshBase();
      await switchTab('dashboard');
    });
  });
  entryForm.addEventListener('reset', () => setTimeout(resetEntryDefaults, 0));

  const dz = $('#dropzone');
  const fileInput = $('#screenshot-file');
  dz.addEventListener('click', () => fileInput.click());
  fileInput.addEventListener('change', () => { handleFile(fileInput.files[0]); fileInput.value = ''; });
  ['dragenter', 'dragover'].forEach((ev) => dz.addEventListener(ev, (e) => { e.preventDefault(); dz.classList.add('dragover'); }));
  ['dragleave', 'drop'].forEach((ev) => dz.addEventListener(ev, (e) => { e.preventDefault(); dz.classList.remove('dragover'); }));
  dz.addEventListener('drop', (e) => handleFile(e.dataTransfer.files[0]));

  // 筛选与统计
  $('#filter-form').addEventListener('submit', (e) => { e.preventDefault(); guard(loadEntries); });
  $('#filter-form').addEventListener('reset', () => setTimeout(() => guard(loadEntries), 0));
  $('#report-form').addEventListener('submit', (e) => { e.preventDefault(); guard(loadReport); });

  // 预算
  $('#allocation-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.currentTarget;
    await guard(async () => {
      await post('api/allocations', {
        period: formVal(f, 'period'), batch: formVal(f, 'batch'),
        name: formVal(f, 'name'), budget: formVal(f, 'budget'),
        category_id: formNum(f, 'category_id'), note: formVal(f, 'note'),
      });
      toast('预算已保存');
      f.reset();
      f.elements['period'].value = state.period;
      await reloadCurrent();
    });
  });

  // 计划单
  $('#plan-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.currentTarget;
    await guard(async () => {
      await post('api/plans', {
        name: formVal(f, 'name'), period: formVal(f, 'period'), note: formVal(f, 'note'),
        allocation_ids: $$('input[type="checkbox"]:checked', $('#plan-alloc-box')).map((i) => Number(i.value)),
      });
      toast('计划单已保存');
      f.reset();
      await reloadCurrent();
    });
  });

  // 基础数据
  $('#member-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.currentTarget;
    await guard(async () => { await post('api/members', { name: formVal(f, 'name') }); f.reset(); toast('成员已添加'); await reloadCurrent(); });
  });
  $('#category-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.currentTarget;
    await guard(async () => {
      await post('api/categories', {
        name: formVal(f, 'name'), parent_id: formNum(f, 'parent_id'), kind: formVal(f, 'kind'),
      });
      f.reset(); toast('分类已添加'); await reloadCurrent();
    });
  });
  $('#tag-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.currentTarget;
    await guard(async () => { await post('api/tags', { name: formVal(f, 'name') }); f.reset(); toast('标签已添加'); await reloadCurrent(); });
  });
  $('#stock-category-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.currentTarget;
    await guard(async () => {
      await post('api/stock-categories', {
        name: formVal(f, 'name'),
        keywords: formVal(f, 'keywords'),
        shelf_days: Number(formVal(f, 'shelf_days')) || 365,
      });
      f.reset();
      f.elements['shelf_days'].value = 365;
      toast('库存品类已添加');
      await reloadCurrent();
    });
  });

  // OCR 兜底：粘贴文本解析
  $('#parse-text-btn').addEventListener('click', () => {
    const text = $('#ocr-text').value.trim();
    if (!text) {
      toast('请先粘贴账单文字', true);
      return;
    }
    const sid = Number($('#entry-form select[name="screenshot_id"]').value || 0);
    guard(() => runRecognition({ text, screenshot_id: sid }, true));
  });

  // 库存
  $('#stock-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.currentTarget;
    await guard(async () => {
      await post('api/stock/items', {
        name: formVal(f, 'name'),
        qty: Number(formVal(f, 'qty')) || 1,
        unit: formVal(f, 'unit') || '件',
        amount: formVal(f, 'amount') || '0',
        category_key: formVal(f, 'category_key'),
        expiry_date: formVal(f, 'expiry_date'),
        note: formVal(f, 'note'),
      });
      f.reset();
      f.elements['qty'].value = '1';
      f.elements['unit'].value = '件';
      toast('已入库');
      await loadStock();
    });
  });
  $('#stock-filter').addEventListener('submit', (e) => { e.preventDefault(); guard(loadStockItems); });
  $('#stock-filter').addEventListener('reset', () => setTimeout(() => guard(loadStockItems), 0));

  // 全局点击委托
  document.addEventListener('click', async (e) => {
    const t = e.target.closest('[data-edit], [data-del], [data-classify], [data-editalloc], [data-delalloc], [data-useshot], [data-delshot], [data-rename], [data-delmember], [data-delcat], [data-editscat], [data-delscat], [data-editstock], [data-delstock], [data-consume], [data-waste], [data-dropitem]');
    if (!t) return;
    const d = t.dataset;
    if (d.dropitem !== undefined) {
      t.closest('tr').remove();
      return;
    }
    if (d.edit) return guard(() => openEdit(Number(d.edit)));
    if (d.classify) return guard(() => openClassify(Number(d.classify)));
    if (d.editalloc) return guard(() => openEditAllocation(Number(d.editalloc)));
    if (d.editsplan) return guard(() => openEditPlan(Number(d.editsplan)));
    if (d.reopenplan) return guard(async () => {
      await post(`api/plans/${d.reopenplan}/reopen`);
      toast('计划单已重新打开');
      await reloadCurrent();
    });
    if (d.clearplan) return guard(async () => {
      await post(`api/plans/${d.clearplan}/clear`);
      toast('计划单已标记完成');
      await reloadCurrent();
    });
    if (d.useshot) {
      const sel = $('#entry-form select[name="screenshot_id"]');
      sel.value = d.useshot;
      $('#upload-preview').innerHTML = `<img src="${imageUrl(d.useshot)}" alt="截图预览">`;
      return switchTab('record');
    }
    if (d.rename) {
      const m = state.meta.members.find((x) => x.id === Number(d.rename));
      const name = prompt('修改成员姓名', m?.name || '');
      if (!name || !name.trim()) return;
      return guard(async () => { await patch(`api/members/${d.rename}`, { name: name.trim() }); toast('已更新'); await reloadCurrent(); });
    }
    if (d.editscat) return guard(() => openEditStockCategory(Number(d.editscat)));
    if (d.editstock) return guard(() => openEditStockItem(Number(d.editstock)));
    if (d.consume || d.waste) {
      const isWaste = !!d.waste;
      const id = Number(d.consume || d.waste);
      const raw = prompt(isWaste ? '损耗数量（留空表示全部损耗）' : '消耗数量（留空表示全部用完）', '');
      if (raw === null) return;
      const body = raw.trim() ? { qty: Number(raw) } : {};
      return guard(async () => {
        await post(`api/stock/items/${id}/${isWaste ? 'waste' : 'consume'}`, body);
        toast(isWaste ? '已登记损耗' : '已登记消耗');
        await loadStock();
      });
    }
    const actions = [
      ['del', `确定删除流水 #${d.del}？`, `api/entries/${d.del}`, '流水已删除'],
      ['delstock', '确定删除该库存项？出入库记录会一并删除。', `api/stock/items/${d.delstock}`, '库存项已删除'],
      ['delalloc', `确定删除预算 #${d.delalloc}？关联流水会解除预算。`, `api/allocations/${d.delalloc}`, '预算已删除'],
      ['delplan', `确定删除计划单 #${d.delplan}？关联预算和流水都会保留。`, `api/plans/${d.delplan}`, '计划单已删除'],
      ['delshot', `确定删除截图 #${d.delshot}？`, `api/screenshots/${d.delshot}`, '截图已删除'],
      ['delmember', `确定删除该成员？`, `api/members/${d.delmember}`, '成员已删除'],
      ['delcat', `确定删除该分类？`, `api/categories/${d.delcat}`, '分类已删除'],
      ['delscat', `确定删除该库存品类？若还有库存项使用它，将拒绝删除并提示先改到其他品类。`, `api/stock-categories/${d.delscat}`, '库存品类已删除'],
    ];
    for (const [key, msg, url, okMsg] of actions) {
      if (d[key]) {
        if (!confirm(msg)) return;
        return guard(async () => { await del(url); toast(okMsg); await reloadCurrent(); });
      }
    }
  });

  $('#modal').addEventListener('click', (e) => { if (e.target.id === 'modal') closeModal(); });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeModal(); });
}

// ---------- OCR 识别复核 ----------

async function loadOCRStatus() {
  const s = await get('api/ocr/status');
  state.ocrAvailable = s.available;
  const el = $('#ocr-status');
  el.className = `ocr-status ${s.available ? 'ok' : 'warn'}`;
  el.textContent = s.available
    ? `本地 OCR 已就绪：${s.engine} · 离线运行，截图不会上传到外部`
    : `OCR 未就绪：${s.hint}`;
}

async function runRecognition(payload, isText = false) {
  const progress = $('#ocr-progress');
  progress.classList.remove('hidden');
  progress.textContent = isText ? '正在解析账单文本…' : '正在本地识别账单文字…（首次加载模型可能需要几秒）';
  try {
    const res = await post(isText ? 'api/ocr/parse-text' : 'api/ocr/recognize', payload);
    state.recognition = res;
    applyRecognition(res);
    if (res.error) toast(res.error, true);
    else toast(`识别完成：${res.items.length} 项家庭用品待确认`);
  } finally {
    progress.classList.add('hidden');
  }
}

function applyRecognition(res) {
  const s = res.suggestion || {};
  $('#recognition-card').classList.remove('hidden');
  const lineCount = (res.lines || []).length;
  $('#recognition-meta').textContent = `引擎 ${res.engine} · 识别 ${lineCount} 行 · 置信度 ${Math.round((res.confidence || 0) * 100)}%`;
  $('#recognition-raw').textContent = res.raw_text || '（无文本）';

  const conf = s.field_confidence || {};
  const mark = (k) => (conf[k] !== undefined && conf[k] < 0.6) ? ' <span class="conf-low">请复核</span>' : '';
  $('#recognition-fields').innerHTML = `<div class="field-list">
    <div class="fl"><span>类型</span><b>${s.type === 'income' ? '收入' : '支出'}${mark('type')}</b></div>
    <div class="fl"><span>金额</span><b>${s.amount ? money(s.amount) : '未识别'}${mark('amount')}</b></div>
    <div class="fl"><span>发生时间</span><b>${esc(s.occurred_at || '')}${mark('occurred_at')}</b></div>
    <div class="fl"><span>建议分类</span><b>${esc(s.category_name || '未指定')}${mark('category')}</b></div>
    <div class="fl"><span>商户 / 备注</span><b>${esc(s.merchant || '—')}</b></div>
  </div>`;
  $('#recognition-warnings').innerHTML = (res.warnings || []).length
    ? `<div class="warn-box">${res.warnings.map((w) => `<div>· ${esc(w)}</div>`).join('')}</div>`
    : '';

  // 自动填入录入表单
  const f = $('#entry-form');
  const radio = $(`input[name="type"][value="${s.type || 'expense'}"]`, f);
  if (radio) radio.checked = true;
  f.elements['amount'].value = s.amount || '';
  f.elements['occurred_at'].value = (s.occurred_at || '').slice(0, 16) || localDateTime();
  if (s.category_id != null) f.elements['category_id'].value = String(s.category_id);
  f.elements['note'].value = s.note || '';
  $('#entry-tag-box').innerHTML = tagChips(s.tag_ids || []);
  if (res.screenshot_id) {
    const sel = f.elements['screenshot_id'];
    if (!Array.from(sel.options).some((o) => o.value === String(res.screenshot_id))) {
      sel.innerHTML += `<option value="${res.screenshot_id}">#${res.screenshot_id} 本次识别截图</option>`;
    }
    sel.value = String(res.screenshot_id);
  }
  renderRecognitionItems(res.items || []);
}

function renderRecognitionItems(items) {
  if (!items.length) {
    $('#recognition-items').innerHTML = '<div class="empty">没有识别到家庭用品，本次不会自动入库</div>';
    return;
  }
  const rules = state.meta.stock_rules || [];
  const opts = (sel) => rules.map((r) =>
    `<option value="${esc(r.key)}"${r.key === sel ? ' selected' : ''}>${esc(r.name)}</option>`).join('') +
    `<option value="other"${sel === 'other' ? ' selected' : ''}>其他</option>`;
  $('#recognition-items').innerHTML = `<table class="item-table">
    <thead><tr><th>入库</th><th>品名</th><th>数量</th><th>单位</th><th>金额</th><th>品类</th><th>保质期至</th><th></th></tr></thead>
    <tbody>${items.map((it, i) => `<tr>
      <td><input type="checkbox" data-include checked></td>
      <td><input data-name value="${esc(it.name)}"></td>
      <td><input data-qty type="number" step="0.01" value="${it.qty}"></td>
      <td><input data-unit value="${esc(it.unit)}"></td>
      <td><input data-amount value="${esc(it.amount)}"></td>
      <td><select data-cat>${opts(it.category_key)}</select></td>
      <td><input data-expiry type="date" value="${esc(it.expiry_date)}"></td>
      <td><button type="button" class="btn mini danger" data-dropitem="${i}">移除</button></td>
    </tr>`).join('')}</tbody></table>`;
}

function collectStockItems() {
  const rules = state.meta.stock_rules || [];
  return $$('#recognition-items tbody tr').filter((r) => {
    const box = $('[data-include]', r);
    return box && box.checked;
  }).map((r) => {
    const key = $('[data-cat]', r).value;
    const rule = rules.find((x) => x.key === key);
    return {
      name: $('[data-name]', r).value.trim(),
      qty: Number($('[data-qty]', r).value) || 1,
      unit: $('[data-unit]', r).value.trim() || '件',
      amount: $('[data-amount]', r).value.trim() || '0',
      category_key: key,
      category_name: rule ? rule.name : '其他',
      expiry_date: $('[data-expiry]', r).value,
    };
  }).filter((x) => x.name);
}

function clearRecognition() {
  state.recognition = null;
  $('#recognition-card').classList.add('hidden');
  $('#recognition-items').innerHTML = '';
  $('#recognition-warnings').innerHTML = '';
  $('#ocr-text').value = '';
}

// ---------- 库存 ----------

const stockStatusLabel = { in_stock: '在库', used_up: '已用完', wasted: '已损耗' };

async function loadStock() {
  const [summary, movements] = await Promise.all([
    get('api/stock/summary'), get('api/stock/movements'),
  ]);
  renderStockSummary(summary);
  renderStockMovements(movements);
  await loadStockItems();
}

async function loadStockItems() {
  const params = {};
  for (const [k, v] of new FormData($('#stock-filter')).entries()) if (v) params[k] = v;
  const items = await get('api/stock/items', params);
  renderStockItems(items);
}

function renderStockSummary(d) {
  $('#stock-summary-label').textContent = `在库 ${d.total_items} 项 · 合计 ${money(d.total_amount)}`;
  const expired = d.expired || 0, soon = d.expiring_soon || 0;
  $('#stock-alerts').innerHTML = (expired || soon)
    ? `<div class="stock-alert${expired ? '' : ' soon'}"><b>过期 / 临期通报</b>：${expired} 项已过期，${soon} 项将在 3 天内到期。
        ${(d.alerts || []).slice(0, 6).map((a) => esc(a.name) + (a.expiry_status === 'expired' ? '（已过期）' : `（剩 ${a.days_left} 天）`)).join('、')}</div>`
    : '<div class="ocr-status ok">暂无过期或临期物品</div>';
  $('#stock-categories').innerHTML = (d.categories || []).length
    ? d.categories.map((c) => `<div class="list-item">
        <span class="grow"><b>${esc(c.name)}</b> <span class="muted">${c.item_count} 项 · ${c.total_qty} 单位</span></span>
        <span>${money(c.amount)}</span>
        ${c.expired ? `<span class="tag-status expired">${c.expired} 过期</span>` : ''}
        ${c.expiring_soon ? `<span class="tag-status soon">${c.expiring_soon} 临期</span>` : ''}
      </div>`).join('')
    : '<div class="empty">库存为空，上传一张超市账单即可自动入库</div>';
}

function renderStockItems(items) {
  if (!items.length) {
    $('#stock-items').innerHTML = '<div class="empty">没有符合条件的库存项</div>';
    return;
  }
  $('#stock-items').innerHTML = `<table>
    <thead><tr><th>物品</th><th>品类</th><th>数量</th><th>金额</th><th>保质期</th><th>状态</th><th>来源</th><th>备注</th><th>操作</th></tr></thead>
    <tbody>${items.map((s) => `<tr>
      <td><b>${esc(s.name)}</b></td>
      <td>${esc(s.category_name)}</td>
      <td>${s.qty} ${esc(s.unit)}</td>
      <td>${money(s.amount)}</td>
      <td>${esc(s.expiry_date || '—')} ${s.expiry_status === 'expired' ? '<span class="tag-status expired">已过期</span>' : s.expiry_status === 'soon' ? `<span class="tag-status soon">剩 ${s.days_left} 天</span>` : ''}</td>
      <td><span class="tag-status ${s.status === 'in_stock' ? '' : 'used'}">${stockStatusLabel[s.status] || s.status}</span></td>
      <td>${s.auto_created ? `账单 #${s.entry_id}` : '手工录入'}</td>
      <td class="note">${esc(s.note || '')}</td>
      <td>
        <button class="btn mini" data-consume="${s.id}">消耗</button>
        <button class="btn mini" data-waste="${s.id}">损耗</button>
        <button class="btn mini" data-editstock="${s.id}">编辑</button>
        <button class="btn mini danger" data-delstock="${s.id}">删除</button>
      </td>
    </tr>`).join('')}</tbody></table>`;
}

function renderStockMovements(list) {
  if (!list.length) {
    $('#stock-movements').innerHTML = '<div class="empty">暂无出入库记录</div>';
    return;
  }
  const kindLabel = { in: '入库', consume: '消耗', waste: '损耗' };
  $('#stock-movements').innerHTML = `<table>
    <thead><tr><th>时间</th><th>物品</th><th>类型</th><th>数量</th><th>备注</th></tr></thead>
    <tbody>${list.slice(0, 30).map((m) => `<tr>
      <td>${esc(m.created_at.replace('T', ' ').slice(0, 16))}</td>
      <td>${esc(m.item_name)}</td><td>${kindLabel[m.kind] || m.kind}</td>
      <td>${m.qty}</td><td class="note">${esc(m.note)}</td>
    </tr>`).join('')}</tbody></table>`;
}

function openEditStockCategory(id) {
  const c = (state.stockCategories || []).find((x) => x.id === id);
  if (!c) { toast('品类不存在', true); return; }
  openModal(`<h3>编辑品类 · ${esc(c.name)}</h3>
    <form id="edit-stock-category-form">
      <label>品类名称<input name="name" value="${esc(c.name)}" required></label>
      <label>识别关键词（逗号分隔，命中即自动归入此品类）<input name="keywords" value="${esc(c.keywords || '')}" placeholder="例如：猫抓板, 猫玩具"></label>
      <label>默认保质期天数<input name="shelf_days" type="number" min="1" max="3650" value="${c.shelf_days}"></label>
      ${c.is_builtin ? '<p class="muted">内置品类可修改，改名会同步更新已有库存项的显示名。</p>' : ''}
      <div class="actions">
        <button type="submit" class="btn primary">保存</button>
        <button type="button" class="btn" id="modal-cancel">取消</button>
      </div>
    </form>`);
  $('#modal-cancel').addEventListener('click', closeModal);
  $('#edit-stock-category-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.currentTarget;
    await guard(async () => {
      await put(`api/stock-categories/${id}`, {
        name: formVal(f, 'name'),
        keywords: formVal(f, 'keywords'),
        shelf_days: Number(formVal(f, 'shelf_days')) || 365,
      });
      closeModal();
      toast('品类已更新');
      await reloadCurrent();
    });
  });
}

async function openEditStockItem(id) {
  const items = await get('api/stock/items');
  const s = items.find((x) => x.id === id);
  if (!s) {
    toast('库存项不存在', true);
    return;
  }
  // 品类可改：之前编辑弹窗没有这个字段，品类选错了只能删了重建
  const rules = state.meta.stock_rules || [];
  const catOpts = rules
    .map((r) => `<option value="${esc(r.key)}"${s.category_key === r.key ? ' selected' : ''}>${esc(r.name)}</option>`)
    .join('') + `<option value="other"${s.category_key === 'other' || !s.category_key ? ' selected' : ''}>其他</option>`;
  openModal(`<h3>编辑库存 · ${esc(s.name)}</h3>
    <form id="edit-stock-form">
      <label>名称<input name="name" value="${esc(s.name)}" required></label>
      <div class="row">
        <label>数量<input name="qty" type="number" step="0.01" value="${s.qty}"></label>
        <label>单位<input name="unit" value="${esc(s.unit)}"></label>
      </div>
      <div class="row">
        <label>品类<select name="category_key">${catOpts}</select></label>
        <label>金额（元）<input name="amount" value="${esc(s.amount)}"></label>
      </div>
      <label>保质期至<input name="expiry_date" type="date" value="${esc(s.expiry_date)}"></label>
      <label>状态<select name="status">
        <option value="in_stock">在库</option><option value="used_up">已用完</option><option value="wasted">已损耗</option>
      </select></label>
      <label>备注<input name="note" value="${esc(s.note)}"></label>
      <div class="modal-actions"><button type="button" class="btn" id="modal-cancel">取消</button><button type="submit" class="btn primary">保存</button></div>
    </form>`);
  const f = $('#edit-stock-form');
  f.elements['status'].value = s.status;
  $('#modal-cancel').addEventListener('click', closeModal);
  f.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    await guard(async () => {
      await put(`api/stock/items/${id}`, {
        name: formVal(f, 'name'), qty: Number(formVal(f, 'qty')),
        unit: formVal(f, 'unit'), amount: formVal(f, 'amount'),
        category_key: formVal(f, 'category_key'),
        expiry_date: formVal(f, 'expiry_date'), status: formVal(f, 'status'),
        note: formVal(f, 'note'),
      });
      closeModal();
      toast('库存已更新');
      await loadStock();
    });
  });
}

// ---------- 启动 ----------

async function init() {
  $('#global-period').value = state.period;
  $('#allocation-form input[name="period"]').value = state.period;
  const now = localDateTime();
  $('#filter-form input[name="from"]').value = `${state.period}-01`;
  $('#report-form input[name="from"]').value = `${state.period}-01`;
  $('#report-form select[name="type"]').value = 'expense';
  $('#entry-form input[name="occurred_at"]').value = now;
  await guard(async () => {
    await refreshBase();
    bindEvents();
    await loadOCRStatus();
    await switchTab('dashboard');
  });
}

init();
