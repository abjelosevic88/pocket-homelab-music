// The player: a bar at the bottom of every page (a mini player on phones),
// the full-screen Now Playing view with synced lyrics and the queue, other
// devices ("Playing on iPhone" → Play here), quality and the sleep timer.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, albumCover, clock, qualityOf } from '../lib/music'
import { player, remote, takeOver, useDevices, usePlayer, useProgress } from '../lib/player-react'
import type { Device, Lyrics, Song } from '../lib/types'
import { useApp } from '../lib/app-context'
import { Icon } from './icons'
import { Cover, StarButton, AddToPlaylist, startRadio, useCoverColor } from './music'
import { type Anchor, Menu, type MenuItem, rectOf, useIsMobile, useToast, errMsg } from './ui'

export function PlayerBar() {
  const p = usePlayer()
  const mobile = useIsMobile()
  const [open, setOpen] = useState<null | 'now' | 'lyrics' | 'queue'>(null)
  const { others } = useDevices()
  const remoteDev = others.find(d => d.active && d.playing && d.song)
  useEffect(() => {
    const h = (e: Event) => setOpen((e as CustomEvent).detail || 'now')
    window.addEventListener('pmu:nowplaying', h)
    return () => window.removeEventListener('pmu:nowplaying', h)
  }, [])
  useEffect(() => { document.body.classList.toggle('has-player', !!p.current || !!remoteDev) }, [p.current, remoteDev])
  if (!p.current) return remoteDev ? <RemoteBar d={remoteDev} /> : null
  return (
    <>
      {remoteDev && !p.playing && <RemoteBar d={remoteDev} slim />}
      {mobile ? <MiniBar onOpen={() => setOpen('now')} /> : <DesktopBar onOpen={v => setOpen(v)} openView={open} />}
      {open && <NowPlaying view={open} onView={setOpen} onClose={() => setOpen(null)} />}
    </>
  )
}

// ---------- bars ----------

function Progress({ className = '', big = false }: { className?: string; big?: boolean }) {
  const { position, duration, buffered } = useProgress()
  const [drag, setDrag] = useState<number | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  const at = (clientX: number) => {
    const r = ref.current!.getBoundingClientRect()
    return Math.max(0, Math.min(1, (clientX - r.left) / r.width)) * (duration || 0)
  }
  const pos = drag ?? position
  const pct = duration ? (pos / duration) * 100 : 0
  const buf = duration ? Math.min(100, (buffered / duration) * 100) : 0
  return (
    <div className={`progress ${big ? 'big' : ''} ${className}`}>
      <span className="t tnum">{clock(pos)}</span>
      <div className="track" ref={ref} role="slider" aria-label="Position" aria-valuemin={0} aria-valuemax={Math.round(duration)} aria-valuenow={Math.round(pos)} tabIndex={0}
        onKeyDown={e => { if (e.key === 'ArrowRight') player.seek(position + 5); if (e.key === 'ArrowLeft') player.seek(position - 5) }}
        onPointerDown={e => { (e.target as HTMLElement).setPointerCapture(e.pointerId); setDrag(at(e.clientX)) }}
        onPointerMove={e => { if (drag !== null) setDrag(at(e.clientX)) }}
        onPointerUp={e => { if (drag !== null) { player.seek(at(e.clientX)); setDrag(null) } }}>
        <span className="buf" style={{ width: `${buf}%` }} />
        <span className="fill" style={{ width: `${pct}%` }} />
        <span className="knob" style={{ left: `${pct}%` }} />
      </div>
      <span className="t tnum">{clock(duration)}</span>
    </div>
  )
}

