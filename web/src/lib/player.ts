// The audio engine: one queue, two <audio> elements (the next song loads
// while this one plays, so albums run gapless and crossfades are possible),
// the lock-screen controls, scrobbling and the device report. It lives
// outside React; components subscribe through lib/player-react.tsx.
import { api } from './api'
import { V1, albumCover, deviceId, deviceName, isPhone } from './music'
import type { Settings, Song } from './types'

export type Repeat = 'off' | 'all' | 'one'
export interface Source { kind: 'album' | 'artist' | 'playlist' | 'mix' | 'radio' | 'songs' | 'search' | 'queue' | 'genre' | 'device'; name: string; link?: string }

export interface PlayerState {
  queue: Song[]
  index: number
  playing: boolean
  loading: boolean
  shuffle: boolean
  repeat: Repeat
  volume: number
  muted: boolean
  source: Source | null
  error: string
  sleepAt: number // ms epoch, 0 = off, -1 = end of this song
  quality: string // in effect for this device
}

const LS = { queue: 'pmu-queue', volume: 'pmu-volume', muted: 'pmu-muted', repeat: 'pmu-repeat', quality: 'pmu-quality' }
const isIOS = () => /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)

type Listener = () => void

export class Player {
  state: PlayerState
  private a: HTMLAudioElement[]
  private cur = 0 // which <audio> plays
  private offset = 0 // seconds: a transcoded stream started at this point of the song
  private transcoded = false
  private preloaded = -1 // queue index loaded into the other element
  private listeners = new Set<Listener>()
  private timeListeners = new Set<Listener>()
  private settings: Settings | null = null
  private unshuffled: Song[] | null = null
  // scrobbling: what of the current song was actually heard
  private heardMs = 0
  private lastTick = 0
  private startedAt = 0
  private scrobbled = false
  private reportTimer = 0
  private reportedQueue = ''
  private fading = false
  private errors = 0
  private restored = false
  // a browser that can't decode the chosen transcode format (some Chromium builds and MP3) gets AAC
  private formatFallback = localStorage.getItem('pmu-format-fallback') || ''
  private originalFailed = new Set<string>()

  constructor() {
    this.a = [new Audio(), new Audio()]
    for (const el of this.a) {
      el.preload = 'auto'
      ;(el as HTMLAudioElement & { playsInline: boolean }).playsInline = true
      el.addEventListener('timeupdate', () => { if (el === this.el) this.onTime() })
      el.addEventListener('ended', () => { if (el === this.el) this.onEnded() })
      el.addEventListener('playing', () => { if (el === this.el) this.set({ playing: true, loading: false, error: '' }) })
      el.addEventListener('pause', () => { if (el === this.el && !this.fading) { this.set({ playing: false }); this.report() } })
      el.addEventListener('waiting', () => { if (el === this.el) this.set({ loading: true }) })
      el.addEventListener('canplay', () => { if (el === this.el) this.set({ loading: false }) })
      el.addEventListener('error', () => { if (el === this.el) this.onError() })
    }
    this.state = {
      queue: [], index: -1, playing: false, loading: false, shuffle: false,
      repeat: (localStorage.getItem(LS.repeat) as Repeat) || 'off',
      volume: Number(localStorage.getItem(LS.volume) ?? 1), muted: localStorage.getItem(LS.muted) === '1',
      source: null, error: '', sleepAt: 0, quality: localStorage.getItem(LS.quality) || '',
    }
    this.applyVolume()
    this.mediaSession()
    window.addEventListener('pagehide', () => this.persist())
    setInterval(() => { if (this.state.playing) this.persist() }, 10000)
    setInterval(() => this.checkSleep(), 1000)
  }

  // ---------- subscription ----------
  subscribe = (fn: Listener) => { this.listeners.add(fn); return () => { this.listeners.delete(fn) } }
  subscribeTime = (fn: Listener) => { this.timeListeners.add(fn); return () => { this.timeListeners.delete(fn) } }
  getState = () => this.state
  private set(patch: Partial<PlayerState>) {
    this.state = { ...this.state, ...patch }
    this.listeners.forEach(f => f())
  }

