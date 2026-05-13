// Tiny service worker: cache-first for the static assets, network-first
// for everything else. Bump CACHE_NAME when shipping a new build so
// clients evict the stale wasm blob.
const CACHE_NAME = 'chippy-v3'; // bumped: P-register pane alignment fix
const ASSETS = [
  './',
  './index.html',
  './style.css',
  './chippy.js',
  './wasm_exec.js',
  './chippy.wasm',
];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE_NAME).then((c) => c.addAll(ASSETS)));
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))),
    ),
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  event.respondWith(
    caches.match(req).then((cached) => {
      if (cached) return cached;
      return fetch(req)
        .then((resp) => {
          // Only cache same-origin static responses.
          if (resp.ok && new URL(req.url).origin === self.location.origin) {
            const clone = resp.clone();
            caches.open(CACHE_NAME).then((c) => c.put(req, clone));
          }
          return resp;
        })
        .catch(() => cached); // last-resort cached fallback
    }),
  );
});