function Transport({ size = 18, big = false }: { size?: number; big?: boolean }) {
  const p = usePlayer()
  return (
    <div className={`transport ${big ? 'big' : ''}`}>
      <button type="button" className={`btn ghost icon-only tp-mode ${p.shuffle ? 'on' : ''}`} aria-pressed={p.shuffle} aria-label="Shuffle" title="Shuffle (s)" onClick={() => player.toggleShuffle()}><Icon name="shuffle" size={size - 2} /></button>
      <button type="button" className="btn ghost icon-only" aria-label="Previous" title="Previous (←)" onClick={() => player.previous()}><Icon name="skip-back" size={size} /></button>
      <button type="button" className="tp-play" aria-label={p.playing ? 'Pause' : 'Play'} title={p.playing ? 'Pause (space)' : 'Play (space)'} onClick={() => player.toggle()}>
        {p.loading && p.playing ? <span className="spinner" /> : <Icon name={p.playing ? 'pause' : 'play'} size={big ? size + 8 : size} />}
      </button>
      <button type="button" className="btn ghost icon-only" aria-label="Next" title="Next (→)" onClick={() => player.next()}><Icon name="skip-forward" size={size} /></button>
      <button type="button" className={`btn ghost icon-only tp-mode ${p.repeat !== 'off' ? 'on' : ''}`} aria-label={`Repeat: ${p.repeat}`} title={`Repeat: ${p.repeat} (r)`} onClick={() => player.cycleRepeat()}><Icon name={p.repeat === 'one' ? 'repeat-1' : 'repeat'} size={size - 2} /></button>
    </div>
  )
}

function Volume() {
  const p = usePlayer()
  const v = p.muted ? 0 : p.volume
  return (
    <div className="volume">
      <button type="button" className="btn ghost icon-only" aria-label={p.muted ? 'Unmute' : 'Mute'} title="Mute (m)" onClick={() => player.toggleMute()}><Icon name={v === 0 ? 'volume-x' : v < 0.5 ? 'volume-1' : 'volume'} size={17} /></button>
      <input type="range" min={0} max={1} step={0.01} value={v} aria-label="Volume" onChange={e => player.setVolume(Number(e.target.value))} style={{ ['--v' as string]: `${v * 100}%` }} />
    </div>
  )
}

function DesktopBar({ onOpen, openView }: { onOpen: (v: 'now' | 'lyrics' | 'queue') => void; openView: string | null }) {
  const p = usePlayer()
  const s = p.current!
  const navigate = useNavigate()
  return (
    <div className="player-bar" role="region" aria-label="Player">
      <div className="pb-left">
        <button type="button" className="pb-art" onClick={() => onOpen('now')} aria-label="Open Now Playing" title="Now Playing (n)"><Cover src={albumCover(s.album_id, 120)} size={52} label={s.album} /><Icon name="maximize" size={14} className="pb-expand" /></button>
        <div className="pb-meta">
          <Link to={`/albums/${s.album_id}`} className="pb-title ellipsis">{s.title}</Link>
          <span className="pb-artist ellipsis">{s.artist_id ? <Link to={`/artists/${s.artist_id}`}>{s.artist}</Link> : s.artist}</span>
        </div>
        <StarButton kind="song" id={s.id} starred={!!s.starred_at} size={16} />
      </div>
      <div className="pb-center">
        <Transport />
        <Progress />
      </div>
      <div className="pb-right">
        {p.source && <button type="button" className="pb-source ellipsis" title={`Playing from ${p.source.name}`} onClick={() => p.source?.link && navigate(p.source.link)}>{p.source.name}</button>}
        <button type="button" className={`btn ghost icon-only ${openView === 'lyrics' ? 'active' : ''}`} aria-label="Lyrics" title="Lyrics (l)" onClick={() => onOpen('lyrics')}><Icon name="mic" size={17} /></button>
        <button type="button" className={`btn ghost icon-only ${openView === 'queue' ? 'active' : ''}`} aria-label="Queue" title="Queue (q)" onClick={() => onOpen('queue')}><Icon name="queue" size={17} /></button>
        <DevicesButton />
        <PlayerMenu />
        <Volume />
      </div>
      {p.error && <div className="pb-error"><Icon name="alert" size={14} />{p.error}</div>}
    </div>
  )
}