  get el() { return this.a[this.cur] }
  get other() { return this.a[1 - this.cur] }
  get current(): Song | null { return this.state.queue[this.state.index] || null }
  /** Where in the song we are, seconds. */
  get position(): number { return this.offset + (this.el.currentTime || 0) }
  get duration(): number { return this.current?.duration || (isFinite(this.el.duration) ? this.el.duration : 0) }
  get buffered(): number {
    const b = this.el.buffered
    return b.length ? this.offset + b.end(b.length - 1) : this.offset
  }

  configure(s: Settings) { this.settings = s; this.applyVolume() }

  // ---------- what plays ----------

  /** The streaming quality in effect: this device's choice, else the person's, "auto" decided per song. */
  qualityFor(song: Song): string {
    const q = this.state.quality || this.settings?.quality || 'auto'
    // formats no browser plays (DSD, Monkey's Audio, WavPack, WMA) — or this one couldn't — are transcoded
    if (UNPLAYABLE.has(song.suffix) || this.originalFailed.has(song.id)) return q === 'auto' || q === 'original' ? '320' : q
    if (q !== 'auto') return q
    // phones over the mobile network: lossless and hi-res files are made 256k
    if (isPhone() && (song.bitrate > 330 || ['flac', 'wav', 'aiff', 'alac', 'ape', 'wv', 'dsf', 'dff'].includes(song.suffix))) return '256'
    return 'original'
  }

  private srcFor(song: Song, offset = 0): { url: string; transcoded: boolean } {
    const q = this.qualityFor(song)
    if (q === 'original') return { url: `${V1}/stream/${encodeURIComponent(song.id)}`, transcoded: false }
    const f = this.formatFallback || this.settings?.format || 'mp3'
    return { url: `${V1}/stream/${encodeURIComponent(song.id)}?q=${q}&format=${f}${offset > 0 ? `&offset=${Math.floor(offset)}` : ''}`, transcoded: true }
  }

  setQuality(q: string) {
    if (q) localStorage.setItem(LS.quality, q); else localStorage.removeItem(LS.quality)
    this.set({ quality: q })
    // reload the current song at the same spot in the new quality
    if (this.current) this.load(this.state.index, this.state.playing, this.position)
  }

  /** Start playing a list of songs. With shuffle, start < 0 shuffles everything;
   *  otherwise the chosen song plays first and the rest are shuffled. */
  playSongs(songs: Song[], start = 0, source: Source | null = null, shuffle = false) {
    if (!songs.length) return
    let queue = songs.slice()
    let index = Math.max(0, Math.min(start, songs.length - 1))
    this.unshuffled = null
    if (shuffle) {
      this.unshuffled = queue.slice()
      if (start < 0) queue = shuffleSongs(queue)
      else queue = [queue[index], ...shuffleSongs(queue.filter((_, i) => i !== index))]
      index = 0
    }
    this.set({ queue, index, source, shuffle })
    this.load(index, true, 0)
    this.reportedQueue = ''
    this.report(true)
  }

  /** Put songs right after the current one. */
  playNext(songs: Song[]) {
    if (!this.current) return this.playSongs(songs, 0, { kind: 'queue', name: 'Queue' })
    const q = this.state.queue.slice()
    q.splice(this.state.index + 1, 0, ...songs)
    this.set({ queue: q })
    this.preloaded = -1
    this.persist(); this.report(true)
  }

  /** Add songs at the end of the queue. */
  addToQueue(songs: Song[]) {
    if (!this.current) return this.playSongs(songs, 0, { kind: 'queue', name: 'Queue' })
    this.set({ queue: [...this.state.queue, ...songs] })
    this.preloaded = -1
    this.persist(); this.report(true)
  }

  removeAt(i: number) {
    if (i === this.state.index) return this.next()
    const q = this.state.queue.slice()
    q.splice(i, 1)
    this.set({ queue: q, index: i < this.state.index ? this.state.index - 1 : this.state.index })
    this.preloaded = -1
    this.persist(); this.report(true)
  }

