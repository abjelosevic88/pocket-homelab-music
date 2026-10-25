// React access to the audio engine and to the person's other devices.
import { useEffect, useState, useSyncExternalStore } from 'react'
import { api } from './api'
import { onLive, startLive as openLive } from './live'
import { V1, deviceId } from './music'
import { player } from './player'
import type { Device, Song } from './types'

export { player }

/** The player's state (queue, current, playing…); re-renders on changes, not on every tick. */
export function usePlayer() {
  const state = useSyncExternalStore(player.subscribe, player.getState)
  return { ...state, current: state.queue[state.index] || null }
}

/** Position, duration and buffered seconds, updated a few times a second. */
export function useProgress(active = true) {
  const [t, setT] = useState(() => ({ position: player.position, duration: player.duration, buffered: player.buffered }))
  useEffect(() => {
    if (!active) return
    let last = 0
    const f = () => {
      const now = performance.now()
      if (now - last < 200) return
      last = now
      setT({ position: player.position, duration: player.duration, buffered: player.buffered })
    }
    f()
    return player.subscribeTime(f)
  }, [active])
  return t
}

/** Is this song the one playing? */
export function useIsCurrent(id: string) {
  const cur = useSyncExternalStore(player.subscribe, () => player.current?.id || '')
  return cur === id
}

/** The person's other devices, live. Also wires remote-control commands to this one. */
let devices: Device[] = []
const devSubs = new Set<() => void>()
let wired = false
function wireLive() {
  if (wired) return
  wired = true
  onLive(ev => {
    if (ev.type === 'devices') { devices = ev.data; devSubs.forEach(f => f()) }
    if (ev.type === 'device') {
      const i = devices.findIndex(d => d.id === ev.data.id)
      devices = i >= 0 ? devices.map(d => d.id === ev.data.id ? ev.data : d) : [ev.data, ...devices]
      devSubs.forEach(f => f())
    }
    if (ev.type === 'command' && ev.data.target === deviceId()) player.command(ev.data.action, ev.data.value)
    if (ev.type === 'stars' && ev.data.ids && ev.data.kind === 'song') player.patchSongs(ev.data.ids, { starred_at: ev.data.on ? new Date().toISOString() : '' })
    if (ev.type === 'ratings' && ev.data.kind === 'song') player.patchSongs(ev.data.ids, { rating: ev.data.rating })
  })
}
export function useDevices() {
  wireLive()
  const list = useSyncExternalStore(f => { devSubs.add(f); return () => { devSubs.delete(f) } }, () => devices)
  const me = deviceId()
  return { others: list.filter(d => d.id !== me), all: list, me }
}
export function startLive() { wireLive(); openLive() }

/** Is a device playing right now? An app counts until its song can have ended; a browser while it reports. */
export function isLive(d: Device): boolean {
  if (d.kind === 'app') return !!d.until && Date.now() < Date.parse(d.until)
  return d.active && d.playing
}

/** Re-render now and then, so "Playing on …" goes away when it's over. */
export function useTick(ms = 15000) {
  const [, set] = useState(0)
  useEffect(() => { const t = setInterval(() => set(n => n + 1), ms); return () => clearInterval(t) }, [ms])
}

/** Send a command to another device. */
export const remote = (id: string, action: string, value = 0) => api.post(`${V1}/devices/${id}/command`, { action, value, from: deviceId() }).catch(() => {})

/** Move another device's queue here and pause it there. */
export async function takeOver(d: Device) {
  const r = await api.get<{ songs: Song[] }>(`${V1}/devices/${encodeURIComponent(d.id)}/queue`)
  if (d.kind !== 'app') remote(d.id, 'pause') // an app can't be told to stop
  player.takeOver(r.songs, d.queue_index, d.position_ms + (d.playing ? Date.now() - Date.parse(d.updated_at) : 0), d.name)
}
