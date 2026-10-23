// Building blocks of every music page: covers, cards, shelves, the song table,
// the song menu and "Add to playlist".
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, albumCover, artistImage, clock, coverURL, hueFor, qualityOf } from '../lib/music'
import { player, useIsCurrent, usePlayer } from '../lib/player-react'
import type { Source } from '../lib/player'
import type { Album, Artist, Mix, Playlist, Song } from '../lib/types'
import { Icon } from './icons'
import { type Anchor, Menu, type MenuItem, Modal, Spinner, errMsg, rectOf, useToast } from './ui'

// ---------- covers ----------

export function Cover({ src, alt = '', size, round, className = '', label, fallbackIcon = 'disc' }: { src: string; alt?: string; size?: number; round?: boolean; className?: string; label?: string; fallbackIcon?: string }) {
  const [failed, setFailed] = useState(!src)
  const [loaded, setLoaded] = useState(false)
  useEffect(() => { setFailed(!src); setLoaded(false) }, [src])
  const hue = hueFor(label || alt || src)
  const style: React.CSSProperties = { ...(size ? { width: size, height: size } : {}), ['--h' as string]: hue }
  return (
    <span className={`cover ${round ? 'round' : ''} ${loaded ? 'loaded' : ''} ${failed ? 'none' : ''} ${className}`} style={style}>
      {failed
        ? <span className="cover-fallback">{label ? <b>{initialsOf(label)}</b> : <Icon name={fallbackIcon} size={Math.max(14, (size || 120) / 3.4)} strokeWidth={1.4} />}</span>
        : <img src={src} alt={alt} loading="lazy" decoding="async" draggable={false} onLoad={() => setLoaded(true)} onError={() => setFailed(true)} />}
    </span>
  )
}

function initialsOf(s: string) {
  const w = s.replace(/[^\p{L}\p{N} ]/gu, '').split(/\s+/).filter(Boolean)
  return ((w[0]?.[0] || '') + (w[1]?.[0] || '')).toUpperCase() || '♪'
}

/** 2×2 album collage (mixes, playlists). */
export function Collage({ albums, size, color }: { albums: string[]; size?: number; color?: string }) {
  const a = albums.slice(0, 4)
  if (a.length < 4) return <span className="collage single" style={{ ...(size ? { width: size, height: size } : {}), ['--mix' as string]: color }}>
    {a[0] ? <Cover src={albumCover(a[0], 400)} /> : <span className="collage-empty"><Icon name="music" size={28} /></span>}
  </span>
  return (
    <span className="collage" style={{ ...(size ? { width: size, height: size } : {}), ['--mix' as string]: color }}>
      {a.map(id => <Cover key={id} src={albumCover(id, 200)} />)}
    </span>
  )
}

/** The average colour of a cover, for page heroes. */
const colorCache = new Map<string, string>()
export function useCoverColor(src: string): string {
  const [c, setC] = useState(() => colorCache.get(src) || '')
  useEffect(() => {
    if (!src) return
    if (colorCache.has(src)) { setC(colorCache.get(src)!); return }
    const img = new Image()
    img.onload = () => {
      try {
        const cv = document.createElement('canvas')
        cv.width = cv.height = 24
        const g = cv.getContext('2d', { willReadFrequently: true })!
        g.drawImage(img, 0, 0, 24, 24)
        const d = g.getImageData(0, 0, 24, 24).data
        let r = 0, gg = 0, b = 0, n = 0
        for (let i = 0; i < d.length; i += 4) {
          const mx = Math.max(d[i], d[i + 1], d[i + 2]), mn = Math.min(d[i], d[i + 1], d[i + 2])
          const w = 1 + (mx - mn) / 40 // colourful pixels count more than grey ones
          r += d[i] * w; gg += d[i + 1] * w; b += d[i + 2] * w; n += w
        }
        const col = `${Math.round(r / n)}, ${Math.round(gg / n)}, ${Math.round(b / n)}`
        colorCache.set(src, col)
        setC(col)
      } catch { /* tainted */ }
    }
    img.src = src
  }, [src])
  return c
}

