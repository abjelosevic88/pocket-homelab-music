// Offline listening: songs downloaded onto this device (Cache Storage, served
// by the service worker at /offline/song/<id> with Range support), the index
// of what is here (localStorage), and plays made without a network, sent when
// it comes back.
import { useEffect, useState } from 'react'
import { api } from './api'
import { V1, albumCover, isPhone } from './music'
import type { Song } from './types'

const CACHE = 'pmu-offline'
const LS_INDEX = 'pmu-offline-index'
const LS_QUALITY = 'pmu-download-quality'
const LS_PLAYS = 'pmu-offline-plays'
const UNPLAYABLE = new Set(['dsf', 'dff', 'ape', 'wv', 'wma', 'mpc', 'tak', 'shn', 'tta'])

export interface OfflineSong { song: Song; bytes: number; quality: string; at: number; groups: string[] }
export interface OfflineGroup { key: string; name: string; kind: 'album' | 'playlist' | 'mix' | 'songs'; cover?: string }
interface Index { songs: Record<string, OfflineSong>; groups: Record<string, OfflineGroup> }

export const offlineSupported = () => 'caches' in window && 'serviceWorker' in navigator

let index: Index = load()
let queue: { song: Song; group: OfflineGroup }[] = []
export interface ActiveDownload { id: string; title: string; done: number; total: number }
let active: ActiveDownload | null = null
let failed = 0
let format = 'aac' // when a download is transcoded: the person's format (set by the player)
export function setOfflineFormat(f: string) { if (f) format = f }
const subs = new Set<() => void>()

function load(): Index {
  try { const x = JSON.parse(localStorage.getItem(LS_INDEX) || ''); if (x?.songs && x?.groups) return x } catch { /* first time */ }
  return { songs: {}, groups: {} }
}
function save() {
  try { localStorage.setItem(LS_INDEX, JSON.stringify(index)) } catch { /* full: the cache still has the files */ }
  subs.forEach(f => f())
}
const notify = () => subs.forEach(f => f())

export const offlineURL = (id: string) => `/offline/song/${encodeURIComponent(id)}`
export const isOffline = (id: string) => !!index.songs[id]

/** What a download is made as on this device: the original file, or a smaller transcode. */
export function downloadQuality(): string {
  return localStorage.getItem(LS_QUALITY) || (isPhone() ? '256' : 'original')
}
export function setDownloadQuality(q: string) { localStorage.setItem(LS_QUALITY, q); notify() }

function sourceURL(song: Song, q: string, format: string): { url: string; quality: string } {
  if (q === 'original' && !UNPLAYABLE.has((song.suffix || '').toLowerCase())) return { url: `${V1}/stream/${encodeURIComponent(song.id)}`, quality: 'original' }
  const kbps = q === 'original' ? '320' : q
  return { url: `${V1}/stream/${encodeURIComponent(song.id)}?q=${kbps}&format=${format}`, quality: kbps }
}

/** Download songs for offline play, filed under a group (an album, a playlist…). */
export async function download(songs: Song[], group: OfflineGroup) {
  if (!offlineSupported()) throw new Error('This browser can’t keep songs offline')
  // ask once to keep the files when space runs short (iOS asks nothing, Chrome decides itself)
  navigator.storage?.persist?.().catch(() => {})
  index.groups[group.key] = group
  for (const s of songs) {
    const have = index.songs[s.id]
    if (have) { if (!have.groups.includes(group.key)) have.groups.push(group.key) } else if (!queue.some(x => x.song.id === s.id)) queue.push({ song: s, group })
  }
  save()
  if (!active) run()
}

