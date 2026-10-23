// Browsing the library: albums, artists, songs, loved songs, genres.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, hueFor, longDuration } from '../lib/music'
import { player } from '../lib/player-react'
import type { Album, Artist, Genre, Song } from '../lib/types'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { AlbumCard, ArtistCard, SongTable, startRadio } from '../components/music'
import { useCrumb, shuffleLibrary } from '../components/Layout'
import { Empty, Loading, Segmented, Spinner, errMsg, useToast } from '../components/ui'

/** Pages through a list as the person scrolls. */
function usePaged<T>(fetchPage: (offset: number) => Promise<{ items: T[]; total: number }>, key: string) {
  const [items, setItems] = useState<T[] | null>(null)
  const [total, setTotal] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const gen = useRef(0)
  const fetchRef = useRef(fetchPage)
  fetchRef.current = fetchPage
  useEffect(() => {
    const g = ++gen.current
    setItems(null); setError('')
    fetchRef.current(0).then(r => { if (g === gen.current) { setItems(r.items); setTotal(r.total) } }).catch(e => { if (g === gen.current) setError(errMsg(e)) })
  }, [key])
  const more = useCallback(() => {
    if (busy || !items || items.length >= total) return
    const g = gen.current
    setBusy(true)
    fetchRef.current(items.length).then(r => { if (g === gen.current) setItems(cur => [...(cur || []), ...r.items]) }).catch(() => {}).finally(() => setBusy(false))
  }, [busy, items, total])
  const sentinel = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = sentinel.current
    if (!el) return
    const io = new IntersectionObserver(es => { if (es[0].isIntersecting) more() }, { root: document.querySelector('.content'), rootMargin: '600px' })
    io.observe(el)
    return () => io.disconnect()
  }, [more, items])
  return { items, total, busy, error, sentinel }
}

const DECADES = [1960, 1970, 1980, 1990, 2000, 2010, 2020]

function useGenres() {
  const [gs, setGs] = useState<Genre[]>([])
  useEffect(() => { api.get<Genre[]>(`${V1}/genres`).then(setGs).catch(() => {}) }, [])
  return gs
}

export function Albums() {
  const [sp, setSp] = useSearchParams()
  const genres = useGenres()
  const sort = sp.get('sort') || 'added'
  const genre = sp.get('genre') || ''
  const decade = Number(sp.get('decade') || 0)
  const starred = sp.get('starred') === '1'
  const unplayed = sp.get('unplayed') === '1'
  const [seed] = useState(() => Math.floor(Math.random() * 100000))
  const set = (k: string, v: string) => { const n = new URLSearchParams(sp); if (v) n.set(k, v); else n.delete(k); setSp(n, { replace: true }) }
  const q = `sort=${sort}&genre=${encodeURIComponent(genre)}&decade=${decade || ''}&starred=${starred ? 1 : ''}&unplayed=${unplayed ? 1 : ''}&seed=${seed}`
  const { items, total, busy, error, sentinel } = usePaged<Album>(off => api.get<{ albums: Album[]; total: number }>(`${V1}/albums?${q}&limit=60&offset=${off}`).then(r => ({ items: r.albums, total: r.total })), q)
  return (
    <div>
      <div className="page-header">
        <div><h1>Albums</h1><div className="sub">{items ? `${total.toLocaleString()} albums` : ' '}</div></div>
      </div>
      <div className="filters">
        <Select ariaLabel="Sort" className="inline" value={sort} onChange={v => set('sort', v)} options={[
          { value: 'added', label: 'Recently added' }, { value: 'played', label: 'Recently played' }, { value: 'plays', label: 'Most played' }, { value: 'name', label: 'Name' },
          { value: 'artist', label: 'Artist' }, { value: 'year', label: 'Newest first' }, { value: 'year_asc', label: 'Oldest first' }, { value: 'random', label: 'Random' }, { value: 'starred', label: 'Loved recently' }]} />
        <Select ariaLabel="Genre" className="inline" value={genre} onChange={v => set('genre', v)} placeholder="All genres" clearLabel="All genres" options={genres.slice(0, 300).map(g => ({ value: g.name, label: g.name, hint: String(g.albums) }))} />
        <div className="chips">
          {DECADES.map(d => <button key={d} type="button" className={`chip ${decade === d ? 'active' : ''}`} onClick={() => set('decade', decade === d ? '' : String(d))}>{String(d).slice(2)}s</button>)}
        </div>
        <button type="button" className={`chip ${starred ? 'active' : ''}`} onClick={() => set('starred', starred ? '' : '1')}><Icon name="heart" size={13} />Loved</button>
        <button type="button" className={`chip ${unplayed ? 'active' : ''}`} onClick={() => set('unplayed', unplayed ? '' : '1')}>Never played</button>
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!items && !error && <Loading />}
      {items && items.length === 0 && <Empty title="No albums here" icon="album">Try another filter.</Empty>}
      {items && <div className="grid-tiles">{items.map(a => <AlbumCard key={a.id} a={a} />)}</div>}
      <div ref={sentinel} className="sentinel">{busy && <Spinner />}</div>
    </div>
  )
}

