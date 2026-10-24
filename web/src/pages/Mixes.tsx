import { useParams } from 'react-router-dom'
import { api } from '../lib/api'
import { OfflineButton } from '../components/OfflineButton'
import { V1, longDuration, tz } from '../lib/music'
import { player } from '../lib/player-react'
import type { Mix, Song } from '../lib/types'
import { Icon } from '../components/icons'
import { Collage, HeroActions, MixCard, SongTable } from '../components/music'
import { openCreate, useCrumb } from '../components/Layout'
import { Empty, Loading, errMsg, useAsync, useToast } from '../components/ui'
import { playMix } from './Home'

export function Mixes() {
  const { toast } = useToast()
  const { data, error } = useAsync(() => api.get<Mix[]>(`${V1}/mixes?tz=${tz()}`), [])
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  return (
    <div>
      <div className="page-header">
        <div><h1>Mixes</h1><div className="sub">Made fresh every day from your library and what you play — the more you listen, the better they get.</div></div>
        <div className="actions"><button type="button" className="btn primary" onClick={() => openCreate('mix')}><Icon name="sparkles" size={15} />Make a mix</button></div>
      </div>
      <div className="make-mix-card" onClick={() => openCreate('mix')} role="button" tabIndex={0} onKeyDown={e => { if (e.key === 'Enter') openCreate('mix') }}>
        <Icon name="sparkles" size={22} />
        <div className="grow"><b>Make a mix from a sentence</b><p className="muted small">“Calm piano for a rainy Sunday”, “80s for the gym”, “songs I’ve never played by bands I love” — only music from your own library.</p></div>
        <Icon name="chevron-right" size={18} className="muted" />
      </div>
      {data.length === 0 ? <Empty title="No mixes yet" icon="sparkles">Mixes appear once your library is indexed; they get personal as you listen.</Empty>
        : <div className="grid-tiles mixes">{data.map(m => <MixCard key={m.key} m={m} onPlay={() => playMix(m).catch(e => toast(errMsg(e), 'err'))} />)}</div>}
    </div>
  )
}

export function MixPage() {
  const { key = '' } = useParams()
  const { data, error } = useAsync(() => api.get<{ mix: Mix; songs: Song[] }>(`${V1}/mixes/${key}?tz=${tz()}`), [key])
  useCrumb(data?.mix.name)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const { mix: m, songs } = data
  const source = { kind: 'mix' as const, name: m.name, link: `/mixes/${m.key}` }
  return (
    <div className="detail" style={{ ['--mixc' as string]: m.color }}>
      <div className="detail-glow mix" />
      <header className="detail-hero">
        <span className="hero-cover"><Collage albums={m.covers} color={m.color} /></span>
        <div className="hero-text">
          <p className="eyebrow">Mix · made for you today</p>
          <h1 className="hero-title">{m.name}</h1>
          <p className="hero-desc">{m.description}</p>
          <p className="hero-meta"><span>{songs.length} songs, {longDuration(m.duration)}</span></p>
          <HeroActions onPlay={() => player.playSongs(songs, 0, source)} onShuffle={() => player.playSongs(songs, -1, source, true)}>
            <OfflineButton songs={songs} group={{ key: `mix:${m.key}`, name: m.name, kind: 'mix' }} />
            <button type="button" className="btn lg" onClick={() => api.post(`${V1}/playlists`, { name: `${m.name} · ${new Date().toLocaleDateString()}`, comment: m.description, song_ids: songs.map(s => s.id) }).then(() => window.dispatchEvent(new CustomEvent('pmu:playlists')))}><Icon name="playlist" size={16} />Save as playlist</button>
          </HeroActions>
        </div>
      </header>
      <SongTable songs={songs} source={source} showCover showPlays />
    </div>
  )
}