  move(from: number, to: number) {
    if (from === to) return
    const q = this.state.queue.slice()
    const [s] = q.splice(from, 1)
    q.splice(to, 0, s)
    let idx = this.state.index
    if (from === idx) idx = to
    else if (from < idx && to >= idx) idx--
    else if (from > idx && to <= idx) idx++
    this.set({ queue: q, index: idx })
    this.preloaded = -1
    this.persist(); this.report(true)
  }

  /** Drop everything after the current song. */
  clearUpcoming() {
    this.set({ queue: this.state.queue.slice(0, this.state.index + 1) })
    this.preloaded = -1
    this.persist(); this.report(true)
  }

  jump(i: number) { if (i >= 0 && i < this.state.queue.length) this.load(i, true, 0) }

  // ---------- transport ----------

  play() {
    if (!this.current) return
    if (!this.el.src) { this.load(this.state.index, true, this.restorePos); return }
    this.el.play().catch(e => this.set({ error: playError(e), playing: false }))
    this.lastTick = performance.now()
  }
  pause() { this.el.pause(); this.persist() }
  toggle() { if (this.state.playing) this.pause(); else this.play() }

  next(auto = false) {
    const { queue, index, repeat } = this.state
    if (!queue.length) return
    if (auto && repeat === 'one') return this.load(index, true, 0)
    if (index + 1 < queue.length) return this.load(index + 1, true, 0)
    if (repeat === 'all') return this.load(0, true, 0)
    // the end of the queue: stop at the start of the last song
    this.finishListen()
    this.el.pause()
    this.seek(0)
    this.set({ playing: false })
    this.report()
  }

  previous() {
    if (this.position > 3 || this.state.index === 0) return this.seek(0)
    this.load(this.state.index - 1, true, 0)
  }

  seek(t: number) {
    const song = this.current
    if (!song) return
    t = Math.max(0, Math.min(t, (song.duration || this.duration) - 0.25))
    if (!this.transcoded) {
      this.el.currentTime = t
    } else if (t >= this.offset && t <= this.buffered) {
      this.el.currentTime = t - this.offset
    } else {
      // a transcoded stream can't jump ahead of what is buffered: start it again there
      this.load(this.state.index, !this.el.paused || this.state.playing, t, true)
    }
    this.timeListeners.forEach(f => f())
    this.updatePositionState()
  }

  setVolume(v: number) {
    v = Math.max(0, Math.min(1, v))
    localStorage.setItem(LS.volume, String(v))
    this.set({ volume: v, muted: v === 0 ? this.state.muted : false })
    localStorage.setItem(LS.muted, this.state.muted ? '1' : '0')
    this.applyVolume()
  }
  toggleMute() { this.set({ muted: !this.state.muted }); localStorage.setItem(LS.muted, this.state.muted ? '1' : '0'); this.applyVolume() }

  toggleShuffle() {
    const { queue, index, shuffle } = this.state
    if (!queue.length) { this.set({ shuffle: !shuffle }); return }
    const cur = queue[index]
    if (!shuffle) {
      // only what is still to come gets shuffled
      this.unshuffled = queue.slice()
      this.set({ shuffle: true, queue: [...queue.slice(0, index + 1), ...shuffleSongs(queue.slice(index + 1))] })
    } else {
      const orig = this.unshuffled || queue
      const i = orig.findIndex(s => s.id === cur.id)
      this.set({ shuffle: false, queue: orig, index: i >= 0 ? i : 0 })
      this.unshuffled = null
    }
    this.preloaded = -1
    this.persist(); this.report(true)
  }
  cycleRepeat() {
    const r: Repeat = this.state.repeat === 'off' ? 'all' : this.state.repeat === 'all' ? 'one' : 'off'
    localStorage.setItem(LS.repeat, r)
    this.set({ repeat: r })
  }

  /** Stop after N minutes (0 = off, -1 = at the end of this song). */
  sleep(minutes: number) { this.set({ sleepAt: minutes > 0 ? Date.now() + minutes * 60000 : minutes }) }
  private checkSleep() {
    const at = this.state.sleepAt
    if (at > 0 && Date.now() >= at && this.state.playing) {
      // fade out over a few seconds rather than cutting off
      const el = this.el, v0 = el.volume
      let k = 0
      const id = setInterval(() => {
        k++
        el.volume = Math.max(0, v0 * (1 - k / 20))
        if (k >= 20) { clearInterval(id); this.pause(); this.applyVolume(); this.set({ sleepAt: 0 }) }
      }, 150)
      this.set({ sleepAt: 0 })
    }
  }