export function Artists() {
  const [sp, setSp] = useSearchParams()
  const sort = sp.get('sort') || 'name'
  const starred = sp.get('starred') === '1'
  const all = sp.get('all') === '1'
  const set = (k: string, v: string) => { const n = new URLSearchParams(sp); if (v) n.set(k, v); else n.delete(k); setSp(n, { replace: true }) }
  const q = `sort=${sort}&starred=${starred ? 1 : ''}&all=${all ? 1 : ''}`
  const { items, total, busy, error, sentinel } = usePaged<Artist>(off => api.get<{ artists: Artist[]; total: number }>(`${V1}/artists?${q}&limit=90&offset=${off}`).then(r => ({ items: r.artists, total: r.total })), q)
  return (
    <div>
      <div className="page-header"><div><h1>Artists</h1><div className="sub">{items ? `${total.toLocaleString()} artists` : ' '}</div></div></div>
      <div className="filters">
        <Select ariaLabel="Sort" className="inline" value={sort} onChange={v => set('sort', v)} options={[{ value: 'name', label: 'Name' }, { value: 'plays', label: 'Most played' }, { value: 'played', label: 'Recently played' }, { value: 'albums', label: 'Most albums' }, { value: 'songs', label: 'Most songs' }]} />
        <button type="button" className={`chip ${starred ? 'active' : ''}`} onClick={() => set('starred', starred ? '' : '1')}><Icon name="heart" size={13} />Loved</button>
        <button type="button" className={`chip ${all ? 'active' : ''}`} onClick={() => set('all', all ? '' : '1')} title="Also artists who only appear on songs (features, compilations)">Include guest artists</button>
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!items && !error && <Loading />}
      {items && <div className="grid-tiles artists">{items.map(a => <ArtistCard key={a.id} a={a} />)}</div>}
      <div ref={sentinel} className="sentinel">{busy && <Spinner />}</div>
    </div>
  )
}

export function Songs({ loved = false }: { loved?: boolean }) {
  const [sp, setSp] = useSearchParams()
  const { toast } = useToast()
  const genres = useGenres()
  const sort = sp.get('sort') || (loved ? 'starred' : 'added')
  const genre = sp.get('genre') || ''
  const lossless = sp.get('lossless') === '1'
  const set = (k: string, v: string) => { const n = new URLSearchParams(sp); if (v) n.set(k, v); else n.delete(k); setSp(n, { replace: true }) }
  const q = `sort=${sort}&genre=${encodeURIComponent(genre)}&starred=${loved ? 1 : ''}&lossless=${lossless ? 1 : ''}`
  const { items, total, busy, error, sentinel } = usePaged<Song>(off => api.get<{ songs: Song[]; total: number }>(`${V1}/songs?${q}&limit=200&offset=${off}`).then(r => ({ items: r.songs, total: r.total })), q)
  const playAll = async (shuffle: boolean) => {
    try {
      if (!loved && !genre && !lossless) return shuffleLibrary()
      const r = await api.get<{ songs: Song[] }>(`${V1}/songs?${q}&limit=1000`)
      player.playSongs(r.songs, shuffle ? -1 : 0, { kind: 'songs', name: loved ? 'Loved songs' : 'Songs', link: loved ? '/loved' : '/songs' }, shuffle)
    } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div>
      <div className="page-header">
        <div><h1>{loved ? 'Loved' : 'Songs'}</h1><div className="sub">{items ? `${total.toLocaleString()} songs` : ' '}</div></div>
        <div className="actions">
          {loved && <button type="button" className="btn primary" onClick={() => playAll(false)} disabled={!total}><Icon name="play" size={15} />Play</button>}
          <button type="button" className="btn" onClick={() => playAll(true)} disabled={!total}><Icon name="shuffle" size={15} />Shuffle</button>
        </div>
      </div>
      <div className="filters">
        <Select ariaLabel="Sort" className="inline" value={sort} onChange={v => set('sort', v)} options={[{ value: 'added', label: 'Recently added' }, ...(loved ? [{ value: 'starred', label: 'Recently loved' }] : []), { value: 'title', label: 'Title' }, { value: 'artist', label: 'Artist' }, { value: 'album', label: 'Album' }, { value: 'year', label: 'Year' }, { value: 'duration', label: 'Longest' }, { value: 'random', label: 'Random' }]} />
        <Select ariaLabel="Genre" className="inline" value={genre} onChange={v => set('genre', v)} placeholder="All genres" clearLabel="All genres" options={genres.slice(0, 300).map(g => ({ value: g.name, label: g.name, hint: String(g.songs) }))} />
        <button type="button" className={`chip ${lossless ? 'active' : ''}`} onClick={() => set('lossless', lossless ? '' : '1')}>Lossless</button>
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!items && !error && <Loading />}
      {items && items.length === 0 && (loved ? <Empty title="Nothing loved yet" icon="heart">Tap the heart next to a song you like. Loves are saved on your music server too.</Empty> : <Empty title="No songs" icon="music" />)}
      {items && items.length > 0 && <SongTable songs={items} source={{ kind: 'songs', name: loved ? 'Loved songs' : 'Songs', link: loved ? '/loved' : '/songs' }} showCover showPlays />}
      <div ref={sentinel} className="sentinel">{busy && <Spinner />}</div>
    </div>
  )
}

