// Pocket Music service worker: the app opens without a network, and songs
// downloaded for offline play are served from this device.
//
//   /offline/song/<id>   downloaded songs (Cache "pmu-offline", written by the
//                        page — lib/offline.ts), with Range support: Safari
//                        won't play audio without it
//   /api/v1/cover/…      cover art, cache first ("pmu-covers", trimmed)
//   /api/v1/…            GETs network first, the last answer when offline ("pmu-api")
//   /assets/…            hashed build files, cache first ("pmu-assets")
//   pages                network first, else the cached app shell

const OFFLINE = 'pmu-offline'
const COVERS = 'pmu-covers'
const API = 'pmu-api'
const ASSETS = 'pmu-assets'
const SHELL = 'pmu-shell'
const MAX_COVERS = 4000
const MAX_API = 600

self.addEventListener('install', e => {
  e.waitUntil(caches.open(SHELL).then(c => c.add(new Request('/', { cache: 'reload' }))).catch(() => {}).then(() => self.skipWaiting()))
})

self.addEventListener('activate', e => { e.waitUntil(self.clients.claim()) })

// the page tells us which build files it uses: keep those, drop older builds
self.addEventListener('message', e => {
  const d = e.data || {}
  if (d.type === 'assets' && Array.isArray(d.urls)) {
    e.waitUntil((async () => {
      const c = await caches.open(ASSETS)
      const want = new Set(d.urls)
      for (const req of await c.keys()) if (!want.has(req.url)) await c.delete(req)
      for (const u of want) if (!(await c.match(u))) await c.add(u).catch(() => {})
      const shell = await caches.open(SHELL)
      await shell.add(new Request('/', { cache: 'reload' })).catch(() => {})
    })())
  }
  if (d.type === 'signed-out') e.waitUntil(caches.delete(API))
})

async function trim(name, max) {
  const c = await caches.open(name)
  const keys = await c.keys()
  for (let i = 0; i < keys.length - max; i++) await c.delete(keys[i])
}

async function offlineSong(req) {
  const hit = await caches.match(new URL(req.url).pathname, { cacheName: OFFLINE })
  if (!hit) return new Response('not downloaded', { status: 404 })
  const blob = await hit.blob()
  const type = hit.headers.get('Content-Type') || 'audio/mpeg'
  const range = /^bytes=(\d*)-(\d*)$/.exec(req.headers.get('Range') || '')
  if (!range) {
    return new Response(blob, { status: 200, headers: { 'Content-Type': type, 'Content-Length': String(blob.size), 'Accept-Ranges': 'bytes' } })
  }
  let start = range[1] === '' ? NaN : Number(range[1])
  let end = range[2] === '' ? blob.size - 1 : Math.min(Number(range[2]), blob.size - 1)
  if (isNaN(start)) { start = Math.max(0, blob.size - end - 1); end = blob.size - 1 } // bytes=-N
  if (start >= blob.size || start > end) {
    return new Response('', { status: 416, headers: { 'Content-Range': `bytes */${blob.size}` } })
  }
  return new Response(blob.slice(start, end + 1), { status: 206, headers: {
    'Content-Type': type, 'Content-Length': String(end - start + 1), 'Content-Range': `bytes ${start}-${end}/${blob.size}`, 'Accept-Ranges': 'bytes' } })
}

async function cacheFirst(req, name, max) {
  const c = await caches.open(name)
  const hit = await c.match(req)
  if (hit) return hit
  const res = await fetch(req)
  if (res.ok) { await c.put(req, res.clone()); if (max) trim(name, max) }
  return res
}

async function networkFirst(req, name, max) {
  try {
    const res = await fetch(req)
    if (res.ok && (res.headers.get('Content-Type') || '').includes('json')) {
      const c = await caches.open(name)
      await c.put(req, res.clone())
      trim(name, max)
    }
    return res
  } catch (err) {
    const hit = await caches.match(req, { cacheName: name })
    if (hit) return hit
    return new Response(JSON.stringify({ error: 'You are offline — this page hasn’t been opened on this device before.' }), { status: 503, headers: { 'Content-Type': 'application/json' } })
  }
}

self.addEventListener('fetch', e => {
  const req = e.request
  const url = new URL(req.url)
  if (url.origin !== self.location.origin || req.method !== 'GET') return
  const p = url.pathname
  if (p.startsWith('/offline/song/')) return e.respondWith(offlineSong(req))
  if (p.startsWith('/assets/')) return e.respondWith(cacheFirst(req, ASSETS))
  if (p.startsWith('/api/v1/cover/')) return e.respondWith(cacheFirst(req, COVERS, MAX_COVERS))
  if (p.startsWith('/api/')) {
    // live streams and audio go straight to the network
    if (p.startsWith('/api/v1/stream/') || p.startsWith('/api/v1/events') || req.headers.get('Accept') === 'text/event-stream') return
    return e.respondWith(networkFirst(req, API, MAX_API))
  }
  if (p.startsWith('/rest/')) return
  if (req.mode === 'navigate') {
    e.respondWith(fetch(req).catch(async () => (await caches.match('/', { cacheName: SHELL })) || Response.error()))
  }
})
