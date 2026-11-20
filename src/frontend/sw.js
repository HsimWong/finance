// 家庭账本 PWA · Service Worker
// 策略：app shell 预缓存（离线可开），API 一律走网络（账单实时、不缓存敏感数据）。
// 注意：本项目挂在 /finance/ 子路径下，所有路径必须相对，不能用 / 开头。

const CACHE = 'finance-mobile-v2';
const SHELL = [
  './mobile.html',
  './css/mobile.css',
  './js/mobile.js',
  './js/api.js',
  './manifest.json',
  './icons/icon-192.png',
  './icons/apple-touch-icon.png',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

// 只接管同源 GET 请求；API 直接放行，其余静态资源走 cache-first、后台更新。
self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // API（相对路径下形如 .../api/xxx）不缓存，保证账单实时且不落敏感数据
  if (/\/api\//.test(url.pathname)) return;

  event.respondWith(
    caches.match(req).then((hit) => {
      const fresh = fetch(req).then((res) => {
        if (res && res.ok) {
          const copy = res.clone();
          caches.open(CACHE).then((c) => c.put(req, copy));
        }
        return res;
      }).catch(() => hit);
      return hit || fresh;
    })
  );
});
