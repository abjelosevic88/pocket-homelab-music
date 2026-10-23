// Lidarr: new and upcoming releases by your artists, requests, finding any album.
import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, ApiError } from '../lib/api'
import { V1 } from '../lib/music'
import type { Release, Request } from '../lib/types'
import { fmtDate } from '../lib/format'
import { Icon } from '../components/icons'
import { Cover } from '../components/music'
import { Confirm, Empty, Loading, Segmented, Spinner, errMsg, useDebounce, useToast } from '../components/ui'

function LidarrOff({ message }: { message: string }) {
  return (
    <div className="empty big">
      <span className="empty-ico"><Icon name="download" size={22} /></span>
      <h3>Connect Lidarr to find new music</h3>
      <p className="muted" style={{ maxWidth: 480 }}>{message}</p>
      <p className="muted small" style={{ maxWidth: 480 }}>With Lidarr, artist pages show the albums you are missing, new releases by your artists appear here, and any album is one click from your library.</p>
      <Link to="/settings/lidarr" className="btn primary">Settings → Lidarr</Link>
    </div>
  )
}

export function Releases() {
  const { toast } = useToast()
  const [data, setData] = useState<Release[] | null>(null)
  const [off, setOff] = useState('')
  const [show, setShow] = useState<'recent' | 'upcoming'>('recent')
  const [busy, setBusy] = useState(0)
  const load = () => api.get<Release[]>(`${V1}/releases`).then(setData).catch(e => { if (e instanceof ApiError && e.data.code === 'lidarr_off') setOff(e.message); else toast(errMsg(e), 'err') })
  useEffect(() => { load() }, []) // eslint-disable-line react-hooks/exhaustive-deps
  if (off) return <LidarrOff message={off} />
  if (!data) return <Loading />
  const today = new Date().toISOString().slice(0, 10)
  const shown = data.filter(r => show === 'upcoming' ? r.date > today : r.date <= today).sort((a, b) => show === 'upcoming' ? a.date.localeCompare(b.date) : b.date.localeCompare(a.date))
  const request = async (r: Release) => {
    setBusy(r.lidarr_id)
    try { await api.post(`${V1}/requests`, { lidarr_id: r.lidarr_id }); toast(`Lidarr is looking for “${r.title}”`); load() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(0) }
  }
  return (
    <div>
      <div className="page-header">
        <div><h1>New releases</h1><div className="sub">Albums by artists in Lidarr from the last six weeks and the next four months.</div></div>
        <div className="actions"><Link to="/requests?find=1" className="btn"><Icon name="search" size={15} />Find any album</Link></div>
      </div>
      <div className="filters"><Segmented size="sm" value={show} onChange={setShow} options={[{ value: 'recent', label: 'Out now' }, { value: 'upcoming', label: 'Coming soon' }]} /></div>
      {shown.length === 0 ? <Empty title={show === 'upcoming' ? 'Nothing announced' : 'No new releases'} icon="compass">Lidarr lists releases of the artists it follows.</Empty> : (
        <div className="grid-tiles">
          {shown.map(r => (
            <div key={r.lidarr_id} className="card-tile static">
              <span className="tile-art">{r.library_id ? <Link to={`/albums/${r.library_id}`}><Cover src={r.cover} label={r.title} /></Link> : <Cover src={r.cover} label={r.title} />}</span>
              <span className="tile-title ellipsis">{r.title}</span>
              <span className="tile-sub ellipsis">{r.artist_id ? <Link to={`/artists/${r.artist_id}`}>{r.artist}</Link> : r.artist} · {fmtDate(r.date)}</span>
              <span className="tile-action">
                {r.state === 'have' ? <span className="badge ok">In library</span> : r.state === 'upcoming' ? <span className="badge">{r.monitored ? 'Wanted' : 'Coming'}</span> : r.state === 'requested' ? <span className="badge">Wanted</span>
                  : <button type="button" className="btn sm" onClick={() => request(r)} disabled={busy === r.lidarr_id}>{busy === r.lidarr_id ? <Spinner /> : <Icon name="download" size={13} />}Get it</button>}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

interface SearchRes {
  artists: { foreign_id: string; name: string; overview: string; image: string; in_lidarr: boolean; library_id: string }[]
  albums: { foreign_id: string; artist_foreign_id: string; title: string; artist: string; year: number; type: string; cover: string; library_id: string }[]
}

export function Requests() {
  const { toast } = useToast()
  const [sp] = useSearchParams()
  const [reqs, setReqs] = useState<Request[] | null>(null)
  const [off, setOff] = useState('')
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 400)
  const [res, setRes] = useState<SearchRes | null>(null)
  const [searching, setSearching] = useState(false)
  const [busy, setBusy] = useState('')
  const [del, setDel] = useState<Request | null>(null)
  const load = () => api.get<Request[]>(`${V1}/requests`).then(setReqs).catch(e => toast(errMsg(e), 'err'))
  useEffect(() => { load(); api.get<{ ready: boolean; detail: string }>(`${V1}/lidarr/status`).then(s => { if (!s.ready) setOff(s.detail) }).catch(() => {}) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!dq.trim()) { setRes(null); return }
    setSearching(true)
    api.get<SearchRes>(`${V1}/lidarr/search?q=${encodeURIComponent(dq)}`).then(setRes).catch(e => toast(errMsg(e), 'err')).finally(() => setSearching(false))
  }, [dq, toast])
  const request = async (a: SearchRes['albums'][0]) => {
    setBusy(a.foreign_id)
    try {
      await api.post(`${V1}/requests`, { foreign_id: a.foreign_id, artist_foreign_id: a.artist_foreign_id, artist: a.artist, title: a.title, year: a.year, cover: a.cover })
      toast(`Lidarr is looking for “${a.title}”`); load()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy('') }
  }
  if (off && !reqs?.length) return <LidarrOff message={off} />
  const open = (reqs || []).filter(r => r.state === 'requested' || r.state === 'downloading')
  const done = (reqs || []).filter(r => r.state === 'arrived' || r.state === 'failed')
  return (
    <div>
      <div className="page-header"><div><h1>Requests</h1><div className="sub">Albums you asked Lidarr for. You get a push when one arrives (Settings → Notifications).</div></div></div>
      <section className="section">
        <div className="find-box">
          <Icon name="search" size={18} />
          <input value={q} onChange={e => setQ(e.target.value)} placeholder="Find any album or artist (MusicBrainz, through Lidarr)" autoFocus={sp.get('find') === '1'} />
          {searching && <Spinner />}
        </div>
        {res && (
          <div className="find-results">
            {res.albums.length === 0 && res.artists.length === 0 && <p className="muted">Nothing found.</p>}
            {res.albums.map(a => (
              <div key={a.foreign_id} className="find-row">
                <Cover src={a.cover} size={52} label={a.title} />
                <div className="grow"><div className="ellipsis"><b>{a.title}</b></div><div className="muted small ellipsis">{a.artist} · {a.year || '—'} · {a.type}</div></div>
                {a.library_id ? <Link to={`/albums/${a.library_id}`} className="badge ok">In library</Link>
                  : <button type="button" className="btn sm primary" disabled={busy === a.foreign_id} onClick={() => request(a)}>{busy === a.foreign_id ? <Spinner /> : <Icon name="download" size={13} />}Get it</button>}
              </div>
            ))}
            {res.artists.length > 0 && <p className="muted small mt-s">Artists: {res.artists.map((a, i) => <span key={a.foreign_id}>{i ? ', ' : ''}{a.library_id ? <Link to={`/artists/${a.library_id}`}>{a.name}</Link> : a.name}</span>)} — search for an album title to request it.</p>}
          </div>
        )}
      </section>
      {!reqs && <Loading />}
      {reqs && reqs.length === 0 && !res && <Empty title="No requests yet" icon="download">Search above, or use “Get it” on an artist’s missing albums.</Empty>}
      {open.length > 0 && <section className="section"><div className="section-head"><h2>Waiting</h2></div><ReqList items={open} onDelete={setDel} /></section>}
      {done.length > 0 && <section className="section"><div className="section-head"><h2>Done</h2></div><ReqList items={done} onDelete={setDel} /></section>}
      {del && <Confirm title={`Forget “${del.album}”?`} message="It disappears from this list. Lidarr keeps the album monitored; unmonitor it there to stop the search." confirmLabel="Forget" onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/requests/${del.id}`).catch(() => {}); setDel(null); load() }} />}
    </div>
  )
}

function ReqList({ items, onDelete }: { items: Request[]; onDelete: (r: Request) => void }) {
  return (
    <div className="req-list">
      {items.map(r => (
        <div key={r.id} className={`req-row state-${r.state}`}>
          <Cover src={r.cover_url} size={52} label={r.album} />
          <div className="grow"><div className="ellipsis"><b>{r.album}</b></div><div className="muted small ellipsis">{r.artist}{r.year ? ` · ${r.year}` : ''} · asked {fmtDate(r.created_at.slice(0, 10))}</div></div>
          <span className={`badge ${r.state === 'arrived' ? 'ok' : r.state === 'failed' ? 'danger' : r.state === 'downloading' ? 'accent' : ''}`}>{r.state === 'requested' ? 'Searching' : r.state === 'downloading' ? `Downloading ${r.detail}` : r.state === 'arrived' ? 'In your library' : 'Failed'}</span>
          <button type="button" className="btn ghost icon-only sm" aria-label="Forget" onClick={() => onDelete(r)}><Icon name="x" size={14} /></button>
        </div>
      ))}
    </div>
  )
}