  // ---------- loading ----------

  private restorePos = 0

  private load(i: number, autoplay: boolean, startAt = 0, keepListen = false) {
    const song = this.state.queue[i]
    if (!song) return
    if (!keepListen) this.finishListen()
    this.fading = false
    const sameSong = keepListen && i === this.state.index
    if (!sameSong) {
      this.heardMs = 0; this.scrobbled = false; this.startedAt = Date.now() - startAt * 1000
    }
    // the song may already be loaded in the other element
    if (this.preloaded === i && startAt === 0 && !sameSong) {
      this.el.pause()
      this.el.removeAttribute('src'); this.el.load()
      this.cur = 1 - this.cur
      this.offset = 0
      this.transcoded = this.qualityFor(song) !== 'original'
    } else {
      const { url, transcoded } = this.srcFor(song, startAt)
      this.transcoded = transcoded
      this.offset = transcoded ? Math.floor(startAt) : 0
      this.el.src = url
      if (!transcoded && startAt > 0) {
        const el = this.el
        const at = startAt
        el.addEventListener('loadedmetadata', () => { el.currentTime = at }, { once: true })
      }
    }
    this.preloaded = -1
    this.set({ index: i, loading: true, error: '' })
    this.applyVolume()
    this.mediaMetadata(song)
    if (autoplay) {
      this.lastTick = performance.now()
      this.el.play().catch(e => this.set({ error: playError(e), playing: false, loading: false }))
      if (!sameSong) this.report(true, true)
    }
    this.persist()
  }

  /** Load the next song into the other element while this one plays. */
  private preloadNext() {
    const { queue, index, repeat } = this.state
    let n = index + 1
    if (n >= queue.length) n = repeat === 'all' ? 0 : -1
    if (repeat === 'one') n = -1
    if (n < 0 || n === this.preloaded) return
    const song = queue[n]
    const { url } = this.srcFor(song)
    this.other.src = url
    this.other.load()
    this.preloaded = n
  }

  private onTime() {
    const now = performance.now()
    if (this.state.playing && this.lastTick) this.heardMs += Math.min(2000, now - this.lastTick)
    this.lastTick = now
    this.timeListeners.forEach(f => f())
    const song = this.current
    if (!song) return
    const pos = this.position, dur = song.duration || this.duration
    // a play counts after scrobble_pct of the song or four minutes (like Last.fm)
    const pct = (this.settings?.scrobble_pct || 50) / 100
    if (!this.scrobbled && dur > 30 && this.heardMs / 1000 >= Math.min(dur * pct, 240)) {
      this.scrobbled = true
      this.sendListen(song)
    }
    if (dur && dur - pos < 25) this.preloadNext()
    // crossfade (not on iOS: volume is fixed there)
    const cf = this.settings?.crossfade || 0
    if (cf > 0 && !isIOS() && !this.fading && dur && dur - pos <= cf && this.state.repeat !== 'one' && this.state.index + 1 < this.state.queue.length) {
      this.crossfade(cf)
    }
    if (Math.floor(pos) % 15 === 0) this.updatePositionState()
  }

  private crossfade(seconds: number) {
    this.preloadNext()
    const next = this.state.index + 1
    if (this.preloaded !== next) return
    this.fading = true
    const out = this.el, inn = this.other
    const target = this.targetVolume(this.state.queue[next])
    inn.volume = 0
    inn.play().catch(() => {})
    const steps = Math.max(1, Math.round(seconds * 10))
    const v0 = out.volume
    let k = 0
    const id = setInterval(() => {
      k++
      out.volume = Math.max(0, v0 * (1 - k / steps))
      inn.volume = Math.min(1, target * (k / steps))
      if (k >= steps) {
        clearInterval(id)
        out.pause()
        this.finishListen()
        this.cur = 1 - this.cur
        this.offset = 0
        this.transcoded = this.qualityFor(this.state.queue[next]) !== 'original'
        this.preloaded = -1
        this.heardMs = 0; this.scrobbled = false; this.startedAt = Date.now()
        this.fading = false
        this.set({ index: next, playing: true })
        this.mediaMetadata(this.state.queue[next])
        this.applyVolume()
        this.report(true, true)
        this.persist()
      }
    }, 100)
  }

