import { useMemo } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, albumCover, artistImage, longDuration, tz } from '../lib/music'
import type { Stats as StatsT, Top } from '../lib/types'
import { fmtDate } from '../lib/format'
import { Icon } from '../components/icons'
import { Cover } from '../components/music'
import { BarList, Columns, type Point } from '../components/charts'
import { Empty, Loading, Segmented, useAsync } from '../components/ui'
import { useLiveRefresh } from '../lib/live'
import { Select } from '../components/Select'

const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const hours = (s: number) => Math.round(s / 360) / 10

function delta(cur: number, prev: number) {
  if (!prev) return null
  const d = Math.round(((cur - prev) / prev) * 100)
  return d
}

function Tile({ label, value, d, sub }: { label: string; value: string; d?: number | null; sub?: string }) {
  return (
    <div className="stat-tile">
      <span className="stat-label">{label}</span>
      <span className="stat-value">{value}</span>
      {d != null ? <span className={`stat-delta ${d > 0 ? 'up' : d < 0 ? 'down' : ''}`}><Icon name={d >= 0 ? 'trending-up' : 'trending-down'} size={12} />{d > 0 ? '+' : ''}{d}% vs the period before</span> : sub ? <span className="stat-delta">{sub}</span> : null}
    </div>
  )
}