function MiniBar({ onOpen }: { onOpen: () => void }) {
  const p = usePlayer()
  const s = p.current!
  const { position, duration } = useProgress()
  const color = useCoverColor(albumCover(s.album_id, 64))
  return (
    <div className="mini-bar" role="region" aria-label="Player" style={color ? { ['--art' as string]: color } : undefined} onClick={e => { if (!(e.target as HTMLElement).closest('button')) onOpen() }}>
      <Cover src={albumCover(s.album_id, 120)} size={42} label={s.album} />
      <div className="pb-meta grow">
        <span className="pb-title ellipsis">{s.title}</span>
        <span className="pb-artist ellipsis">{p.error || s.artist}</span>
      </div>
      <DevicesButton compact />
      <button type="button" className="tp-play sm" aria-label={p.playing ? 'Pause' : 'Play'} onClick={() => player.toggle()}><Icon name={p.playing ? 'pause' : 'play'} size={18} /></button>
      <button type="button" className="btn ghost icon-only" aria-label="Next" onClick={() => player.next()}><Icon name="skip-forward" size={18} /></button>
      <span className="mini-progress" style={{ width: `${duration ? (position / duration) * 100 : 0}%` }} />
    </div>
  )
}

/** Another device of the person plays: show it, control it, take it over. */
function RemoteBar({ d, slim }: { d: Device; slim?: boolean }) {
  const { toast } = useToast()
  const s = d.song!
  return (
    <div className={`remote-bar ${slim ? 'slim' : ''}`} role="region" aria-label={`Playing on ${d.name}`}>
      <Icon name="speaker" size={16} className="remote-ico" />
      {!slim && <Cover src={albumCover(s.album_id, 80)} size={40} label={s.album} />}
      <div className="grow ellipsis"><b>Playing on {d.name}</b><span className="muted"> · {s.title} — {s.artist}</span></div>
      <button type="button" className="btn ghost icon-only sm" aria-label={`Pause on ${d.name}`} title={`Pause on ${d.name}`} onClick={() => remote(d.id, 'pause')}><Icon name="pause" size={15} /></button>
      <button type="button" className="btn ghost icon-only sm" aria-label={`Next on ${d.name}`} title={`Next on ${d.name}`} onClick={() => remote(d.id, 'next')}><Icon name="skip-forward" size={15} /></button>
      <button type="button" className="btn sm primary" onClick={() => takeOver(d).catch(e => toast(errMsg(e), 'err'))}><Icon name="play" size={13} />Play here</button>
    </div>
  )
}

function DevicesButton({ compact }: { compact?: boolean }) {
  const { others } = useDevices()
  const { toast } = useToast()
  const [a, setA] = useState<Anchor | null>(null)
  const recent = others.filter(d => d.song && Date.now() - Date.parse(d.updated_at) < 7 * 864e5).slice(0, 6)
  if (!recent.length) return null
  const live = recent.some(d => d.active && d.playing)
  const items: MenuItem[] = [{ heading: 'Your other devices' }, ...recent.map(d => ({
    label: <span className="dev-item"><span className="ellipsis"><b>{d.name}</b></span><span className="muted xsmall ellipsis">{d.active && d.playing ? '▶ ' : ''}{d.song?.title} — {d.song?.artist}</span></span>,
    icon: d.active && d.playing ? 'speaker' : 'devices',
    onSelect: () => takeOver(d).then(() => toast(`Continuing from ${d.name}`)).catch(e => toast(errMsg(e), 'err')),
    hint: 'Play here',
  }))]
  return <>
    <button type="button" className={`btn ghost icon-only ${live ? 'live' : ''} ${compact ? 'sm' : ''}`} aria-label="Devices" title="Your devices" onClick={e => { e.stopPropagation(); setA(a ? null : rectOf(e.currentTarget)) }}><Icon name="devices" size={17} /></button>
    {a && <Menu anchor={a} items={items} align="right" width={300} onClose={() => setA(null)} />}
  </>
}

