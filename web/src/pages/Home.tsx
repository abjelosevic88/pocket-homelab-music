import { useEffect } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { useApp } from '../lib/app-context'
import { V1, longDuration, tz } from '../lib/music'
import { player } from '../lib/player-react'
import type { Home as HomeT, Mix, Song } from '../lib/types'
import { Icon } from '../components/icons'
import { AlbumCard, ArtistCard, Cover, MixCard, Shelf } from '../components/music'
import { openCreate, shuffleLibrary } from '../components/Layout'
import { Loading, errMsg, useAsync, useToast } from '../components/ui'
import { isLibraryChange, useLiveRefresh } from '../lib/live'

export async function playMix(m: Pick<Mix, 'key' | 'name'>) {
  const r = await api.get<{ mix: Mix; songs: Song[] }>(`${V1}/mixes/${m.key}`)
  player.playSongs(r.songs, 0, { kind: 'mix', name: r.mix.name, link: `/mixes/${m.key}` })
}

function greeting() {
  const h = new Date().getHours()
  return h < 5 ? 'Good night' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening'
}

export default function Home() {
  const { user } = useApp()
  const { toast } = useToast()
  const { data: h, error, reload } = useAsync(() => api.get<HomeT>(`${V1}/home?tz=${tz()}`), [])
  useEffect(() => {
    const r = () => reload()
    window.addEventListener('pmu:synced', r)
    return () => window.removeEventListener('pmu:synced', r)
  }, [reload])
  // plays (here, another tab, a phone app), stars and playlists change what Home shows
  useLiveRefresh(ev => ev.type === 'listens' || ev.type === 'playlists' || isLibraryChange(ev), reload, 600)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!h) return <Loading />
  const first = (user?.name || '').split(' ')[0]
  if (h.not_connected) return <NotConnected message={h.not_connected} />
  if (!h.sync.synced_at) return <FirstSync progress={h.sync.progress} error={h.sync.error} />
  const t = h.today
  return (
    <div className="home">
      <header className="home-hero">
        <div className="grow">
          <h1>{greeting()}{first ? `, ${first}` : ''}</h1>
          <p className="muted">
            {t && t.plays > 0 ? <>{longDuration(t.seconds)} of music today</> : <>{h.totals.albums.toLocaleString()} albums · {h.totals.songs.toLocaleString()} songs · {longDuration(h.totals.seconds)} of music</>}
            {h.streak && h.streak.current > 1 && <> · <span className="streak"><Icon name="zap" size={13} />{h.streak.current}-day streak</span></>}
          </p>
        </div>
        <div className="row wrap">
          <button type="button" className="btn" onClick={() => shuffleLibrary().catch(e => toast(errMsg(e), 'err'))}><Icon name="shuffle" size={16} />Shuffle all</button>
          <button type="button" className="btn primary" onClick={() => openCreate('mix')}><Icon name="sparkles" size={16} />Make a mix</button>
        </div>
      </header>

      {h.mixes && h.mixes.length > 0 && (
        <Shelf title="Made for you" sub="New every day, from what you play" to="/mixes">
          {h.mixes.map(m => <MixCard key={m.key} m={m} onPlay={() => playMix(m).catch(e => toast(errMsg(e), 'err'))} />)}
        </Shelf>
      )}
      {h.jump_back && h.jump_back.length > 0 && <Shelf title="Jump back in" to="/history">{h.jump_back.map(a => <AlbumCard key={a.id} a={a} />)}</Shelf>}
      {h.requests && h.requests.some(r => r.state === 'arrived') && (
        <Shelf title="Just arrived" sub="Albums you asked for" to="/requests">
          {h.requests.filter(r => r.state === 'arrived').map(r => (
            <Link key={r.id} to={`/requests`} className="card-tile"><span className="tile-art"><Cover src={r.cover_url} label={r.album} /></span><span className="tile-title ellipsis">{r.album}</span><span className="tile-sub ellipsis">{r.artist}</span></Link>
          ))}
        </Shelf>
      )}
      {h.recently_added && h.recently_added.length > 0 && <Shelf title="Recently added" to="/albums?sort=added">{h.recently_added.map(a => <AlbumCard key={a.id} a={a} />)}</Shelf>}
      {h.top_artists && h.top_artists.length > 0 && (
        <Shelf title="Your top artists" sub="The last 30 days" to="/stats">
          {h.top_artists.filter(a => a.artist_id || a.key).map(a => <ArtistCard key={a.key || a.name} a={{ id: a.artist_id || a.key, name: a.name }} sub={`${a.plays} plays`} />)}
        </Shelf>
      )}
      {h.on_this_day && h.on_this_day.length > 0 && <Shelf title="On this day" sub="What you played on this date in earlier years">{h.on_this_day.map(a => <AlbumCard key={a.id} a={a} />)}</Shelf>}
      {h.rediscover && h.rediscover.length > 0 && <Shelf title="Rediscover" sub="Albums you loved and haven't played in half a year">{h.rediscover.map(a => <AlbumCard key={a.id} a={a} sub={`${a.artist} · ${a.plays} plays`} />)}</Shelf>}
      {h.never_played && h.never_played.length > 0 && <Shelf title="Never played" sub="From your own shelves, picked for today" to="/albums?unplayed=1&sort=random">{h.never_played.map(a => <AlbumCard key={a.id} a={a} />)}</Shelf>}
    </div>
  )
}

export function NotConnected({ message }: { message: string }) {
  return (
    <div className="empty big">
      <span className="empty-ico"><Icon name="server" size={22} /></span>
      <h3>Connect your music library</h3>
      <p className="muted" style={{ maxWidth: 460 }}>{message}</p>
      <p className="muted small" style={{ maxWidth: 460 }}>Pocket Music plays your Navidrome library (or any Subsonic server) with your own account, so your playlists, loves and play counts stay yours.</p>
      <Link to="/settings/library" className="btn primary"><Icon name="settings" size={15} />Settings → Library</Link>
    </div>
  )
}

function FirstSync({ progress, error }: { progress: number; error: string }) {
  return (
    <div className="empty big">
      <span className="empty-ico"><Icon name="disc" size={22} /></span>
      <h3>{error ? 'Your library could not be read' : 'Reading your library…'}</h3>
      {error ? <p className="muted">{error}</p> : <>
        <p className="muted" style={{ maxWidth: 440 }}>Every album, artist and song your account can see is copied into a small index, so browsing, search, mixes and stats are instant. It takes a few seconds per ten thousand songs.</p>
        <div className="sync-bar"><span style={{ width: `${Math.max(4, progress)}%` }} /></div>
      </>}
      {error && <Link to="/settings/library" className="btn">Settings → Library</Link>}
    </div>
  )
}
