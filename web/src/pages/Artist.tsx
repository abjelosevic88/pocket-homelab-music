import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../lib/api'
import { V1, artistImage } from '../lib/music'
import { player } from '../lib/player-react'
import type { Album, Artist as ArtistT, DiscAlbum, Song } from '../lib/types'
import { Icon } from '../components/icons'
import { AlbumCard, ArtistCard, Cover, Shelf, SongTable, StarButton, startRadio, useCoverColor } from '../components/music'
import { useCrumb } from '../components/Layout'
import { Loading, Segmented, Spinner, errMsg, useAsync, useToast } from '../components/ui'
import { touches, useLiveRefresh } from '../lib/live'

interface ArtistResp { artist: ArtistT; albums: Album[]; appears_on: Album[]; top_songs: Song[]; song_count: number; biography?: string; lastfm_url?: string; similar?: ArtistT[] }

export default function Artist() {
  const { id = '' } = useParams()
  const { toast } = useToast()
  const { data, error, setData } = useAsync(() => api.get<ArtistResp>(`${V1}/artists/${id}?info=1`), [id])
  // starred or rated elsewhere, or the index refreshed: the artist again, keeping the biography already here
  useLiveRefresh(ev => touches(ev, [id, ...(data?.top_songs || []).map(s => s.id), ...(data?.albums || []).map(a => a.id)]),
    () => api.get<ArtistResp>(`${V1}/artists/${id}`).then(r => setData(d => d ? { ...r, biography: d.biography, lastfm_url: d.lastfm_url, similar: d.similar } : r)).catch(() => {}))
  const [allTop, setAllTop] = useState(false)
  const [bioOpen, setBioOpen] = useState(false)
  const color = useCoverColor(artistImage(id, 64))
  useCrumb(data?.artist.name)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const a = data.artist
  const playAll = async (shuffle: boolean) => {
    try {
      const r = await api.get<{ songs: Song[] }>(`${V1}/songs?artist=${a.id}&limit=1000&sort=${shuffle ? 'random' : 'year'}`)
      player.playSongs(r.songs, shuffle ? -1 : 0, { kind: 'artist', name: a.name, link: `/artists/${a.id}` }, shuffle)
    } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div className="detail" style={color ? { ['--art' as string]: color } : undefined}>
      <div className="detail-glow" />
      <header className="detail-hero artist">
        <Cover src={artistImage(a.id, 600)} className="hero-cover" round label={a.name} />
        <div className="hero-text">
          <p className="eyebrow">Artist</p>
          <h1 className="hero-title">{a.name}</h1>
          <p className="hero-meta">
            <span>{data.albums.length} albums</span>
            <span>{data.song_count} songs</span>
            {a.plays ? <span>{a.plays.toLocaleString()} plays by you</span> : null}
          </p>
          <div className="hero-actions">
            <button type="button" className="btn primary lg" onClick={() => playAll(false)}><Icon name="play" size={17} />Play</button>
            <button type="button" className="btn lg" onClick={() => playAll(true)}><Icon name="shuffle" size={17} />Shuffle</button>
            <button type="button" className="btn lg" onClick={() => startRadio({ artist: a.id }, a.name + ' radio').catch(e => toast(errMsg(e), 'err'))}><Icon name="radio" size={17} />Radio</button>
            <StarButton kind="artist" id={a.id} starred={!!a.starred_at} size={19} className="lg" />
          </div>
        </div>
      </header>

      {data.top_songs.length > 0 && (
        <section className="section">
          <div className="section-head"><h2>{data.top_songs.some(s => s.plays) ? 'Your most played' : 'Popular'}</h2></div>
          <SongTable songs={allTop ? data.top_songs : data.top_songs.slice(0, 5)} source={{ kind: 'artist', name: a.name, link: `/artists/${a.id}` }} showArtist={false} showCover showPlays />
          {data.top_songs.length > 5 && <button type="button" className="link-btn mt-s" onClick={() => setAllTop(o => !o)}>{allTop ? 'Show less' : 'Show more'}</button>}
        </section>
      )}
      {data.albums.length > 0 && (
        <section className="section">
          <div className="section-head"><h2>Albums</h2></div>
          <div className="grid-tiles">{data.albums.map(x => <AlbumCard key={x.id} a={x} sub={[x.year || '', x.plays ? `${x.plays} plays` : ''].filter(Boolean).join(' · ') || ' '} />)}</div>
        </section>
      )}
      <Discography artistId={a.id} />
      {data.appears_on.length > 0 && <Shelf title="Appears on">{data.appears_on.map(x => <AlbumCard key={x.id} a={x} />)}</Shelf>}
      {data.similar && data.similar.length > 0 && <Shelf title="Similar artists in your library">{data.similar.map(s => <ArtistCard key={s.id} a={s} />)}</Shelf>}
      {data.biography && (
        <section className="notes">
          <h2>About {a.name}</h2>
          <p className={bioOpen ? '' : 'clamp'}>{data.biography}</p>
          <div className="row">{data.biography.length > 300 && <button type="button" className="link-btn" onClick={() => setBioOpen(o => !o)}>{bioOpen ? 'Less' : 'More'}</button>}{data.lastfm_url && <a href={data.lastfm_url} target="_blank" rel="noreferrer" className="small muted">Last.fm <Icon name="external" size={11} /></a>}</div>
        </section>
      )}
    </div>
  )
}