const QUALITIES = [['', 'Use my setting'], ['original', 'Original file'], ['320', 'High (320 kbps)'], ['256', 'Good (256 kbps)'], ['192', 'Normal (192 kbps)'], ['128', 'Data saver (128 kbps)']] as const

function PlayerMenu() {
  const p = usePlayer()
  const [a, setA] = useState<Anchor | null>(null)
  const [addTo, setAddTo] = useState<Song[] | null>(null)
  const navigate = useNavigate()
  const s = p.current
  const sleepLeft = p.sleepAt > 0 ? Math.ceil((p.sleepAt - Date.now()) / 60000) : 0
  const items = (): MenuItem[] => [
    ...(s ? [
      { label: 'Start radio from this song', icon: 'radio', onSelect: () => startRadio({ song: s.id }, s.title + ' radio', s) },
      { label: 'Add to playlist…', icon: 'playlist', onSelect: () => setAddTo([s]) },
      { label: 'Go to album', icon: 'album', onSelect: () => navigate(`/albums/${s.album_id}`) },
      'sep' as const,
    ] : []),
    { heading: `Sleep timer${sleepLeft ? ` · ${sleepLeft} min left` : p.sleepAt === -1 ? ' · after this song' : ''}` },
    { label: 'Off', checked: p.sleepAt === 0, onSelect: () => player.sleep(0) },
    { label: 'In 15 minutes', checked: false, onSelect: () => player.sleep(15) },
    { label: 'In 30 minutes', checked: false, onSelect: () => player.sleep(30) },
    { label: 'In 1 hour', checked: false, onSelect: () => player.sleep(60) },
    { label: 'After this song', checked: p.sleepAt === -1, onSelect: () => player.sleep(-1) },
    'sep',
    { heading: `Quality on this device${s ? ` · ${qualityOf(s)}` : ''}` },
    ...QUALITIES.map(([v, l]) => ({ label: l, checked: p.quality === v, onSelect: () => player.setQuality(v) })),
  ]
  return <>
    <button type="button" className={`btn ghost icon-only ${p.sleepAt ? 'live' : ''}`} aria-label="More" title="Sleep timer, quality…" aria-haspopup="menu" onClick={e => setA(a ? null : rectOf(e.currentTarget))}><Icon name={p.sleepAt ? 'moon' : 'more'} size={17} /></button>
    {a && <Menu anchor={a} items={items()} align="right" width={250} onClose={() => setA(null)} />}
    {addTo && <AddToPlaylist songs={addTo} onClose={() => setAddTo(null)} />}
  </>
}

// ---------- Now Playing ----------

