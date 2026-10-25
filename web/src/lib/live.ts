// Live updates from the server (server-sent events on GET /api/v1/events):
// this person's devices and remote control, and every change to their data
// made in another tab, on a phone, or in a Subsonic app through /rest. Events
// carry ids only; the pages that show the thing fetch it again.
//
// One EventSource for the whole app. It reconnects with backoff; every
// (re)connect starts with "hello", and after a drop listeners get a
// synthetic "resync" so whatever changed meanwhile is fetched again.
import { useEffect, useRef, useState } from 'react'
import type { Device } from './types'
import { V1 } from './music'

export type LiveEvent =
  | { type: 'hello'; data: { server_time: number } }
  | { type: 'resync'; data: null }
  | { type: 'devices'; data: Device[] }
  | { type: 'device'; data: Device }
  | { type: 'command'; data: { target: string; action: string; value: number; from: string } }
  | { type: 'sync'; data: { state: string; progress?: number; songs?: number; error?: string } }
  | { type: 'listens'; data: { ids?: number[]; song_ids?: string[]; deleted?: number[]; imported?: number } }
  | { type: 'stars'; data: { kind?: 'song' | 'album' | 'artist'; ids?: string[]; on?: boolean; refreshed?: boolean } }
  | { type: 'ratings'; data: { kind: 'song' | 'album' | 'artist'; ids: string[]; rating: number } }
  | { type: 'playlists'; data: { action: 'created' | 'updated' | 'deleted'; ids: string[]; smart_id?: number } }
  | { type: 'requests'; data: { action: 'added' | 'updated' | 'deleted'; ids: number[] } }
  | { type: 'lyrics'; data: { file_ids: number[]; many?: number } }
  | { type: 'lyrics_job'; data: { kind: 'run' | 'index'; state: string; done: number; total: number } }
  | { type: 'import'; data: { id: number; state?: string; seen: number; imported: number } }

export type LiveType = LiveEvent['type']
type Fn = (e: LiveEvent) => void

const subs = new Set<Fn>()
const statusSubs = new Set<(up: boolean) => void>()
let es: EventSource | null = null
let retry: number | undefined
let backoff = 1000
let up = false
let hellos = 0
let stopped = false

function setUp(v: boolean) {
  if (up === v) return
  up = v
  statusSubs.forEach(f => f(v))
}

function emit(ev: LiveEvent) {
  subs.forEach(f => { try { f(ev) } catch { /* one listener must not stop the rest */ } })
}

function connect() {
  clearTimeout(retry)
  es?.close()
  stopped = false
  const s = new EventSource(`${V1}/events`, { withCredentials: true })
  es = s
  s.onopen = () => { backoff = 1000; setUp(true) }
  s.onmessage = m => {
    let ev: LiveEvent
    try { ev = JSON.parse(m.data) as LiveEvent } catch { return }
    if (ev.type === 'hello' && hellos++ > 0) emit({ type: 'resync', data: null })
    emit(ev)
  }
  s.addEventListener('unauthorized', () => {
    s.close(); if (es === s) es = null
    stopped = true
    setUp(false)
    window.dispatchEvent(new CustomEvent('pmu:unauthorized'))
  })
  s.onerror = () => {
    setUp(false)
    // the browser retries a dropped stream by itself; a refused one (401, 5xx) is closed for good
    if (s.readyState === EventSource.CLOSED && es === s && !stopped) {
      retry = window.setTimeout(connect, backoff)
      backoff = Math.min(30_000, backoff * 2)
    }
  }
}

function ensure() { if (!es && !stopped && typeof EventSource !== 'undefined') connect() }

/** Listen to live events (opens the stream if needed). */
export function onLive(fn: Fn): () => void {
  subs.add(fn)
  ensure()
  return () => { subs.delete(fn) }
}

/** (Re)open the stream, e.g. after signing in. */
export function startLive() {
  if (typeof EventSource === 'undefined') return
  if (stopped || !es) { backoff = 1000; connect() }
}

export function stopLive() { clearTimeout(retry); stopped = true; es?.close(); es = null; setUp(false) }

export function liveConnected() { return up }

/** Is the live stream up? While it is down, pages fall back to a slow poll. */
export function useLiveConnected() {
  const [v, setV] = useState(up)
  useEffect(() => {
    statusSubs.add(setV)
    setV(up)
    return () => { statusSubs.delete(setV) }
  }, [])
  return v
}

/** Calls reload (debounced) when an event matches, and after a reconnect. */
export function useLiveRefresh(match: (e: LiveEvent) => boolean, reload: () => unknown, delay = 300) {
  const m = useRef(match)
  const r = useRef(reload)
  m.current = match
  r.current = reload
  useEffect(() => {
    let t: number | undefined
    const off = onLive(ev => {
      if (ev.type !== 'resync' && !m.current(ev)) return
      clearTimeout(t)
      t = window.setTimeout(() => r.current(), delay)
    })
    return () => { clearTimeout(t); off() }
  }, [delay])
}

/** Polls fn every ms while active, but only while the live stream is down. */
export function usePollWhileDown(fn: () => unknown, ms: number, active = true) {
  const live = useLiveConnected()
  const f = useRef(fn)
  f.current = fn
  useEffect(() => {
    if (!active || live) return
    const t = setInterval(() => f.current(), ms)
    return () => clearInterval(t)
  }, [active, live, ms])
}

/** The library index finished rebuilding (new songs, tags, play counts). */
export const isSynced = (e: LiveEvent) => e.type === 'sync' && e.data.state === 'idle'
/** Something that changes how songs, albums or artists look: stars, ratings, a refreshed index. */
export const isLibraryChange = (e: LiveEvent) => e.type === 'stars' || e.type === 'ratings' || isSynced(e)
/** A star, rating or index refresh that concerns any of these ids (an album and its songs, say). */
export function touches(e: LiveEvent, ids: Iterable<string>): boolean {
  if (isSynced(e) || (e.type === 'stars' && e.data.refreshed)) return true
  if (e.type !== 'stars' && e.type !== 'ratings') return false
  const want = new Set(ids)
  return (e.data.ids || []).some(id => want.has(id))
}