  private onEnded() {
    if (this.fading) return
    if (this.state.sleepAt === -1) { this.finishListen(); this.set({ sleepAt: 0, playing: false }); this.next(true); this.el.pause(); return }
    this.next(true)
  }

  private onError() {
    const song = this.current
    const code = this.el.error?.code
    if (song && !this.transcoded && code && code >= 3 && !this.originalFailed.has(song.id)) {
      // the browser can't decode the original file: let the server transcode it
      this.originalFailed.add(song.id)
      this.load(this.state.index, true, this.position, true)
      return
    }
    if (song && this.transcoded && code && code >= 3 && this.formatFallback !== 'aac') {
      // decode / format errors: try the same spot again as AAC before giving up on the song
      this.formatFallback = 'aac'
      localStorage.setItem('pmu-format-fallback', 'aac')
      this.load(this.state.index, true, this.position, true)
      return
    }
    this.errors++
    this.set({ error: song ? `Could not play “${song.title}”` : 'Playback failed', loading: false })
    // skip a broken file, but don't spin through a whole queue when the server is down
    if (this.errors <= 2 && this.state.index + 1 < this.state.queue.length) setTimeout(() => this.next(), 1200)
    else this.set({ playing: false })
  }

  // ---------- volume and ReplayGain ----------

  /** The element volume for a song: the player volume times its ReplayGain. */
  private targetVolume(song: Song | null): number {
    if (this.state.muted) return 0
    let v = this.state.volume
    const mode = this.settings?.replay_gain || 'auto'
    if (song && mode !== 'off') {
      const albumMode = mode === 'album' || (mode === 'auto' && this.state.source?.kind === 'album')
      const g = albumMode ? (song.rg_album ?? song.rg_track) : (song.rg_track ?? song.rg_album)
      // aim at -14 LUFS-ish: ReplayGain's reference is louder, so leave 3 dB of room for quiet tracks
      if (g != null) v *= Math.min(1, Math.pow(10, (g - 3) / 20) * 1.41)
    }
    return Math.max(0, Math.min(1, v))
  }
  private applyVolume() { if (!this.fading) this.el.volume = this.targetVolume(this.current) }

  // ---------- plays, reports, persistence ----------

  private sendListen(song: Song) {
    this.errors = 0
    api.post(`${V1}/listens`, { song_id: song.id, played_at: this.startedAt, ms_played: Math.round(this.heardMs), device: deviceName() }).catch(() => {})
  }

  /** At a song change: a song played long enough but not yet counted (short songs) counts now. */
  private finishListen() {
    const song = this.current
    if (!song || this.scrobbled) return
    const dur = song.duration || 0
    if (dur > 0 && dur <= 30 && this.heardMs / 1000 >= dur * 0.8) { this.scrobbled = true; this.sendListen(song) }
  }

  /** Tell the server (and so the other devices) what plays here. */
  report(withQueue = false, started = false) {
    clearTimeout(this.reportTimer)
    const song = this.current
    const ids = this.state.queue.map(s => s.id)
    const key = ids.join(',')
    const body: Record<string, unknown> = {
      device_id: deviceId(), device_name: deviceName(), song_id: song?.id || '', queue_index: this.state.index,
      position_ms: Math.round(this.position * 1000), playing: this.state.playing || (started && !this.el.paused), started,
    }
    if (withQueue || key !== this.reportedQueue) { body.queue = ids.slice(0, 5000); this.reportedQueue = key }
    api.post(`${V1}/now`, body).catch(() => {})
    this.reportTimer = window.setTimeout(() => this.report(), this.state.playing ? 15000 : 60000)
  }

