import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, albumCover, longDuration } from '../lib/music'
import { player } from '../lib/player-react'
import type { Album as AlbumT, Song } from '../lib/types'
import { Icon } from '../components/icons'
import { AddToPlaylist, AlbumCard, Cover, HeroActions, MenuTrigger, Shelf, SongTable, StarButton, startRadio, useCoverColor } from '../components/music'
import { useCrumb } from '../components/Layout'
import { NotConnected } from './Home'
import { ApiError } from '../lib/api'
import { Loading, errMsg, useAsync, useToast } from '../components/ui'

interface AlbumResp { album: AlbumT; songs: Song[]; more: AlbumT[]; genres: string[]; quality: string; notes?: string; lastfm_url?: string }

export default function Album() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const { data, error, setData } = useAsync(() => api.get<AlbumResp>(`${V1}/albums/${id}?info=1`), [id])
  const [addTo, setAddTo] = useState<Song[] | null>(null)
  const [notesOpen, setNotesOpen] = useState(false)
  const color = useCoverColor(albumCover(id, 64))
  useCrumb(data?.album.name)
  if (error) {
    return error.includes('connect') || error.includes('Library') ? <NotConnected message={error} /> : <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  }
  if (!data) return <Loading />
  const { album: a, songs } = data
  const source = { kind: 'album' as const, name: a.name, link: `/albums/${a.id}` }
  const dur = songs.reduce((t, s) => t + s.duration, 0)
  return (
    <div className="detail" style={color ? { ['--art' as string]: color } : undefined}>
      <div className="detail-glow" />
      <header className="detail-hero">
        <Cover src={albumCover(a.id, 600)} className="hero-cover" label={a.name} />
        <div className="hero-text">
          <p className="eyebrow">{a.compilation ? 'Compilation' : songs.length <= 3 && dur < 1500 ? 'Single / EP' : 'Album'}</p>
          <h1 className="hero-title">{a.name}</h1>
          <p className="hero-by">{a.artist_id ? <Link to={`/artists/${a.artist_id}`}>{a.artist}</Link> : a.artist}</p>
          <p className="hero-meta">
            {a.year ? <span>{a.year}</span> : null}
            <span>{songs.length} songs, {longDuration(dur)}</span>
            {data.quality && <span className="badge">{data.quality}</span>}
            {a.plays ? <span>{a.plays} plays by you</span> : <span className="muted">never played</span>}
          </p>
          {data.genres.length > 0 && <div className="chips">{data.genres.map(g => <Link key={g} to={`/genres/${encodeURIComponent(g)}`} className="chip">{g}</Link>)}</div>}
          <HeroActions onPlay={() => player.playSongs(songs, 0, source)} onShuffle={() => player.playSongs(songs, -1, source, true)}>
            <StarButton kind="album" id={a.id} starred={!!a.starred_at} size={19} className="lg" onChange={on => setData({ ...data, album: { ...a, starred_at: on ? 'now' : '' } })} />
            <MenuTrigger items={() => [
              { label: 'Play next', icon: 'queue', onSelect: () => { player.playNext(songs); toast('Plays next') } },
              { label: 'Add to queue', icon: 'list-plus', onSelect: () => { player.addToQueue(songs); toast('Added to the queue') } },
              { label: 'Start radio', icon: 'radio', onSelect: () => startRadio({ album: a.id }, a.name + ' radio').catch(e => toast(errMsg(e), 'err')) },
              { label: 'Add to playlist…', icon: 'playlist', onSelect: () => setAddTo(songs) },
              'sep',
              { label: 'Go to artist', icon: 'user', onSelect: () => navigate(`/artists/${a.artist_id}`), disabled: !a.artist_id },
            ]} />
          </HeroActions>
        </div>
      </header>
      <SongTable songs={songs} source={source} numbered="track" showAlbum={false} showArtist={songs.some(s => s.artist !== a.artist)} discs showPlays />
      {data.notes && (
        <section className="notes">
          <h2>About this album</h2>
          <p className={notesOpen ? '' : 'clamp'}>{data.notes}</p>
          <div className="row">{data.notes.length > 300 && <button type="button" className="link-btn" onClick={() => setNotesOpen(o => !o)}>{notesOpen ? 'Less' : 'More'}</button>}{data.lastfm_url && <a href={data.lastfm_url} target="_blank" rel="noreferrer" className="small muted">Last.fm <Icon name="external" size={11} /></a>}</div>
        </section>
      )}
      {data.more.length > 0 && <Shelf title={`More by ${a.artist}`} to={a.artist_id ? `/artists/${a.artist_id}` : undefined}>{data.more.map(x => <AlbumCard key={x.id} a={x} sub={x.year ? String(x.year) : ' '} />)}</Shelf>}
      {addTo && <AddToPlaylist songs={addTo} onClose={() => setAddTo(null)} />}
    </div>
  )
}

export function isNotConnected(e: unknown) { return e instanceof ApiError && e.data.code === 'not_connected' }