// ---------- cards ----------

export function PlayFab({ onPlay, label = 'Play', busy }: { onPlay: (e: React.MouseEvent) => void; label?: string; busy?: boolean }) {
  return <button type="button" className="play-fab" aria-label={label} title={label} onClick={e => { e.preventDefault(); e.stopPropagation(); onPlay(e) }}>
    {busy ? <Spinner size={16} /> : <Icon name="play" size={18} />}
  </button>
}

async function playAlbum(id: string, shuffle = false) {
  const r = await api.get<{ album: Album; songs: Song[] }>(`${V1}/albums/${id}`)
  player.playSongs(r.songs, shuffle ? -1 : 0, { kind: 'album', name: r.album.name, link: `/albums/${id}` }, shuffle)
}

export function AlbumCard({ a, sub }: { a: Album; sub?: React.ReactNode }) {
  const [busy, setBusy] = useState(false)
  const { toast } = useToast()
  return (
    <Link to={`/albums/${a.id}`} className="card-tile" title={`${a.name} — ${a.artist}`}>
      <span className="tile-art">
        <Cover src={albumCover(a.id, 300)} label={a.name} />
        <PlayFab busy={busy} label={`Play ${a.name}`} onPlay={async () => { setBusy(true); try { await playAlbum(a.id) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) } }} />
        {a.starred_at && <span className="tile-star"><Icon name="heart-filled" size={12} /></span>}
      </span>
      <span className="tile-title ellipsis">{a.name}</span>
      <span className="tile-sub ellipsis">{sub ?? <>{a.artist}{a.year ? ` · ${a.year}` : ''}</>}</span>
    </Link>
  )
}

export function ArtistCard({ a, sub }: { a: Pick<Artist, 'id' | 'name'> & Partial<Artist>; sub?: React.ReactNode }) {
  return (
    <Link to={`/artists/${a.id}`} className="card-tile artist" title={a.name}>
      <span className="tile-art round"><Cover src={artistImage(a.id, 300)} label={a.name} round /></span>
      <span className="tile-title ellipsis center">{a.name}</span>
      <span className="tile-sub ellipsis center">{sub ?? (a.album_count ? `${a.album_count} album${a.album_count === 1 ? '' : 's'}` : a.song_count ? `${a.song_count} songs` : 'Artist')}</span>
    </Link>
  )
}

export function MixCard({ m, onPlay }: { m: Mix; onPlay: () => Promise<void> }) {
  const [busy, setBusy] = useState(false)
  return (
    <Link to={`/mixes/${m.key}`} className="card-tile mix" title={m.description}>
      <span className="tile-art">
        <Collage albums={m.covers} color={m.color} />
        <span className="mix-label" style={{ ['--mix' as string]: m.color }}>{m.name}</span>
        <PlayFab busy={busy} label={`Play ${m.name}`} onPlay={async () => { setBusy(true); try { await onPlay() } finally { setBusy(false) } }} />
      </span>
      <span className="tile-sub two-lines">{m.description}</span>
    </Link>
  )
}

export function PlaylistCard({ p }: { p: Playlist }) {
  return (
    <Link to={`/playlists/${p.id}`} className="card-tile" title={p.name}>
      <span className="tile-art">
        <Cover src={coverURL(p.coverArt || 'pl-' + p.id, 300)} label={p.name} fallbackIcon="playlist" />
        {p.smart_id ? <span className="tile-badge"><Icon name="zap" size={11} />Smart</span> : null}
      </span>
      <span className="tile-title ellipsis">{p.name}</span>
      <span className="tile-sub ellipsis">{p.songCount} songs{!p.mine && p.owner ? ` · by ${p.owner}` : ''}</span>
    </Link>
  )
}