  persist() {
    if (!this.state.queue.length) { localStorage.removeItem(LS.queue); return }
    try {
      localStorage.setItem(LS.queue, JSON.stringify({ ids: this.state.queue.map(s => s.id).slice(0, 3000), index: this.state.index, position: this.position, source: this.state.source, shuffle: this.state.shuffle }))
    } catch { /* full */ }
  }

  /** Bring back the last queue (paused) after a reload. */
  async restore() {
    if (this.restored) return
    this.restored = true
    const raw = localStorage.getItem(LS.queue)
    if (!raw) return
    try {
      const saved = JSON.parse(raw) as { ids: string[]; index: number; position: number; source: Source | null; shuffle: boolean }
      if (!saved.ids?.length) return
      const songs = await api.post<Song[]>(`${V1}/songs/by-id`, { ids: saved.ids })
      if (!songs.length || this.state.queue.length) return
      const cur = saved.ids[saved.index]
      const idx = Math.max(0, songs.findIndex(s => s.id === cur))
      this.restorePos = songs[idx]?.id === cur ? saved.position || 0 : 0
      this.offset = 0
      this.set({ queue: songs, index: idx, source: saved.source, shuffle: !!saved.shuffle })
      this.mediaMetadata(songs[idx])
    } catch { /* stale */ }
  }

  /** Continue what another device plays, here. */
  takeOver(songs: Song[], index: number, positionMs: number, name: string) {
    if (!songs.length) return
    this.unshuffled = null
    this.set({ queue: songs, index: Math.min(index, songs.length - 1), source: { kind: 'device', name }, shuffle: false })
    this.load(this.state.index, true, positionMs / 1000)
    this.report(true)
  }

  /** A command from another device (remote control). */
  command(action: string, value: number) {
    switch (action) {
      case 'play': this.play(); break
      case 'pause': case 'stop': this.pause(); break
      case 'toggle': this.toggle(); break
      case 'next': this.next(); break
      case 'previous': this.previous(); break
      case 'seek': this.seek(value); break
      case 'volume': this.setVolume(value); break
    }
  }

  // ---------- lock screen / media keys ----------

  private mediaSession() {
    const ms = navigator.mediaSession
    if (!ms) return
    const h = (a: MediaSessionAction, f: MediaSessionActionHandler) => { try { ms.setActionHandler(a, f) } catch { /* unsupported */ } }
    h('play', () => this.play())
    h('pause', () => this.pause())
    h('previoustrack', () => this.previous())
    h('nexttrack', () => this.next())
    h('seekto', d => { if (d.seekTime != null) this.seek(d.seekTime) })
    h('seekbackward', d => this.seek(this.position - (d.seekOffset || 10)))
    h('seekforward', d => this.seek(this.position + (d.seekOffset || 10)))
  }
  private mediaMetadata(song: Song) {
    const ms = navigator.mediaSession
    if (!ms || typeof MediaMetadata === 'undefined') return
    const art = albumCover(song.album_id, 512)
    ms.metadata = new MediaMetadata({ title: song.title, artist: song.artist, album: song.album, artwork: art ? [{ src: new URL(art, location.href).href, sizes: '512x512', type: 'image/jpeg' }] : [] })
    this.updatePositionState()
  }
  private updatePositionState() {
    const ms = navigator.mediaSession
    const dur = this.duration
    if (!ms?.setPositionState || !dur) return
    try { ms.setPositionState({ duration: dur, position: Math.min(this.position, dur), playbackRate: 1 }) } catch { /* bad values */ }
  }
}

const UNPLAYABLE = new Set(['dsf', 'dff', 'ape', 'wv', 'wma', 'mpc', 'tak', 'shn', 'tta'])

export function shuffleSongs<T>(xs: T[]): T[] {
  const out = xs.slice()
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[out[i], out[j]] = [out[j], out[i]]
  }
  return out
}

function playError(e: unknown): string {
  const n = (e as Error)?.name
  if (n === 'NotAllowedError') return 'Tap play to start (the browser blocked autoplay)'
  if (n === 'AbortError') return ''
  return (e as Error)?.message || 'Playback failed'
}

export const player = new Player()

// handy from the browser console (and the e2e suite)
;(window as unknown as { pocketPlayer: Player }).pocketPlayer = player