export function Genres() {
  const [gs, setGs] = useState<Genre[] | null>(null)
  const [q, setQ] = useState('')
  const [sort, setSort] = useState<'songs' | 'plays' | 'name'>('songs')
  useEffect(() => { api.get<Genre[]>(`${V1}/genres`).then(setGs).catch(() => setGs([])) }, [])
  const shown = useMemo(() => {
    let out = (gs || []).filter(g => !q || g.name.toLowerCase().includes(q.toLowerCase()))
    if (sort === 'plays') out = [...out].sort((a, b) => b.plays - a.plays || b.songs - a.songs)
    if (sort === 'name') out = [...out].sort((a, b) => a.name.localeCompare(b.name))
    return out
  }, [gs, q, sort])
  if (!gs) return <Loading />
  return (
    <div>
      <div className="page-header"><div><h1>Genres</h1><div className="sub">{gs.length} genres</div></div></div>
      <div className="filters">
        <input className="filter-input" value={q} onChange={e => setQ(e.target.value)} placeholder="Find a genre" />
        <Segmented size="sm" value={sort} onChange={setSort} options={[{ value: 'songs', label: 'Biggest' }, { value: 'plays', label: 'Most played' }, { value: 'name', label: 'A–Z' }]} />
      </div>
      <div className="genre-grid">
        {shown.slice(0, 400).map(g => (
          <Link key={g.name} to={`/genres/${encodeURIComponent(g.name)}`} className="genre-tile" style={{ ['--h' as string]: hueFor(g.name) }}>
            <span className="genre-name">{g.name}</span>
            <span className="genre-n">{g.albums} albums · {g.songs} songs{g.plays ? ` · ${g.plays} plays` : ''}</span>
          </Link>
        ))}
      </div>
    </div>
  )
}

export function GenrePage() {
  const { name = '' } = useParams()
  const genre = decodeURIComponent(name)
  const { toast } = useToast()
  useCrumb(genre)
  const q = `genre=${encodeURIComponent(genre)}`
  const { items, total, busy, sentinel } = usePaged<Album>(off => api.get<{ albums: Album[]; total: number }>(`${V1}/albums?${q}&sort=plays&limit=60&offset=${off}`).then(r => ({ items: r.albums, total: r.total })), q)
  const shuffle = () => api.get<{ songs: Song[] }>(`${V1}/shuffle?${q}`).then(r => player.playSongs(r.songs, 0, { kind: 'genre', name: genre, link: `/genres/${encodeURIComponent(genre)}` })).catch(e => toast(errMsg(e), 'err'))
  return (
    <div>
      <header className="genre-hero" style={{ ['--h' as string]: hueFor(genre) }}>
        <div>
          <p className="eyebrow">Genre</p>
          <h1>{genre}</h1>
          <p className="muted">{total ? `${total} albums` : ' '}{items ? ` · ${longDuration(items.reduce((t, a) => t + a.duration, 0))} and more` : ''}</p>
        </div>
        <div className="hero-actions">
          <button type="button" className="btn primary lg" onClick={shuffle}><Icon name="shuffle" size={17} />Shuffle</button>
          <button type="button" className="btn lg" onClick={() => startRadio({ genre }, genre + ' radio').catch(e => toast(errMsg(e), 'err'))}><Icon name="radio" size={17} />Radio</button>
        </div>
      </header>
      {!items && <Loading />}
      {items && <div className="grid-tiles">{items.map(a => <AlbumCard key={a.id} a={a} />)}</div>}
      <div ref={sentinel} className="sentinel">{busy && <Spinner />}</div>
    </div>
  )
}
