// Live updates from the server (server-sent events): this person's other
// devices, remote-control commands, library sync progress, import progress.
import type { Device } from './types'
import { V1 } from './music'

export type LiveEvent =
  | { type: 'devices'; data: Device[] }
  | { type: 'device'; data: Device }
  | { type: 'command'; data: { target: string; action: string; value: number; from: string } }
  | { type: 'sync'; data: { state: string; progress?: number; songs?: number; error?: string } }
  | { type: 'listen'; data: unknown }
  | { type: 'import'; data: { id: number; state?: string; seen: number; imported: number } }

type Fn = (e: LiveEvent) => void
const subs = new Set<Fn>()
let es: EventSource | null = null
let retry = 0

function connect() {
  if (es) return
  es = new EventSource(`${V1}/events`, { withCredentials: true })
  es.onmessage = m => {
    retry = 0
    try {
      const ev = JSON.parse(m.data) as LiveEvent
      subs.forEach(f => f(ev))
    } catch { /* keep-alive or garbage */ }
  }
  es.onerror = () => {
    // the browser retries by itself; after a few failures start over (signed out, server restarted)
    if (++retry > 5) { es?.close(); es = null; setTimeout(connect, 15000) }
  }
}

export function onLive(fn: Fn): () => void {
  subs.add(fn)
  connect()
  return () => { subs.delete(fn) }
}

export function stopLive() { es?.close(); es = null }