function NowPlaying({ view, onView, onClose }: { view: 'now' | 'lyrics' | 'queue'; onView: (v: 'now' | 'lyrics' | 'queue') => void; onClose: () => void }) {
  const p = usePlayer()
  const s = p.current
  const mobile = useIsMobile()
  const color = useCoverColor(s ? albumCover(s.album_id, 64) : '')
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); onClose() } }
    window.addEventListener('keydown', h, true)
    return () => window.removeEventListener('keydown', h, true)
  }, [onClose])
  if (!s) return null
  return createPortal(
    <div className={`np ${mobile ? 'mobile' : ''}`} style={color ? { ['--art' as string]: color } : undefined} role="dialog" aria-label="Now Playing">
      <div className="np-bg" />
      <header className="np-head">
        <button type="button" className="btn ghost icon-only" aria-label="Close" title="Close (esc)" onClick={onClose}><Icon name={mobile ? 'chevron-down' : 'x'} size={20} /></button>
        <div className="np-from ellipsis">{p.source ? <><span className="muted">Playing from</span> <b>{p.source.name}</b></> : 'Now playing'}</div>
        <div className="segmented sm np-tabs">
          {(['now', 'lyrics', 'queue'] as const).map(v => <button key={v} type="button" className={view === v ? 'active' : ''} onClick={() => onView(v)}>{v === 'now' ? 'Now' : v === 'lyrics' ? 'Lyrics' : 'Up next'}</button>)}
        </div>
      </header>
      <div className={`np-body view-${view}`}>
        <div className="np-art-col">
          <Cover src={albumCover(s.album_id, 900)} className="np-art" label={s.album} />
          <div className="np-meta">
            <div className="grow">
              <h2 className="ellipsis">{s.title}</h2>
              <p className="ellipsis"><Link to={`/artists/${s.artist_id}`} onClick={onClose}>{s.artist}</Link> · <Link to={`/albums/${s.album_id}`} onClick={onClose}>{s.album}</Link>{s.year ? ` · ${s.year}` : ''}</p>
            </div>
            <StarButton kind="song" id={s.id} starred={!!s.starred_at} size={20} />
          </div>
          <Progress big />
          <div className="np-controls">
            <Transport size={24} big />
          </div>
          <div className="np-foot">
            <span className="np-q">{qualityOf(s)}{p.quality && p.quality !== 'original' ? ` → ${p.quality} kbps` : ''}</span>
            <span className="grow" />
            {!mobile && <Volume />}
            <DevicesButton />
            <PlayerMenu />
          </div>
        </div>
        {(view === 'lyrics' || !mobile) && view !== 'queue' && <LyricsPane song={s} onlyLyrics={view === 'lyrics'} />}
        {view === 'queue' && <QueuePane />}
      </div>
    </div>, document.body)
}

function LyricsPane({ song, onlyLyrics }: { song: Song; onlyLyrics: boolean }) {
  const [ly, setLy] = useState<Lyrics | null>(null)
  const [err, setErr] = useState('')
  const { position } = useProgress()
  const box = useRef<HTMLDivElement>(null)
  const [follow, setFollow] = useState(true)
  useEffect(() => {
    setLy(null); setErr('')
    api.get<Lyrics>(`${V1}/lyrics/${encodeURIComponent(song.id)}`).then(setLy).catch(e => setErr(errMsg(e)))
  }, [song.id])
  const ms = position * 1000
  const active = useMemo(() => {
    if (!ly?.synced) return -1
    let i = -1
    for (let k = 0; k < ly.lines.length; k++) { if (ly.lines[k].t <= ms + 250) i = k; else break }
    return i
  }, [ly, ms])
  useEffect(() => {
    if (!follow || active < 0) return
    const el = box.current?.querySelector(`[data-l="${active}"]`) as HTMLElement | null
    el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }, [active, follow])
  useEffect(() => {
    if (follow) return
    const t = setTimeout(() => setFollow(true), 5000)
    return () => clearTimeout(t)
  }, [follow])
  if (!onlyLyrics && ly && !ly.lines.length) return <QueuePane />
  return (
    <div className={`np-lyrics ${ly?.synced ? 'synced' : ''}`} ref={box} onWheel={() => setFollow(false)} onTouchMove={() => setFollow(false)}>
      {!ly && !err && <div className="loading"><span className="spinner" /> Lyrics…</div>}
      {err && <p className="muted">{err}</p>}
      {ly && !ly.lines.length && <div className="np-nolyrics"><Icon name="mic" size={28} /><p>No lyrics for this song.</p><p className="muted small">Lyrics come from the files (embedded or a .lrc next to the song) and, when switched on in Settings → Playback, from LRCLIB.</p></div>}
      {ly?.lines.map((l, i) => (
        <p key={i} data-l={i} className={`ly ${i === active ? 'on' : ''} ${i < active ? 'past' : ''} ${l.text ? '' : 'gap'}`}
          onClick={() => { if (ly.synced && l.t >= 0) player.seek(l.t / 1000) }}>{l.text || '♪'}</p>
      ))}
      {ly?.source && <p className="ly-src muted xsmall">{ly.source === 'lrclib' ? 'Lyrics from LRCLIB' : 'Lyrics from your library'}{ly.synced ? ' · tap a line to jump' : ''}</p>}
    </div>
  )
}

