// "From a link": paste a YouTube (or Bandcamp, SoundCloud…) link, check how it will be filed — artist, album,
// year, track titles, the folder — and the server downloads the audio into the music folder. The list of
// downloads lives on the Requests page.
import React, { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, longDuration } from '../lib/music'
import { fmtDate } from '../lib/format'
import { useLiveRefresh } from '../lib/live'
import { Icon } from './icons'
import { Cover } from './music'
import { Field, Modal, Segmented, Spinner, errMsg, useDebounce, useToast } from './ui'
import { Select } from './Select'
import type { Playlist } from '../lib/types'

export interface LinkTrack { url: string; title: string; number?: number; file?: string; duration?: number }
export interface LinkDownload {
  id: number; url: string; artist: string; album: string; year: number; genre: string; folder: string; cover_url: string
  tracks: LinkTrack[]; playlist_id: string; playlist_name: string; state: 'queued' | 'downloading' | 'scanning' | 'done' | 'failed'; detail: string; error: string; album_id: string
  created_at: string; updated_at: string
}
interface Root { key: string; label: string; genre: string; exists: boolean }
interface LinkList { ready: boolean; detail: string; roots: Root[]; items: LinkDownload[] }
interface Suggest {
  kind: 'track' | 'playlist'; url: string; source: string; channel: string; channel_url: string; site: string; thumbnail: string; duration: number
  artist: string; album: string; year: number; genre: string; tracks: LinkTrack[]; before: string[]
}
interface Place { root: string; folder: string; artist_new: boolean; album_new: boolean; next_track: number; artists: string[]; albums: string[] }

const ROOT_KEY = 'pmu-link-root'
const PLAYLIST_KEY = 'pmu-link-playlist'
const looksLikeLink = (s: string) => /^https?:\/\/\S+$/i.test(s.trim())

