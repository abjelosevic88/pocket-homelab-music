import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, albumCover, longDuration, tz } from '../lib/music'
import { player } from '../lib/player-react'
import type { Listen, Song } from '../lib/types'
import { fmtDate, fmtTime } from '../lib/format'
import { Icon } from '../components/icons'
import { Cover } from '../components/music'
import { Empty, Loading, Spinner, errMsg, useDebounce, useToast } from '../components/ui'

export default function History() {
  const { toast } = useToast()
  const [items, setItems] = useState<Listen[] | null>(null)
  const [more, setMore] = useState(false)
  const [total, setTotal] = useState(0)
  const [busy, setBusy] = useState(false)
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 250)
  const load = async (before = '') => {
    const r = await api.get<{ listens: Listen[]; total: number; more: boolean }>(`${V1}/history?tz=${tz()}&limit=150${before ? `&before=${encodeURIComponent(before)}` : ''}${dq ? `&q=${encodeURIComponent(dq)}` : ''}`)
    setTotal(r.total); setMore(r.more)
    return r.listens
  }
  useEffect(() => { setItems(null); load().then(setItems).catch(e => toast(errMsg(e), 'err')) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps
  const days = useMemo(() => {
    const out: { day: string; items: Listen[]; seconds: number }[] = []
    for (const l of items || []) {
      const d = new Date(l.played_at)
      const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
      let g = out[out.length - 1]
      if (!g || g.day !== key) { g = { day: key, items: [], seconds: 0 }; out.push(g) }
      g.items.push(l)
      g.seconds += l.ms_played ? l.ms_played / 1000 : l.duration
    }
    return out
  }, [items])
  const loadMore = async () => {
    if (!items?.length) return
    setBusy(true)
    try { const next = await load(items[items.length - 1].played_at); setItems([...items, ...next]) } finally { setBusy(false) }
  }
  const play = async (l: Listen) => {
    if (!l.song_id) return
    try { const s = await api.get<Song>(`${V1}/songs/${l.song_id}`); player.playSongs([s], 0, { kind: 'songs', name: 'History', link: '/history' }) } catch (e) { toast(errMsg(e), 'err') }
  }
  const remove = async (l: Listen) => {
    try { await api.del(`${V1}/listens/${l.id}`); setItems(items!.filter(x => x.id !== l.id)) } catch (e) { toast(errMsg(e), 'err') }
  }
  const today = new Date().toISOString().slice(0, 10)
  return (
    <div>
      <div className="page-header">
        <div><h1>History</h1><div className="sub">{total.toLocaleString()} plays — every song played here or imported from Maloja / ListenBrainz. Yours alone.</div></div>
        <div className="actions"><a className="btn" href={`${V1}/history/export`}><Icon name="download" size={15} />Export CSV</a></div>
      </div>
      <div className="filters"><input className="filter-input" value={q} onChange={e => setQ(e.target.value)} placeholder="Find a song, artist or album you played" /></div>
      {!items && <Loading />}
      {items && items.length === 0 && <Empty title={q ? 'Nothing found' : 'Nothing played yet'} icon="history">{q ? 'Try other words.' : 'Plays appear here once a song has played for half its length (or four minutes). Past plays can be imported in Settings → History.'}</Empty>}
      {days.map(g => (
        <section key={g.day} className="hist-day">
          <h2 className="hist-h">{g.day === today ? 'Today' : fmtDate(g.day)}<span className="muted small"> · {g.items.length} songs · {longDuration(g.seconds)}</span></h2>
          <div className="hist-list">
            {g.items.map(l => (
              <div key={l.id} className="hist-row">
                <span className="hist-time tnum muted small">{fmtTime(new Date(l.played_at))}</span>
                <button type="button" className="hist-art" onClick={() => play(l)} disabled={!l.song_id} aria-label={`Play ${l.title}`}><Cover src={l.album_id ? albumCover(l.album_id, 80) : ''} size={38} label={l.album || l.title} /></button>
                <div className="grow">
                  <div className="ellipsis"><b>{l.title}</b></div>
                  <div className="ellipsis muted small">{l.artist_id ? <Link to={`/artists/${l.artist_id}`}>{l.artist}</Link> : l.artist}{l.album ? <> · {l.album_id ? <Link to={`/albums/${l.album_id}`}>{l.album}</Link> : l.album}</> : null}</div>
                </div>
                {l.source !== 'web' && <span className="badge" title={`Imported from ${l.source}`}>{l.source}</span>}
                {l.device && l.source === 'web' && <span className="muted xsmall hide-mobile">{l.device}</span>}
                <button type="button" className="btn ghost icon-only sm hist-x" aria-label="Remove this play" title="Remove this play" onClick={() => remove(l)}><Icon name="x" size={13} /></button>
              </div>
            ))}
          </div>
        </section>
      ))}
      {more && <div className="center mt"><button type="button" className="btn" onClick={loadMore} disabled={busy}>{busy ? <Spinner /> : null}Older plays</button></div>}
    </div>
  )
}