function QueuePane() {
  const p = usePlayer()
  const [drag, setDrag] = useState<number | null>(null)
  const [over, setOver] = useState<number | null>(null)
  const list = useRef<HTMLDivElement>(null)
  useEffect(() => { list.current?.querySelector('.q-row.current')?.scrollIntoView({ block: 'center' }) }, [])
  const upcoming = p.queue.length - p.index - 1
  return (
    <div className="np-queue">
      <div className="q-head">
        <h3>Up next <span className="muted small">{upcoming} song{upcoming === 1 ? '' : 's'}</span></h3>
        {upcoming > 0 && <button type="button" className="btn ghost sm" onClick={() => player.clearUpcoming()}>Clear</button>}
      </div>
      <div className="q-list" ref={list}>
        {p.queue.map((s, i) => (
          <div key={`${s.id}-${i}`} className={`q-row ${i === p.index ? 'current' : ''} ${i < p.index ? 'past' : ''} ${over === i ? 'over' : ''}`}
            draggable={i !== p.index} onDragStart={() => setDrag(i)} onDragOver={e => { e.preventDefault(); setOver(i) }} onDragEnd={() => { setDrag(null); setOver(null) }}
            onDrop={() => { if (drag !== null && over !== null) player.move(drag, over); setDrag(null); setOver(null) }}
            onDoubleClick={() => player.jump(i)}>
            <Icon name="grip" size={14} className="q-grip" />
            <button type="button" className="q-play" onClick={() => player.jump(i)} aria-label={`Play ${s.title}`}><Cover src={albumCover(s.album_id, 80)} size={36} label={s.album} /></button>
            <div className="grow" onClick={() => player.jump(i)}>
              <div className="ellipsis q-title">{s.title}</div>
              <div className="ellipsis muted small">{s.artist}</div>
            </div>
            <span className="muted small tnum">{clock(s.duration)}</span>
            {i !== p.index && <button type="button" className="btn ghost icon-only sm" aria-label="Remove from queue" onClick={() => player.removeAt(i)}><Icon name="x" size={14} /></button>}
          </div>
        ))}
      </div>
    </div>
  )
}

/** Open Now Playing from anywhere. */
export const openNowPlaying = (view: 'now' | 'lyrics' | 'queue' = 'now') => window.dispatchEvent(new CustomEvent('pmu:nowplaying', { detail: view }))

/** Space, ←/→, n, l, q, s, r, m — the player's keys. */
export function usePlayerKeys() {
  const { settings } = useApp()
  useEffect(() => { if (settings) player.configure(settings) }, [settings])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || document.querySelector('.modal-backdrop, .menu, .palette-backdrop')) return
      if (!player.current) return
      switch (e.key) {
        case ' ': if (!t.closest('button, a, [role="row"]')) { e.preventDefault(); player.toggle() } break
        case 'ArrowRight': if (e.shiftKey) { e.preventDefault(); player.next() } else if (!t.closest('[role="slider"]')) { e.preventDefault(); player.seek(player.position + 10) } break
        case 'ArrowLeft': if (e.shiftKey) { e.preventDefault(); player.previous() } else if (!t.closest('[role="slider"]')) { e.preventDefault(); player.seek(player.position - 10) } break
        case 'n': e.preventDefault(); openNowPlaying('now'); break
        case 'l': e.preventDefault(); openNowPlaying('lyrics'); break
        case 'q': e.preventDefault(); openNowPlaying('queue'); break
        case 's': e.preventDefault(); player.toggleShuffle(); break
        case 'r': e.preventDefault(); player.cycleRepeat(); break
        case 'm': e.preventDefault(); player.toggleMute(); break
        case 'k': e.preventDefault(); player.toggle(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [])
}