export function LinkDownloadModal({ onClose }: { onClose: () => void }) {
  const { toast } = useToast()
  const [info, setInfo] = useState<LinkList | null>(null)
  const [url, setUrl] = useState('')
  const [reading, setReading] = useState(false)
  const [err, setErr] = useState('')
  const [sg, setSg] = useState<Suggest | null>(null)
  const [root, setRoot] = useState(() => localStorage.getItem(ROOT_KEY) || 'ambient')
  const [artist, setArtist] = useState('')
  const [album, setAlbum] = useState('')
  const [year, setYear] = useState('')
  const [genre, setGenre] = useState('')
  const [tracks, setTracks] = useState<LinkTrack[]>([])
  const [place, setPlace] = useState<Place | null>(null)
  const [busy, setBusy] = useState(false)
  const [playlists, setPlaylists] = useState<Playlist[]>([])
  const [playlist, setPlaylist] = useState('')
  const [genres, setGenres] = useState<string[]>([])
  const read = useRef(0)

  useEffect(() => { api.get<LinkList>(`${V1}/link-downloads`).then(setInfo).catch(e => setErr(errMsg(e))) }, [])
  // your own playlists (not smart ones: those fill themselves); the last one used is picked again
  useEffect(() => {
    api.get<Playlist[]>(`${V1}/playlists`).then(ps => {
      const mine = ps.filter(p => p.mine && !p.readonly && !p.smart_id)
      setPlaylists(mine)
      const last = localStorage.getItem(PLAYLIST_KEY) || ''
      if (mine.some(p => p.id === last)) setPlaylist(last)
    }).catch(() => {})
  }, [])
  // the library's genres, most songs first
  useEffect(() => { api.get<{ name: string }[]>(`${V1}/genres`).then(gs => setGenres(gs.map(g => g.name).filter(Boolean))).catch(() => {}) }, [])
  const genreOptions = [...(genre && !genres.some(g => g.toLowerCase() === genre.toLowerCase()) ? [genre] : []), ...genres].map(g => ({ value: g, label: g }))
  const pickPlaylist = (id: string) => { setPlaylist(id); localStorage.setItem(PLAYLIST_KEY, id) }
  const newPlaylist = async (name: string) => {
    const p = await api.post<Playlist>(`${V1}/playlists`, { name, comment: '', song_ids: [] })
    setPlaylists(ps => [{ ...p, mine: true } as Playlist, ...ps])
    window.dispatchEvent(new CustomEvent('pmu:playlists'))
    return p.id
  }
  const rootGenre = (k: string) => info?.roots.find(r => r.key === k)?.genre || ''

  const inspect = async (link = url) => {
    if (!looksLikeLink(link)) { setErr('Paste a link that starts with https://'); return }
    const n = ++read.current
    setReading(true); setErr(''); setSg(null)
    try {
      const s = await api.post<Suggest>(`${V1}/link-downloads/inspect`, { url: link.trim() })
      if (n !== read.current) return
      setSg(s); setArtist(s.artist); setAlbum(s.album); setYear(s.year ? String(s.year) : ''); setTracks(s.tracks)
      setGenre(s.genre || rootGenre(root))
    } catch (e) { if (n === read.current) setErr(errMsg(e)) } finally { if (n === read.current) setReading(false) }
  }
  const pickRoot = (k: string) => {
    if (!genre || genre === rootGenre(root)) setGenre(rootGenre(k))
    setRoot(k); localStorage.setItem(ROOT_KEY, k)
  }

  // where it lands, checked against the folders already there
  const dArtist = useDebounce(artist, 300), dAlbum = useDebounce(album, 300), dYear = useDebounce(year, 300)
  useEffect(() => {
    if (!sg) return
    const q = new URLSearchParams({ root, artist: dArtist, album: dAlbum, year: dYear })
    api.get<Place>(`${V1}/link-downloads/place?${q}`).then(setPlace).catch(() => setPlace(null))
  }, [sg, root, dArtist, dAlbum, dYear])

  const start = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!sg) return
    setBusy(true)
    try {
      await api.post(`${V1}/link-downloads`, {
        url: sg.url, root, artist, album, year: Number(year) || 0, genre, channel: sg.channel, channel_url: sg.channel_url, thumbnail: sg.thumbnail,
        tracks: tracks.map(t => ({ url: t.url, title: t.title })),
        playlist_id: playlist, playlist_name: playlists.find(p => p.id === playlist)?.name || '',
      })
      toast(`Downloading “${album}” — follow it under Requests`)
      onClose()
    } catch (e2) { toast(errMsg(e2), 'err') } finally { setBusy(false) }
  }

  const first = place?.next_track || 1
  const fileName = (i: number, t: LinkTrack) => `${String(first + i).padStart(2, '0')}. ${t.title.replace(/[/\\]/g, '-')}`
  const notReady = info && !info.ready

  return (
    <Modal title="Add from a link" onClose={onClose} size="lg">
      <div className="stack">
        {notReady && <div className="callout warn"><Icon name="alert" size={15} /><span>Downloads are off on this server: {info.detail}</span></div>}
        <form className="find-box" onSubmit={e => { e.preventDefault(); inspect() }}>
          <Icon name="link" size={18} />
          <input autoFocus value={url} placeholder="Paste a YouTube link — a video or a playlist (Bandcamp and SoundCloud work too)"
            onChange={e => setUrl(e.target.value)}
            onPaste={e => { const t = e.clipboardData.getData('text'); if (looksLikeLink(t)) { e.preventDefault(); setUrl(t.trim()); inspect(t) } }} />
          {reading ? <Spinner /> : <button className="btn sm" disabled={!url.trim() || !!notReady}>Look it up</button>}
        </form>
        {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
        {reading && <p className="muted small">Reading the link…</p>}

        {sg && (
          <form className="stack" onSubmit={start}>
            <div className="link-source">
              <Cover src={sg.thumbnail} size={88} label={sg.source} />
              <div className="grow">
                <div className="link-source-title">{sg.source}</div>
                <div className="muted small">{[sg.channel, sg.kind === 'playlist' ? `${sg.tracks.length} videos` : '', sg.duration ? longDuration(sg.duration) : '', sg.site].filter(Boolean).join(' · ')}</div>
                {sg.before.length > 0 && <div className="badge warn mt-s">Downloaded before into {sg.before[0]}</div>}
              </div>
            </div>

            <Field label="Put it in">
              <Segmented value={root} onChange={pickRoot} options={(info?.roots || []).map(r => ({ value: r.key, label: r.key === 'music' ? 'Music (by artist)' : `${r.label} folder` }))} />
            </Field>
            <div className="two-col">
              <Field label="Artist" help={place && artist.trim() ? (place.artist_new ? 'New artist folder' : 'Goes into the existing artist folder') : undefined}>
                <input value={artist} onChange={e => setArtist(e.target.value)} list="pmu-link-artists" required maxLength={150} />
                <datalist id="pmu-link-artists">{place?.artists.map(a => <option key={a} value={a} />)}</datalist>
              </Field>
              <Field label="Album" help={place && album.trim() ? (place.album_new ? (sg.kind === 'track' ? 'A new album with this one track' : 'A new album') : `Added to the album, from track ${first}`) : undefined}>
                <input value={album} onChange={e => setAlbum(e.target.value)} list="pmu-link-albums" required maxLength={150} />
                <datalist id="pmu-link-albums">{place?.albums.map(a => <option key={a} value={a.replace(/^\d{4} - /, '')} />)}</datalist>
              </Field>
            </div>
            <div className="two-col">
              <Field label="Year"><input value={year} onChange={e => setYear(e.target.value.replace(/\D/g, '').slice(0, 4))} inputMode="numeric" placeholder="none" /></Field>
              <Field label="Genre">
                <Select ariaLabel="Genre" value={genre} onChange={setGenre} placeholder="No genre" clearLabel="No genre"
                  options={genreOptions} onCreate={g => g.slice(0, 60)} createLabel="New genre" />
              </Field>
            </div>

            <Field label="Add to a playlist" help={playlist ? 'Added once your music server has scanned the new files' : 'Optional — pick one, or type a name to make a new one'}>
              <Select ariaLabel="Playlist" value={playlist} onChange={pickPlaylist} placeholder="No playlist" clearLabel="No playlist"
                options={playlists.map(p => ({ value: p.id, label: p.name, hint: `${p.songCount} songs` }))}
                onCreate={newPlaylist} createLabel="New playlist" />
            </Field>

            <Field label={tracks.length > 1 ? `Tracks (${tracks.length})` : 'Track title'}>
              <div className={tracks.length > 1 ? 'link-tracks' : ''}>
                {tracks.map((t, i) => (
                  <div key={i} className="link-track">
                    {tracks.length > 1 && <span className="muted small link-track-no">{String(first + i).padStart(2, '0')}</span>}
                    <input value={t.title} onChange={e => setTracks(ts => ts.map((x, j) => j === i ? { ...x, title: e.target.value } : x))} required maxLength={200} />
                    {tracks.length > 1 && <button type="button" className="btn ghost icon-only sm" aria-label="Leave this one out" title="Leave this one out" onClick={() => setTracks(ts => ts.filter((_, j) => j !== i))}><Icon name="x" size={13} /></button>}
                  </div>
                ))}
              </div>
            </Field>

            {place?.folder && tracks[0] && (
              <div className="link-path muted small"><Icon name="disc" size={13} /><span>{place.folder}/{fileName(0, tracks[0])}{tracks.length > 1 ? ` … (${tracks.length} files)` : ''}</span></div>
            )}
            <p className="muted small">The best audio is kept as it is (no re-encoding). Tags, a square cover.jpg and the picture in the file are set for you; your music server scans the folder when it is done.</p>
            <div className="form-actions">
              <button type="button" className="btn" onClick={onClose}>Cancel</button>
              <button className="btn primary" disabled={busy || !artist.trim() || !album.trim() || tracks.length === 0}>{busy ? <Spinner /> : <Icon name="download" size={15} />}Download</button>
            </div>
          </form>
        )}
      </div>
    </Modal>
  )
}