export default function Stats() {
  const [sp, setSp] = useSearchParams()
  const period = sp.get('period') || 'month'
  const { data: s, error, loading, reload } = useAsync(() => api.get<StatsT>(`${V1}/stats?period=${period}&tz=${tz()}&limit=10`), [period])
  useLiveRefresh(ev => ev.type === 'listens', reload, 800)
  const setPeriod = (p: string) => { const n = new URLSearchParams(sp); n.set('period', p); setSp(n, { replace: true }) }
  const timeline = useMemo<Point[]>(() => {
    if (!s) return []
    if (s.timeline_by === 'month') return s.timeline.map(b => ({ key: b.key, label: new Date(b.key + '-01T12:00').toLocaleDateString(undefined, { month: 'short', year: '2-digit' }), value: hours(b.seconds), detail: `${longDuration(b.seconds)} · ${b.plays} plays` }))
    // every day of the period, empty days included
    const by = new Map(s.timeline.map(b => [b.key, b]))
    const out: Point[] = []
    const d = new Date(s.period.from + 'T12:00')
    const end = new Date(s.period.to + 'T12:00')
    while (d <= end) {
      const k = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
      const b = by.get(k)
      out.push({ key: k, label: d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }), value: b ? Math.round(b.seconds / 60) : 0, detail: b ? `${longDuration(b.seconds)} · ${b.plays} plays` : 'no music' })
      d.setDate(d.getDate() + 1)
    }
    return out
  }, [s])
  const clock = useMemo<Point[]>(() => {
    if (!s) return []
    const by = new Map(s.hours.map(b => [Number(b.key), b]))
    return Array.from({ length: 24 }, (_, h) => { const b = by.get(h); return { key: String(h), label: String(h).padStart(2, '0'), value: b?.plays || 0, detail: b ? `${b.plays} plays · ${longDuration(b.seconds)}` : 'nothing' } })
  }, [s])
  const week = useMemo<Point[]>(() => {
    if (!s) return []
    const by = new Map(s.weekdays.map(b => [Number(b.key), b]))
    return [1, 2, 3, 4, 5, 6, 0].map(w => { const b = by.get(w); return { key: String(w), label: WEEKDAYS[w], value: b ? hours(b.seconds) : 0, detail: b ? `${longDuration(b.seconds)} · ${b.plays} plays` : 'nothing' } })
  }, [s])
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!s) return <Loading />
  const t = s.totals, p = s.previous
  const yearly = /^\d{4}$/.test(period)
  return (
    <div className={`stats ${loading ? 'refetching' : ''}`}>
      <div className="page-header">
        <div><h1>Stats</h1><div className="sub">{s.period.label}{s.period.key !== 'all' ? ` · ${fmtDate(s.period.from)} – ${fmtDate(s.period.to)}` : ''}</div></div>
        <div className="actions">
          {yearly && <Link to={`/recap?period=${period}`} className="btn primary"><Icon name="sparkles" size={15} />Your {period} in music</Link>}
        </div>
      </div>
      <div className="filters">
        <Segmented size="sm" value={['week', 'month', 'year', 'all'].includes(period) ? period : ''} onChange={setPeriod} options={[{ value: 'week', label: '7 days' }, { value: 'month', label: '30 days' }, { value: 'year', label: '12 months' }, { value: 'all', label: 'All time' }]} />
        {s.years.length > 0 && <Select ariaLabel="Year" className="inline" value={yearly ? period : ''} onChange={setPeriod} placeholder="A year…" options={s.years.map(y => ({ value: y, label: y }))} />}
      </div>
      {s.all_plays === 0 ? <Empty title="No plays yet" icon="bar">Stats grow from what you play here. Past listening can be imported from Maloja or ListenBrainz in Settings → History.</Empty> : <>
        <div className="stat-tiles">
          <Tile label="Time listened" value={longDuration(t.seconds)} d={s.period.key !== 'all' ? delta(t.seconds, p.seconds) : null} />
          <Tile label="Plays" value={t.plays.toLocaleString()} d={s.period.key !== 'all' ? delta(t.plays, p.plays) : null} />
          <Tile label="Artists" value={t.artists.toLocaleString()} sub={`${t.albums.toLocaleString()} albums`} />
          <Tile label="New to you" value={t.new_songs.toLocaleString()} sub="songs heard for the first time" />
          <Tile label="Streak" value={`${s.streak.current} day${s.streak.current === 1 ? '' : 's'}`} sub={`longest ${s.streak.longest} · music on ${t.days} days`} />
        </div>
        <section className="card chart-card">
          <div className="card-head"><div><h2>{s.timeline_by === 'month' ? 'Hours per month' : 'Minutes per day'}</h2></div></div>
          <div className="card-body"><Columns data={timeline} ariaLabel={s.timeline_by === 'month' ? 'Month' : 'Day'} labelEvery={timeline.length > 40 ? 7 : timeline.length > 14 ? 3 : 1} format={n => s.timeline_by === 'month' ? `${n}h` : `${n}`} /></div>
        </section>
        <div className="two-col">
          <section className="card chart-card">
            <div className="card-head"><div><h2>When you listen</h2><p className="muted small">Plays by hour of the day</p></div></div>
            <div className="card-body"><Columns data={clock} ariaLabel="Hour" labelEvery={3} height={150} /></div>
          </section>
          <section className="card chart-card">
            <div className="card-head"><div><h2>Your week</h2><p className="muted small">Hours by day of the week</p></div></div>
            <div className="card-body"><Columns data={week} ariaLabel="Weekday" height={150} format={n => `${n}h`} /></div>
          </section>
        </div>
        <div className="three-col tops">
          <TopList title="Top artists" items={s.artists} kind="artist" />
          <TopList title="Top albums" items={s.albums} kind="album" />
          <TopList title="Top songs" items={s.songs} kind="song" />
        </div>
        {s.genres.length > 0 && (
          <section className="card chart-card">
            <div className="card-head"><div><h2>Top genres</h2><p className="muted small">Plays per genre</p></div></div>
            <div className="card-body"><BarList data={s.genres.map(g => ({ key: g.key, label: g.name, value: g.plays, detail: `${g.plays} plays` }))} /></div>
          </section>
        )}
      </>}
    </div>
  )
}

function TopList({ title, items, kind }: { title: string; items: Top[]; kind: 'artist' | 'album' | 'song' }) {
  return (
    <section className="card top-card">
      <div className="card-head"><h2>{title}</h2></div>
      <ol className="top-list">
        {items.map((x, i) => {
          const img = kind === 'artist' ? artistImage(x.artist_id || x.key, 80) : albumCover(x.album_id, 80)
          const to = kind === 'artist' ? (x.artist_id ? `/artists/${x.artist_id}` : '') : x.album_id ? `/albums/${x.album_id}` : ''
          const body = <>
            <span className="top-rank tnum">{i + 1}</span>
            <Cover src={img} size={40} round={kind === 'artist'} label={x.name} />
            <span className="grow"><span className="ellipsis block top-name">{x.name}</span>{kind !== 'artist' && <span className="ellipsis block muted small">{x.artist}</span>}</span>
            <span className="muted small tnum">{x.plays}</span>
          </>
          return <li key={x.key + x.name}>{to ? <Link to={to} className="top-row">{body}</Link> : <span className="top-row">{body}</span>}</li>
        })}
        {items.length === 0 && <li className="muted small" style={{ padding: 12 }}>Nothing yet.</li>}
      </ol>
    </section>
  )
}

