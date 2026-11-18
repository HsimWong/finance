const clean = (params) => {
  const out = {};
  for (const [k, v] of Object.entries(params || {})) {
    if (v === undefined || v === null || v === '' || v === false) continue;
    out[k] = v;
  }
  return out;
};

async function request(path, options = {}) {
  const res = await fetch(path, options);
  let json;
  try {
    json = await res.json();
  } catch {
    throw new Error('服务端返回的不是有效 JSON');
  }
  if (!res.ok || !json.ok) throw new Error(json.error || `请求失败 (${res.status})`);
  return json.data;
}

const jsonHeaders = { 'Content-Type': 'application/json' };

export function get(path, params) {
  const q = new URLSearchParams(clean(params)).toString();
  return request(q ? `${path}?${q}` : path);
}
export const post = (path, body) => request(path, { method: 'POST', headers: jsonHeaders, body: JSON.stringify(body) });
export const put = (path, body) => request(path, { method: 'PUT', headers: jsonHeaders, body: JSON.stringify(body) });
export const patch = (path, body) => request(path, { method: 'PATCH', headers: jsonHeaders, body: JSON.stringify(body) });
export const del = (path) => request(path, { method: 'DELETE' });

export function upload(path, file) {
  const fd = new FormData();
  fd.append('file', file);
  return request(path, { method: 'POST', body: fd });
}

export const imageUrl = (id) => `api/screenshots/${id}/image`;

export function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
}

export function localDateTime(d = new Date()) {
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function currentMonth() {
  return localDateTime().slice(0, 7);
}