async function run() {
  const cache = await caches.open(CACHE)
  const fmt = localStorage.getItem('pmu-format-fallback') || format
  let done = 0
  failed = 0
  while (queue.length) {
    const { song, group } = queue.shift()!
    active = { id: song.id, title: song.title, done, total: done + queue.length + 1 }
    notify()
    try {
      const { url, quality } = sourceURL(song, downloadQuality(), fmt)
      const res = await fetch(url, { credentials: 'same-origin' })
      if (!res.ok) throw new Error(res.statusText)
      const blob = await res.blob()
      await cache.put(offlineURL(song.id), new Response(blob, { headers: { 'Content-Type': res.headers.get('Content-Type') || blob.type || 'audio/mpeg', 'Content-Length': String(blob.size) } }))
      index.songs[song.id] = { song, bytes: blob.size, quality, at: Date.now(), groups: [group.key] }
      // its cover, in the sizes the app shows, for offline browsing
      for (const size of [64, 300]) fetch(albumCover(song.album_id, size)).catch(() => {})
      save()
    } catch {
      failed++
    }
    done++
  }
  active = null
  notify()
}

/** Remove a group's downloads (songs another group still wants stay). */
export async function removeGroup(key: string) {
  const cache = await caches.open(CACHE)
  for (const [id, o] of Object.entries(index.songs)) {
    o.groups = o.groups.filter(g => g !== key)
    if (!o.groups.length) { delete index.songs[id]; await cache.delete(offlineURL(id)) }
  }
  delete index.groups[key]
  queue = queue.filter(x => x.group.key !== key)
  save()
}

export async function removeAll() {
  queue = []
  await caches.delete(CACHE)
  index = { songs: {}, groups: {} }
  save()
}

/** A downloaded file that went missing (the browser evicted it): forget it. */
export function forget(id: string) {
  if (!index.songs[id]) return
  delete index.songs[id]
  save()
}

export interface OfflineState { songs: Record<string, OfflineSong>; groups: OfflineGroup[]; active: ActiveDownload | null; queued: number; failed: number; bytes: number; quality: string }
function snapshot(): OfflineState {
  const bytes = Object.values(index.songs).reduce((t, o) => t + o.bytes, 0)
  return { songs: index.songs, groups: Object.values(index.groups), active, queued: queue.length, failed, bytes, quality: downloadQuality() }
}

export function useOffline(): OfflineState {
  const [st, setSt] = useState(snapshot)
  useEffect(() => {
    const f = () => setSt(snapshot())
    subs.add(f)
    return () => { subs.delete(f) }
  }, [])
  return st
}

/** How much of a list of songs is on this device: 'all' | 'some' | 'none'. */
export function coverage(songs: Song[]): 'all' | 'some' | 'none' {
  if (!songs.length) return 'none'
  const n = songs.filter(s => index.songs[s.id]).length
  return n === songs.length ? 'all' : n ? 'some' : 'none'
}

// ---------- plays made offline ----------

interface PendingPlay { song_id: string; played_at: number; ms_played: number; device: string }

export function queuePlay(p: PendingPlay) {
  const list: PendingPlay[] = JSON.parse(localStorage.getItem(LS_PLAYS) || '[]')
  list.push(p)
  localStorage.setItem(LS_PLAYS, JSON.stringify(list.slice(-2000)))
}

let flushing = false
export async function flushPlays() {
  if (flushing || !navigator.onLine) return
  const list: PendingPlay[] = JSON.parse(localStorage.getItem(LS_PLAYS) || '[]')
  if (!list.length) return
  flushing = true
  const left: PendingPlay[] = []
  for (const p of list) {
    try { await api.post(`${V1}/listens`, p) } catch (e) {
      // the server said no (a song gone from the library): drop it; no network: keep it
      if (!(e as { status?: number }).status) left.push(p)
    }
  }
  localStorage.setItem(LS_PLAYS, JSON.stringify(left))
  flushing = false
}

// ---------- the service worker ----------

export function registerServiceWorker() {
  if (!('serviceWorker' in navigator) || location.port === '5173') return // not under the vite dev server
  navigator.serviceWorker.register('/sw.js').then(() => navigator.serviceWorker.ready).then(reg => {
    // the build files this page uses, so they are there next time without a network
    const urls = performance.getEntriesByType('resource').map(e => e.name).filter(u => u.startsWith(location.origin + '/assets/'))
    reg.active?.postMessage({ type: 'assets', urls })
  }).catch(() => {})
  window.addEventListener('online', () => { flushPlays() })
  setTimeout(flushPlays, 3000)
}

export function signedOut() {
  navigator.serviceWorker?.controller?.postMessage({ type: 'signed-out' })
}