/** A titled row of cards that scrolls sideways. */
export function Shelf({ title, sub, to, children, action }: { title: React.ReactNode; sub?: React.ReactNode; to?: string; children: React.ReactNode; action?: React.ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const scroll = (d: number) => ref.current?.scrollBy({ left: d * ref.current.clientWidth * 0.8, behavior: 'smooth' })
  return (
    <section className="shelf">
      <div className="shelf-head">
        <div className="grow"><h2>{to ? <Link to={to}>{title}</Link> : title}</h2>{sub && <p className="muted small">{sub}</p>}</div>
        {action}
        {to && <Link to={to} className="btn ghost sm">See all</Link>}
        <span className="shelf-arrows">
          <button type="button" className="btn ghost icon-only sm" aria-label="Scroll left" onClick={() => scroll(-1)}><Icon name="chevron-left" size={16} /></button>
          <button type="button" className="btn ghost icon-only sm" aria-label="Scroll right" onClick={() => scroll(1)}><Icon name="chevron-right" size={16} /></button>
        </span>
      </div>
      <div className="shelf-row" ref={ref}>{children}</div>
    </section>
  )
}

// ---------- stars ----------

export function StarButton({ kind, id, starred, onChange, size = 16, className = '' }: { kind: 'song' | 'album' | 'artist'; id: string; starred: boolean; onChange?: (on: boolean) => void; size?: number; className?: string }) {
  const [on, setOn] = useState(starred)
  const { toast } = useToast()
  useEffect(() => setOn(starred), [starred])
  const flip = async (e: React.MouseEvent) => {
    e.preventDefault(); e.stopPropagation()
    const next = !on
    setOn(next)
    try { await api.post(`${V1}/star`, { kind, id, on: next }); onChange?.(next) } catch (err) { setOn(!next); toast(errMsg(err), 'err') }
  }
  return <button type="button" className={`btn ghost icon-only star-btn ${on ? 'on' : ''} ${className}`} aria-pressed={on} aria-label={on ? 'Loved — remove' : 'Love'} title={on ? 'Loved' : 'Love'} onClick={flip}>
    <Icon name={on ? 'heart-filled' : 'heart'} size={size} />
  </button>
}

// ---------- the song menu ----------

export function songMenu(s: Song, nav: (to: string) => void, opts: { onAddTo: () => void; onRemove?: () => void; toast: (m: string) => void }): MenuItem[] {
  return [
    { label: 'Play next', icon: 'queue', onSelect: () => { player.playNext([s]); opts.toast(`“${s.title}” plays next`) } },
    { label: 'Add to queue', icon: 'list-plus', onSelect: () => { player.addToQueue([s]); opts.toast('Added to the queue') } },
    { label: 'Start radio', icon: 'radio', onSelect: () => startRadio({ song: s.id }, s.title + ' radio', s) },
    { label: 'Add to playlist…', icon: 'playlist', onSelect: opts.onAddTo },
    'sep',
    { label: 'Go to album', icon: 'album', onSelect: () => nav(`/albums/${s.album_id}`), disabled: !s.album_id },
    { label: 'Go to artist', icon: 'user', onSelect: () => nav(`/artists/${s.artist_id || s.album_artist_id}`), disabled: !(s.artist_id || s.album_artist_id) },
    { label: 'Download', icon: 'download', onSelect: () => { location.href = `${V1}/download/${encodeURIComponent(s.id)}` } },
    ...(opts.onRemove ? ['sep' as const, { label: 'Remove from playlist', icon: 'trash', danger: true, onSelect: opts.onRemove }] : []),
  ]
}

/** Radio from a seed: the seed song plays at once, the rest arrives a moment later. */
export async function startRadio(seed: Record<string, string>, name: string, first?: Song) {
  if (first) player.playSongs([first], 0, { kind: 'radio', name })
  const q = new URLSearchParams(seed).toString()
  const r = await api.get<{ name: string; songs: Song[] }>(`${V1}/radio?${q}`)
  if (first) {
    if (player.current?.id === first.id) player.addToQueue(r.songs.filter(x => x.id !== first.id))
  } else player.playSongs(r.songs, 0, { kind: 'radio', name: r.name })
}

// ---------- the song table ----------

export interface SongTableProps {
  songs: Song[]; source: Source; numbered?: 'track' | 'index'; showAlbum?: boolean; showArtist?: boolean; showCover?: boolean
  showPlays?: boolean; discs?: boolean; onRemove?: (i: number) => void; compact?: boolean
}

export function SongTable({ songs, source, numbered = 'index', showAlbum = true, showArtist = true, showCover = false, showPlays = false, discs = false, onRemove, compact }: SongTableProps) {
  const navigate = useNavigate()
  const { toast } = useToast()
  const [menu, setMenu] = useState<{ a: Anchor; i: number } | null>(null)
  const [addTo, setAddTo] = useState<Song[] | null>(null)
  const multiDisc = discs && new Set(songs.map(s => s.disc)).size > 1
  const play = (i: number) => player.playSongs(songs, i, source)
  let lastDisc = -1
  return (
    <div className={`songs ${compact ? 'compact' : ''}`} role="table" aria-label="Songs">
      {songs.map((s, i) => {
        const head = multiDisc && s.disc !== lastDisc ? <div className="disc-head" key={`d${s.disc}`}><Icon name="disc" size={14} />Disc {s.disc || 1}</div> : null
        lastDisc = s.disc
        return (
          <React.Fragment key={`${s.id}-${i}`}>
            {head}
            <SongRow s={s} i={i} n={numbered === 'track' ? s.track || i + 1 : i + 1} showAlbum={showAlbum} showArtist={showArtist} showCover={showCover} showPlays={showPlays}
              onPlay={() => play(i)} onMenu={a => setMenu({ a, i })} />
          </React.Fragment>
        )
      })}
      {menu && <Menu anchor={menu.a} align="right" width={220} onClose={() => setMenu(null)}
        items={songMenu(songs[menu.i], navigate, { toast: m => toast(m), onAddTo: () => setAddTo([songs[menu.i]]), onRemove: onRemove ? () => onRemove(menu.i) : undefined })} />}
      {addTo && <AddToPlaylist songs={addTo} onClose={() => setAddTo(null)} />}
    </div>
  )
}

function SongRow({ s, i, n, showAlbum, showArtist, showCover, showPlays, onPlay, onMenu }: { s: Song; i: number; n: number; showAlbum: boolean; showArtist: boolean; showCover: boolean; showPlays: boolean; onPlay: () => void; onMenu: (a: Anchor) => void }) {
  const cur = useIsCurrent(s.id)
  const { playing } = usePlayer()
  return (
    <div className={`song-row ${cur ? 'current' : ''}`} role="row" tabIndex={0} data-i={i}
      onClick={e => { if (!(e.target as HTMLElement).closest('a, button')) onPlay() }}
      onKeyDown={e => { if (e.key === 'Enter') onPlay() }}
      onContextMenu={e => { e.preventDefault(); onMenu({ x: e.clientX, y: e.clientY, w: 0, h: 0 }) }}>
      <span className="song-n" role="cell">
        {cur && playing ? <span className="eq" aria-label="Playing"><i /><i /><i /></span> : <span className="n tnum">{n}</span>}
        <Icon name="play" size={13} className="n-play" />
      </span>
      {showCover && <Cover src={albumCover(s.album_id, 80)} size={38} className="song-cover" label={s.album} />}
      <span className="song-main" role="cell">
        <span className="song-title ellipsis">{s.title}</span>
        {showArtist && <span className="song-artist ellipsis">{s.artist_id ? <Link to={`/artists/${s.artist_id}`}>{s.artist}</Link> : s.artist}</span>}
      </span>
      {showAlbum && <span className="song-album ellipsis" role="cell"><Link to={`/albums/${s.album_id}`}>{s.album}</Link></span>}
      {showPlays && <span className="song-plays tnum muted" role="cell" title="Your plays">{s.plays ? s.plays : ''}</span>}
      <span className="song-q" role="cell" title={qualityOf(s)}>{qualityOf(s).split(' ')[0]}</span>
      <StarButton kind="song" id={s.id} starred={!!s.starred_at} size={15} className="song-star" />
      <span className="song-dur tnum" role="cell">{clock(s.duration)}</span>
      <button type="button" className="btn ghost icon-only sm song-more" aria-label="More" onClick={e => { e.stopPropagation(); onMenu(rectOf(e.currentTarget)) }}><Icon name="more" size={16} /></button>
    </div>
  )
}

// ---------- add to playlist ----------

export function AddToPlaylist({ songs, onClose }: { songs: Song[]; onClose: () => void }) {
  const { toast } = useToast()
  const [lists, setLists] = useState<Playlist[] | null>(null)
  const [name, setName] = useState('')
  const [q, setQ] = useState('')
  useEffect(() => { api.get<Playlist[]>(`${V1}/playlists`).then(ls => setLists(ls.filter(p => p.mine && !p.smart_id))).catch(e => toast(errMsg(e), 'err')) }, [toast])
  const shown = useMemo(() => (lists || []).filter(p => !q || p.name.toLowerCase().includes(q.toLowerCase())), [lists, q])
  const ids = songs.map(s => s.id)
  const add = async (p: Playlist) => {
    try { await api.put(`${V1}/playlists/${p.id}`, { add: ids }); toast(`Added to “${p.name}”`); window.dispatchEvent(new CustomEvent('pmu:playlists')); onClose() } catch (e) { toast(errMsg(e), 'err') }
  }
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    try { await api.post(`${V1}/playlists`, { name: name.trim(), song_ids: ids }); toast(`Created “${name.trim()}”`); window.dispatchEvent(new CustomEvent('pmu:playlists')); onClose() } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <Modal title={songs.length === 1 ? `Add “${songs[0].title}” to a playlist` : `Add ${songs.length} songs to a playlist`} onClose={onClose} size="sm">
      <div className="stack">
        <form onSubmit={create} className="row"><input value={name} onChange={e => setName(e.target.value)} placeholder="New playlist name" autoFocus /><button className="btn primary" disabled={!name.trim()}><Icon name="plus" size={15} />Create</button></form>
        {lists && lists.length > 6 && <input value={q} onChange={e => setQ(e.target.value)} placeholder="Find a playlist" />}
        <div className="pick-list">
          {!lists && <div className="loading"><Spinner /> Loading…</div>}
          {lists && shown.length === 0 && <p className="muted small">No playlists of yours yet: name a new one above.</p>}
          {shown.map(p => (
            <button key={p.id} type="button" className="pick-row" onClick={() => add(p)}>
              <Cover src={coverURL(p.coverArt || 'pl-' + p.id, 80)} size={36} label={p.name} fallbackIcon="playlist" />
              <span className="grow"><span className="ellipsis block">{p.name}</span><span className="muted xsmall">{p.songCount} songs</span></span>
              <Icon name="plus" size={15} className="muted" />
            </button>
          ))}
        </div>
      </div>
    </Modal>
  )
}

/** Play / shuffle / more buttons of a page hero. */
export function HeroActions({ onPlay, onShuffle, children }: { onPlay: () => void; onShuffle?: () => void; children?: React.ReactNode }) {
  return (
    <div className="hero-actions">
      <button type="button" className="btn primary lg play-btn" onClick={onPlay}><Icon name="play" size={17} />Play</button>
      {onShuffle && <button type="button" className="btn lg" onClick={onShuffle}><Icon name="shuffle" size={17} />Shuffle</button>}
      {children}
    </div>
  )
}

export function MenuTrigger({ items, label = 'More' }: { items: () => MenuItem[]; label?: string }) {
  const [a, setA] = useState<Anchor | null>(null)
  return <>
    <button type="button" className="btn lg icon-only" aria-label={label} title={label} aria-haspopup="menu" aria-expanded={!!a} onClick={e => setA(a ? null : rectOf(e.currentTarget))}><Icon name="more" size={18} /></button>
    {a && <Menu anchor={a} items={items()} onClose={() => setA(null)} width={230} />}
  </>
}
