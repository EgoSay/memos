/* Offline shell only. Journal content and authenticated responses never enter CacheStorage. */
const SHELL_CACHE = "memos-journal-shell-v2";
const staticResource = (url) => url.origin === self.location.origin &&
  (url.pathname.startsWith("/assets/") || /^\/(?:logo|full-logo|apple-touch-icon|android-chrome-[^/]+)\.(?:png|webp)$/.test(url.pathname));
const privateEndpoint = (url) => /^\/(?:api|file|mcp|s|shares|shared)(?:\/|$)/.test(url.pathname) || url.pathname.includes("/shared/") || url.pathname.startsWith("/memos/shares/") || url.searchParams.has("share_token");
async function warmShell() {
  const response = await fetch("/", { cache: "reload", credentials: "omit" });
  if (!response.ok || !response.headers.get("content-type")?.includes("text/html")) return;
  const cache = await caches.open(SHELL_CACHE);
  await cache.put("/", response.clone());
  const html = await response.text();
  const paths = [...html.matchAll(/(?:src|href)=["']([^"']+)["']/g)].map((match) => new URL(match[1], self.location.origin)).filter(staticResource);
  await Promise.allSettled(paths.map(async (url) => { const asset = await fetch(url, { credentials: "omit" }); if (asset.ok) await cache.put(url, asset); }));
}
self.addEventListener("install", (event) => { event.waitUntil(warmShell().then(() => self.skipWaiting())); });
self.addEventListener("activate", (event) => {
  event.waitUntil((async () => {
    for (const key of await caches.keys()) {
      if (key.startsWith("memos-journal-shell-") && key !== SHELL_CACHE) await caches.delete(key);
    }
    await self.clients.claim();
  })());
});
self.addEventListener("message", (event) => {
  if (event.data?.type !== "journal-cache-static" || !Array.isArray(event.data.urls)) return;
  event.waitUntil((async () => {
    const cache = await caches.open(SHELL_CACHE);
    await Promise.allSettled(event.data.urls.slice(0, 150).map(async (value) => {
      const url = new URL(value, self.location.origin);
      if (!staticResource(url)) return;
      const response = await fetch(url, { credentials: "omit" });
      if (response.ok) await cache.put(url, response);
    }));
  })());
});
self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== "GET" || url.origin !== self.location.origin || privateEndpoint(url)) return;
  if (request.mode === "navigate") {
    event.respondWith((async () => {
      const cache = await caches.open(SHELL_CACHE).catch(() => undefined);
      try {
        const response = await fetch(request);
        // This server returns a static application shell, never embedded diary
        // data. Refresh it after a new build so offline HTML references the
        // version whose hashed assets were actually loaded.
        if (cache && response.ok && response.headers.get("content-type")?.includes("text/html")) await cache.put("/", response.clone()).catch(() => undefined);
        return response;
      } catch {
        return (cache && await cache.match("/").catch(() => undefined)) || new Response("请先联网打开一次记录页面，再使用离线记录。", { status: 503 });
      }
    })());
  } else if (staticResource(url)) {
    event.respondWith((async () => {
      const cache = await caches.open(SHELL_CACHE).catch(() => undefined);
      // Module requests carry Origin while warm-up fetches do not. The server
      // adds Vary: Origin, but these allowlisted static bytes are universal.
      // Private/API requests never reach this branch.
      const cached = cache && await cache.match(request, { ignoreVary: true }).catch(() => undefined);
      if (cached) return cached;
      const response = await fetch(request);
      // CacheStorage is optional: quota/read/write failures must not discard
      // an otherwise successful online resource response.
      if (cache && response.ok) await cache.put(request, response.clone()).catch(() => undefined);
      return response;
    })());
  }
});
