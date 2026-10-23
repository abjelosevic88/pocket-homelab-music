// ⌘K / "/" — search the library (artists, albums, songs, playlists) or run a command.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, albumCover, artistImage, clock, coverURL } from '../lib/music'
import { player } from '../lib/player-react'
import type { Album, Artist, Playlist, Song } from '../lib/types'
import { Icon } from './icons'
import { Cover, startRadio } from './music'
import type { CreateKind } from './Layout'
import { useDebounce } from './ui'

interface Results { artists: Artist[]; albums: Album[]; songs: Song[] }

export function CommandPalette({ onClose, playlists, onCreate, onShortcuts }: { onClose: () => void; playlists: Playlist[]; onCreate: (k: CreateKind) => void; onShortcuts: () => void }) {
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 120)
  const [hi, setHi] = useState(0)
  const [res, setRes] = useState<Results | null>(null)
  const list = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!dq.trim()) { setRes(null); return }
    let live = true
    api.get<Results>(`${V1}/search?q=${encodeURIComponent(dq)}&limit=6`).then(r => { if (live) setRes(r) }).catch(() => {})
    return () => { live = false }
  }, [dq])

  type Row = { key: string; group: string; icon: React.ReactNode; label: React.ReactNode; sub?: React.ReactNode; hint?: string; run: () => void; alt?: () => void }
  const commands: Row[] = useMemo(() => [
    { key: 'mix', group: 'Commands', icon: <Icon name="sparkles" size={16} />, label: 'Make a mix…', hint: 'X', run: () => onCreate('mix') },
    { key: 'shuffle', group: 'Commands', icon: <Icon name="shuffle" size={16} />, label: 'Shuffle my library', run: () => onCreate('shuffle') },
    { key: 'playlist', group: 'Commands', icon: <Icon name="playlist" size={16} />, label: 'New playlist', run: () => onCreate('playlist') },
    { key: 'smart', group: 'Commands', icon: <Icon name="zap" size={16} />, label: 'New smart playlist', run: () => onCreate('smart') },
    { key: 'request', group: 'Commands', icon: <Icon name="download" size={16} />, label: 'Request an album', run: () => onCreate('request') },
    { key: 'home', group: 'Go to', icon: <Icon name="home" size={16} />, label: 'Home', hint: 'H', run: () => navigate('/') },
    { key: 'albums', group: 'Go to', icon: <Icon name="album" size={16} />, label: 'Albums', hint: 'A', run: () => navigate('/albums') },
    { key: 'artists', group: 'Go to', icon: <Icon name="mic" size={16} />, label: 'Artists', hint: 'I', run: () => navigate('/artists') },
    { key: 'songs', group: 'Go to', icon: <Icon name="music" size={16} />, label: 'Songs', run: () => navigate('/songs') },
    { key: 'genres', group: 'Go to', icon: <Icon name="tag" size={16} />, label: 'Genres', hint: 'G', run: () => navigate('/genres') },
    { key: 'mixes', group: 'Go to', icon: <Icon name="sparkles" size={16} />, label: 'Mixes', run: () => navigate('/mixes') },
    { key: 'loved', group: 'Go to', icon: <Icon name="heart" size={16} />, label: 'Loved songs', run: () => navigate('/loved') },
    { key: 'history', group: 'Go to', icon: <Icon name="history" size={16} />, label: 'History', hint: 'Y', run: () => navigate('/history') },
    { key: 'stats', group: 'Go to', icon: <Icon name="bar" size={16} />, label: 'Stats', hint: 'T', run: () => navigate('/stats') },
    { key: 'releases', group: 'Go to', icon: <Icon name="compass" size={16} />, label: 'New releases', run: () => navigate('/releases') },
    { key: 'requests', group: 'Go to', icon: <Icon name="download" size={16} />, label: 'Requests', run: () => navigate('/requests') },
    { key: 'settings', group: 'Go to', icon: <Icon name="settings" size={16} />, label: 'Settings', hint: ',', run: () => navigate('/settings') },
    { key: 'library', group: 'Go to', icon: <Icon name="server" size={16} />, label: 'Settings: library connection', run: () => navigate('/settings/library') },
    { key: 'keys', group: 'Go to', icon: <Icon name="keyboard" size={16} />, label: 'Keyboard shortcuts', hint: '?', run: onShortcuts },
  ], [navigate, onCreate, onShortcuts])

  const rows = useMemo<Row[]>(() => {
    const n = q.trim().toLowerCase()
    const words = n.split(/\s+/).filter(Boolean)
    const hit = (text: string) => words.every(w => text.toLowerCase().includes(w))
    const cmds = commands.filter(c => !n || hit(`${c.label} ${c.group}`))
    const pls = playlists.filter(p => n && hit(p.name)).slice(0, 5).map<Row>(p => ({
      key: 'p' + p.id, group: 'Playlists', icon: <Cover src={coverURL(p.coverArt || 'pl-' + p.id, 64)} size={30} label={p.name} fallbackIcon="playlist" />, label: p.name, sub: `${p.songCount} songs`, run: () => navigate(`/playlists/${p.id}`),
    }))
    if (!n) return cmds
    const out: Row[] = []
    if (res) {
      out.push(...res.artists.map<Row>(a => ({ key: 'ar' + a.id, group: 'Artists', icon: <Cover src={artistImage(a.id, 64)} size={30} round label={a.name} />, label: a.name, sub: a.album_count ? `${a.album_count} albums` : 'Artist', run: () => navigate(`/artists/${a.id}`) })))
      out.push(...res.albums.map<Row>(a => ({ key: 'al' + a.id, group: 'Albums', icon: <Cover src={albumCover(a.id, 64)} size={30} label={a.name} />, label: a.name, sub: `${a.artist}${a.year ? ' · ' + a.year : ''}`, run: () => navigate(`/albums/${a.id}`) })))
      out.push(...res.songs.map<Row>(s => ({
        key: 's' + s.id, group: 'Songs', icon: <Cover src={albumCover(s.album_id, 64)} size={30} label={s.album} />, label: s.title, sub: `${s.artist} · ${s.album} · ${clock(s.duration)}`, hint: '↵ play',
        run: () => player.playSongs([s], 0, { kind: 'search', name: `“${q}”` }), alt: () => startRadio({ song: s.id }, s.title + ' radio', s),
      })))
    }
    out.push(...pls, ...cmds.slice(0, 6))
    return out
  }, [q, res, playlists, commands, navigate])
  useEffect(() => setHi(0), [q, res])
  useEffect(() => { list.current?.querySelector(`[data-i="${hi}"]`)?.scrollIntoView({ block: 'nearest' }) }, [hi])

  const run = (r: Row | undefined, alt = false) => { if (!r) return; onClose(); (alt && r.alt ? r.alt : r.run)() }
  let last = ''
  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box" role="dialog" aria-label="Search and commands">
        <div className="pal-input">
          <Icon name="search" size={18} />
          <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Artists, albums, songs, playlists, or a command…" aria-label="Search"
            onKeyDown={e => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setHi(h => Math.min(rows.length - 1, h + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
              else if (e.key === 'Enter') { e.preventDefault(); run(rows[hi], e.shiftKey) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
            }} />
          <kbd>esc</kbd>
        </div>
        <div className="pal-list" ref={list} role="listbox">
          {rows.length === 0 && <div className="pal-empty">{q.trim() && !res ? 'Searching…' : 'Nothing matches.'}</div>}
          {rows.map((r, i) => {
            const head = r.group !== last ? <div className="pal-group" key={'g' + r.group}>{r.group}</div> : null
            last = r.group
            return (
              <React.Fragment key={r.key}>
                {head}
                <div data-i={i} role="option" aria-selected={i === hi} className={`pal-row ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)} onClick={() => run(r)}>
                  <span className="pal-ico">{r.icon}</span>
                  <span className="grow"><span className="pal-label ellipsis">{r.label}</span>{r.sub && <span className="pal-sub ellipsis">{r.sub}</span>}</span>
                  {r.hint && <kbd>{r.hint}</kbd>}
                  {i === hi && <Icon name="chevron-right" size={14} className="muted" />}
                </div>
              </React.Fragment>
            )
          })}
        </div>
        {q.trim() && <div className="pal-foot muted xsmall"><kbd>↵</kbd> open / play · <kbd>⇧</kbd><kbd>↵</kbd> start radio from a song</div>}
      </div>
    </div>, document.body)
}
