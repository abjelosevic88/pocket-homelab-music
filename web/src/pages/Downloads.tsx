// Downloads: what is kept on this device for offline listening.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { albumCover, longDuration } from '../lib/music'
import { offlineSupported, removeAll, removeGroup, setDownloadQuality, useOffline } from '../lib/offline'
import { player } from '../lib/player-react'
import { fmtBytes } from '../lib/format'
import { Icon } from '../components/icons'
import { Cover, SongTable } from '../components/music'
import { Confirm, Empty, PageHeader, Segmented, useToast } from '../components/ui'

const LINK: Record<string, (k: string) => string> = { album: k => `/albums/${k.slice(6)}`, playlist: k => `/playlists/${k.slice(9)}`, mix: k => `/mixes/${k.slice(4)}` }

export default function Downloads() {
  const st = useOffline()
  const { toast } = useToast()
  const [clear, setClear] = useState(false)
  const [online, setOnline] = useState(navigator.onLine)
  useEffect(() => {
    const on = () => setOnline(true), off = () => setOnline(false)
    window.addEventListener('online', on); window.addEventListener('offline', off)
    return () => { window.removeEventListener('online', on); window.removeEventListener('offline', off) }
  }, [])
  if (!offlineSupported()) return <><PageHeader title="Downloads" /><Empty title="This browser can't keep songs offline" icon="download">Use Safari, Chrome, Edge or Firefox — and open Pocket Music over https.</Empty></>
  const songs = Object.values(st.songs).sort((a, b) => b.at - a.at).map(o => o.song)
  const source = { kind: 'playlist' as const, name: 'Downloads', link: '/downloads' }
  return (
    <div className="stack loose">
      <PageHeader title="Downloads" sub={<>{songs.length} song{songs.length === 1 ? '' : 's'} on this device · {fmtBytes(st.bytes)}{!online && <> · <b>offline</b> — these play anyway</>}</>}
        actions={songs.length > 0 && <>
          <button type="button" className="btn primary" onClick={() => player.playSongs(songs, 0, source)}><Icon name="play" size={15} />Play all</button>
          <button type="button" className="btn" onClick={() => player.playSongs(songs, -1, source, true)}><Icon name="shuffle" size={15} />Shuffle</button>
        </>} />
      {st.active && <div className="callout"><span className="spinner" /> Downloading “{st.active.title}” — {st.active.done + 1} of {st.active.total}. Keep this page open; it carries on in the background while the app is open.</div>}
      {st.failed > 0 && !st.active && <div className="callout danger"><Icon name="alert" size={15} />{st.failed} song{st.failed === 1 ? '' : 's'} could not be downloaded. Try again from the album or playlist.</div>}
      <div className="row wrap dl-quality">
          <span className="muted small">Download as</span>
          <Segmented size="sm" value={st.quality} onChange={q => { setDownloadQuality(q); toast('New downloads use this; songs already here stay as they are') }} options={[
            { value: 'original', label: 'Original file' }, { value: '320', label: '320 kbps' }, { value: '256', label: '256 kbps' }, { value: '128', label: '128 kbps' }]} />
          <span className="muted small">Lossless albums are 300–600 MB; 256 kbps is about a fifth of that.</span>
      </div>
      {st.groups.length === 0 ? <Empty title="Nothing downloaded yet" icon="download">Open an album, playlist or mix and press <Icon name="download" size={13} /> to keep it on this device for the plane, the car or the underground.</Empty> : <>
        <div className="dl-groups">
          {st.groups.map(g => {
            const mine = Object.values(st.songs).filter(o => o.groups.includes(g.key))
            const secs = mine.reduce((t, o) => t + (o.song.duration || 0), 0)
            const bytes = mine.reduce((t, o) => t + o.bytes, 0)
            const cover = g.cover || mine[0]?.song.album_id
            return (
              <div key={g.key} className="dl-group">
                <Cover src={cover ? albumCover(cover, 120) : ''} size={52} label={g.name} />
                <div className="grow">
                  <Link to={(LINK[g.kind] || (() => '/downloads'))(g.key)} className="dl-name ellipsis">{g.name}</Link>
                  <div className="muted small">{g.kind === 'album' ? 'Album' : g.kind === 'mix' ? 'Mix' : 'Playlist'} · {mine.length} songs · {longDuration(secs)} · {fmtBytes(bytes)}</div>
                </div>
                <button type="button" className="btn ghost icon-only" aria-label={`Play ${g.name}`} onClick={() => player.playSongs(mine.map(o => o.song), 0, { ...source, name: g.name })}><Icon name="play" size={16} /></button>
                <button type="button" className="btn ghost icon-only danger-text" aria-label={`Remove ${g.name} from this device`} title="Remove from this device" onClick={() => removeGroup(g.key)}><Icon name="trash" size={15} /></button>
              </div>
            )
          })}
        </div>
        <SongTable songs={songs} source={source} showCover />
        <div className="row"><button type="button" className="btn ghost danger-text" onClick={() => setClear(true)}><Icon name="trash" size={14} />Remove all downloads</button></div>
      </>}
      {clear && <Confirm title="Remove all downloads?" message="Everything kept on this device for offline listening is deleted. Your library is not touched." confirmLabel="Remove all" onCancel={() => setClear(false)} onConfirm={async () => { setClear(false); await removeAll() }} />}
    </div>
  )
}
