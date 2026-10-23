// Small helpers shared by the music pages.
import type { Song } from './types'

export const V1 = '/api/v1'

/** Cover art URL through the server (al-, ar-, pl- prefixed ids or a song id). */
export function coverURL(id: string | undefined, size = 300): string {
  if (!id) return ''
  return `${V1}/cover/${encodeURIComponent(id)}?size=${size}`
}
export const albumCover = (albumId: string | undefined, size = 300) => albumId ? coverURL('al-' + albumId, size) : ''
export const artistImage = (artistId: string | undefined, size = 300) => artistId ? coverURL('ar-' + artistId, size) : ''

/** 3:05, 1:02:03 */
export function clock(sec: number): string {
  if (!isFinite(sec) || sec < 0) sec = 0
  sec = Math.floor(sec)
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`
}

/** 1 h 12 min, 48 min */
export function longDuration(sec: number): string {
  const m = Math.round(sec / 60)
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60), r = m % 60
  if (h >= 48) return `${Math.round(h / 24)} days`
  return r ? `${h} h ${r} min` : `${h} h`
}

export function totalDuration(songs: Song[]): number { return songs.reduce((t, s) => t + (s.duration || 0), 0) }

const LOSSLESS = new Set(['flac', 'alac', 'wav', 'aiff', 'ape', 'wv', 'dsf', 'dff'])
export const isLossless = (s: Pick<Song, 'suffix'>) => LOSSLESS.has((s.suffix || '').toLowerCase())

/** "FLAC 24/96", "MP3 320" */
export function qualityOf(s: Song): string {
  const f = (s.suffix || '').toUpperCase()
  if (isLossless(s) && s.bit_depth && s.sample_rate) return `${f} ${s.bit_depth}/${+(s.sample_rate / 1000).toFixed(1)}`
  return s.bitrate ? `${f} ${s.bitrate}` : f
}

/** The person's offset from UTC in minutes, for date-bucketed stats. */
export const tz = () => -new Date().getTimezoneOffset()

/** A stable colour for things without art. */
export function hueFor(text: string): number {
  let h = 0
  for (let i = 0; i < text.length; i++) h = (h * 31 + text.charCodeAt(i)) % 360
  return h
}

/** This browser's device id and a readable name ("Chrome on Mac"). */
export function deviceId(): string {
  let id = localStorage.getItem('pmu-device')
  if (!id) {
    id = Array.from(crypto.getRandomValues(new Uint8Array(9)), b => b.toString(16).padStart(2, '0')).join('')
    localStorage.setItem('pmu-device', id)
  }
  return id
}
export function deviceName(): string {
  const saved = localStorage.getItem('pmu-device-name')
  if (saved) return saved
  const ua = navigator.userAgent
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : 'Browser'
  const br = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) && !/Edg\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : ''
  if (os === 'iPhone' || os === 'iPad' || os === 'Android') return os
  return br ? `${br} on ${os}` : os
}
export const isPhone = () => /iPhone|Android.+Mobile/.test(navigator.userAgent)