/** The artist's albums in Lidarr next to the library: what's missing, one click to get it. */
function Discography({ artistId }: { artistId: string }) {
  const { toast } = useToast()
  const [d, setD] = useState<{ in_lidarr: boolean; albums: DiscAlbum[] } | null>(null)
  const [off, setOff] = useState('')
  const [show, setShow] = useState<'missing' | 'all'>('missing')
  const [busy, setBusy] = useState(0)
  const load = () => api.get<{ in_lidarr: boolean; albums: DiscAlbum[] }>(`${V1}/artists/${artistId}/discography`).then(setD).catch(e => { if (e instanceof ApiError && e.data.code === 'lidarr_off') setOff(e.message); else setOff('') })
  useEffect(() => { setD(null); setOff(''); load() }, [artistId]) // eslint-disable-line react-hooks/exhaustive-deps
  if (off || !d || !d.in_lidarr) return null
  const missing = d.albums.filter(x => !x.secondary && x.state !== 'have')
  const shown = show === 'missing' ? missing : d.albums
  if (!d.albums.length) return null
  const request = async (x: DiscAlbum) => {
    setBusy(x.lidarr_id)
    try { await api.post(`${V1}/requests`, { lidarr_id: x.lidarr_id }); toast(`Lidarr is looking for “${x.title}”`); await load() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(0) }
  }
  return (
    <section className="section">
      <div className="section-head">
        <h2>Discography</h2>
        <span className="muted small">{d.albums.filter(x => x.state === 'have').length} of {d.albums.filter(x => !x.secondary).length} studio releases in your library · from Lidarr</span>
        <span className="spacer" />
        <Segmented size="sm" value={show} onChange={setShow} options={[{ value: 'missing', label: `Missing (${missing.length})` }, { value: 'all', label: 'Everything' }]} />
      </div>
      {shown.length === 0 ? <p className="muted">You have every studio album.</p> : (
        <div className="disc-grid">
          {shown.map(x => (
            <div key={x.lidarr_id} className={`disc-item state-${x.state}`}>
              {x.library_id ? <Link to={`/albums/${x.library_id}`}><Cover src={x.cover} label={x.title} /></Link> : <Cover src={x.cover} label={x.title} />}
              <div className="disc-text">
                <span className="ellipsis tile-title">{x.title}</span>
                <span className="tile-sub">{x.year || '—'} · {x.secondary ? 'other' : x.type}</span>
              </div>
              <DiscState x={x} busy={busy === x.lidarr_id} onRequest={() => request(x)} />
            </div>
          ))}
        </div>
      )}
    </section>
  )
}

function DiscState({ x, busy, onRequest }: { x: DiscAlbum; busy: boolean; onRequest: () => void }) {
  switch (x.state) {
    case 'have': return <span className="badge ok">In library</span>
    case 'partial': return <span className="badge warn" title={`${x.have} of ${x.total} tracks`}>{x.have}/{x.total} tracks</span>
    case 'downloading': return <span className="badge accent">Downloading{x.download_total ? ` ${Math.round(100 - (x.download_left || 0) * 100 / x.download_total)}%` : ''}</span>
    case 'requested': return <span className="badge">Wanted</span>
    case 'upcoming': return <span className="badge">Not out yet</span>
  }
  return <button type="button" className="btn sm" onClick={onRequest} disabled={busy}>{busy ? <Spinner /> : <Icon name="download" size={13} />}Get it</button>
}