/** The downloads from links, on the Requests page (admins). */
export function LinkDownloads({ onAdd }: { onAdd: () => void }) {
  const { toast } = useToast()
  const [items, setItems] = useState<LinkDownload[] | null>(null)
  const load = () => api.get<LinkList>(`${V1}/link-downloads`).then(r => setItems(r.items)).catch(() => setItems([]))
  useEffect(() => { load() }, [])
  useLiveRefresh(ev => ev.type === 'link_downloads', load)
  if (!items) return null
  const forget = async (d: LinkDownload) => {
    try { await api.del(`${V1}/link-downloads/${d.id}`); load() } catch (e) { toast(errMsg(e), 'err') }
  }
  const finished = items.some(d => d.state === 'done' || d.state === 'failed')
  return (
    <section className="section">
      <div className="section-head">
        <h2>From links</h2>
        <div className="row">
          {finished && <button type="button" className="btn ghost sm" onClick={async () => { await api.post(`${V1}/link-downloads/clear`, {}).catch(() => {}); load() }}>Clear finished</button>}
          <button type="button" className="btn sm" onClick={onAdd}><Icon name="link" size={13} />Add from a link</button>
        </div>
      </div>
      {items.length === 0 ? <p className="muted small">Paste a YouTube link (New → Add from a link) and the audio is filed into your library as an album.</p> : (
        <div className="req-list">
          {items.map(d => {
            const running = d.state === 'queued' || d.state === 'downloading' || d.state === 'scanning'
            return (
              <div key={d.id} className={`req-row state-${d.state}`}>
                <Cover src={d.cover_url} size={52} label={d.album} />
                <div className="grow">
                  <div className="ellipsis"><b>{d.album}</b></div>
                  <div className="muted small ellipsis">{d.artist}{d.year ? ` · ${d.year}` : ''} · {d.folder}{d.playlist_name ? ` · → ${d.playlist_name}` : ''} · {fmtDate(d.created_at.slice(0, 10))}</div>
                  {d.error && <div className={`small ellipsis ${d.state === 'failed' ? 'text-danger' : 'muted'}`} title={d.error}>{d.error}</div>}
                </div>
                {d.state === 'done' && d.album_id
                  ? <Link to={`/albums/${d.album_id}`} className="badge ok">In your library</Link>
                  : <span className={`badge ${d.state === 'done' ? 'ok' : d.state === 'failed' ? 'danger' : d.state === 'queued' ? '' : 'accent'}`}>
                    {d.state === 'queued' ? 'Waiting' : d.state === 'downloading' ? `Downloading${d.detail ? ' ' + d.detail : ''}` : d.state === 'scanning' ? 'Adding to library' : d.state === 'done' ? 'Downloaded' : 'Failed'}
                  </span>}
                <button type="button" className="btn ghost icon-only sm" aria-label={running ? 'Stop' : 'Forget'} title={running ? 'Stop (tracks already downloaded stay)' : 'Forget (the files stay)'} onClick={() => forget(d)}><Icon name="x" size={14} /></button>
              </div>
            )
          })}
        </div>
      )}
    </section>
  )
}