export function Recap() {
  const [sp] = useSearchParams()
  const period = sp.get('period') || String(new Date().getFullYear())
  const recap = useAsync(() => api.get<{ period: { label: string }; totals: StatsT['totals']; artists: Top[]; songs: Top[]; albums: Top[]; genres: Top[]; peak_hour: string; peak_month: string; biggest_day: { key: string; seconds: number; plays: number }; streak: { longest: number } }>(`${V1}/recap?period=${period}&tz=${tz()}`), [period])
  const { data: r, error } = recap
  useLiveRefresh(ev => ev.type === 'listens', recap.reload, 800)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!r) return <Loading />
  const top = r.artists[0]
  const h = Number(r.peak_hour)
  const daypart = isNaN(h) ? '' : h < 6 ? 'a night owl' : h < 12 ? 'a morning listener' : h < 18 ? 'an afternoon listener' : 'an evening listener'
  return (
    <div className="recap">
      <section className="recap-card hero" style={top ? { backgroundImage: `linear-gradient(180deg, rgba(0,0,0,.25), rgba(0,0,0,.75)), url(${artistImage(top.artist_id || top.key, 900)})` } : undefined}>
        <p className="eyebrow">Your {r.period.label} in music</p>
        <h1>{longDuration(r.totals.seconds)}</h1>
        <p>{r.totals.plays.toLocaleString()} songs · {r.totals.artists.toLocaleString()} artists · {r.totals.new_songs.toLocaleString()} new to you</p>
      </section>
      {top && <section className="recap-card"><p className="eyebrow">Your top artist</p><h2>{top.name}</h2><p className="muted">{top.plays} plays, {longDuration(top.seconds)}</p>
        <ol className="recap-list">{r.artists.slice(1).map((a, i) => <li key={a.key + a.name}><span className="tnum">{i + 2}</span>{a.name}<span className="muted"> · {a.plays}</span></li>)}</ol></section>}
      {r.songs[0] && <section className="recap-card"><p className="eyebrow">Your song</p><h2>{r.songs[0].name}</h2><p className="muted">{r.songs[0].artist} · {r.songs[0].plays} plays</p>
        <ol className="recap-list">{r.songs.slice(1).map((a, i) => <li key={a.key + a.name}><span className="tnum">{i + 2}</span>{a.name}<span className="muted"> · {a.artist}</span></li>)}</ol></section>}
      {r.albums[0] && <section className="recap-card art"><Cover src={albumCover(r.albums[0].album_id, 400)} className="recap-cover" label={r.albums[0].name} /><div><p className="eyebrow">Album of the year</p><h2>{r.albums[0].name}</h2><p className="muted">{r.albums[0].artist} · {r.albums[0].plays} plays</p></div></section>}
      <section className="recap-card facts">
        {r.genres[0] && <div><p className="eyebrow">Your sound</p><h3>{r.genres.slice(0, 3).map(g => g.name).join(', ')}</h3></div>}
        {daypart && <div><p className="eyebrow">You are</p><h3>{daypart}</h3><p className="muted small">most plays around {String(h).padStart(2, '0')}:00</p></div>}
        {r.biggest_day.key && <div><p className="eyebrow">Biggest day</p><h3>{fmtDate(r.biggest_day.key)}</h3><p className="muted small">{longDuration(r.biggest_day.seconds)} of music</p></div>}
        {r.streak.longest > 1 && <div><p className="eyebrow">Longest streak</p><h3>{r.streak.longest} days</h3></div>}
      </section>
      <div className="center"><Link to={`/stats?period=${period}`} className="btn">All the numbers</Link></div>
    </div>
  )
}
