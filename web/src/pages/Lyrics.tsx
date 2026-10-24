// Every song's lyrics in one place: list, search the words, fetch the missing ones — and an editor
// (shift timings, tap to sync, LRCLIB's matches to choose from) that writes the files every app reads.
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, albumCover, clock } from '../lib/music'
import { player, usePlayer, useProgress } from '../lib/player-react'
import { useApp } from '../lib/app-context'
import { ago } from '../lib/format'
import type { LrclibMatch, LyricsFile, LyricsFileDetail, LyricsIndexPage, LyricsIndexStatus, Song } from '../lib/types'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { Cover } from '../components/music'
import { useCrumb } from '../components/Layout'
import { Empty, Kbd, Loading, Modal, Segmented, Spinner, Switch, errMsg, useDebounce, useToast } from '../components/ui'
import { usePaged } from './Library'

// ---------- LRC ----------

const STAMP = /\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]/g
const HAS_STAMP = /\[\d{1,3}:\d{1,2}(?:[.:]\d{1,3})?\]/
const META = /^\[[a-z#]+:.*\]$/i
type LrcLine = { t: number | null; text: string }

const stampMs = (m: string, s: string, f = '') => (Number(m) * 60 + Number(s)) * 1000 + Number((f || '0').padEnd(3, '0'))
const pad2 = (n: number) => String(n).padStart(2, '0')
export function fmtStamp(ms: number): string {
  const cs = Math.round(Math.max(0, ms) / 10)
  return `[${pad2(Math.floor(cs / 6000))}:${pad2(Math.floor(cs / 100) % 60)}.${pad2(cs % 100)}]`
}

/** Lines of LRC or plain text, like the server reads them: timed lines sorted, untimed ones dropped when any are timed. */
export function parseLrc(text: string): { lines: LrcLine[]; synced: boolean } {
  let lines: LrcLine[] = []
  let synced = false
  for (const raw of text.replace(/\r\n/g, '\n').split('\n')) {
    const stamps = [...raw.matchAll(STAMP)]
    const body = raw.replace(STAMP, '').trim()
    if (!stamps.length) {
      if (!META.test(body)) lines.push({ t: null, text: body })
      continue
    }
    synced = true
    for (const m of stamps) lines.push({ t: stampMs(m[1], m[2], m[3]), text: body })
  }
  if (synced) lines = lines.filter(l => l.t !== null).sort((a, b) => a.t! - b.t!)
  while (lines.length && !lines[0].text && !synced) lines.shift()
  while (lines.length && !lines[lines.length - 1].text) lines.pop()
  return { lines, synced }
}

const shiftLrc = (text: string, ms: number) => text.replace(STAMP, (_, m, s, f) => fmtStamp(stampMs(m, s, f) + ms))

/** The words without timings: verses stay apart, repeated blank lines go. */
function stripTimes(text: string): string {
  return parseLrc(text).lines.map(l => l.text).join('\n').replace(/\n{3,}/g, '\n\n')
}

function Snippet({ s }: { s: string }) {
  const parts = s.replace(/\n/g, ' / ').split(/(\x02[^\x03]*\x03)/)
  return <>{parts.map((p, i) => p.startsWith('\x02') ? <mark key={i}>{p.slice(1, -1)}</mark> : <span key={i}>{p}</span>)}</>
}

function KindBadge({ f }: { f: LyricsFile }) {
  if (f.kind === 'synced') return <span className="badge ok" title={f.source === 'lrc' ? '.lrc file beside the song' : "In the song's tags"}>Synced</span>
  if (f.kind === 'plain') return <span className="badge" title={f.source === 'lrc' ? '.lrc file without timings' : "In the song's tags"}>Plain</span>
  return <span className="muted xsmall lyr-none" title={f.tried_at ? `LRCLIB was last asked ${new Date(f.tried_at).toLocaleString()}` : undefined}>{missingLabel(f)}</span>
}

/** "3 d ago" for this week, else just the day — short enough for a phone row. */
function when(iso: string): string {
  const t = Date.now() - new Date(iso).getTime() < 7 * 86400e3 ? ago(iso) : new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  return t.replace(/ /g, '\u00a0') // "5 d ago" never breaks in two
}

function missingLabel(f: LyricsFile): string {
  if (f.attempt === 'skip') return 'Kept without lyrics'
  if (f.attempt === 'none' && f.tried_at) return `Not on LRCLIB · ${when(f.tried_at)}`
  if (f.tried_at) return `Asked ${when(f.tried_at)}`
  return 'Not looked up yet'
}

const sourceFor = (id: number | string) => ({ kind: 'songs' as const, name: 'Lyrics', link: `/lyrics/${id}` })

async function playSongID(songID: string, fileID: number) {
  const s = await api.get<Song>(`${V1}/songs/${encodeURIComponent(songID)}`)
  player.playSongs([s], 0, sourceFor(fileID))
}

// ---------- the list ----------

export function LyricsLibrary() {
  const { user } = useApp()
  const admin = user?.role === 'admin'
  const nav = useNavigate()
  const { toast } = useToast()
  const [sp, setSp] = useSearchParams()
  const filter = (sp.get('filter') || 'all') as 'all' | 'synced' | 'plain' | 'missing'
  const sort = sp.get('sort') || ''
  const [q, setQ] = useState(sp.get('q') || '')
  const dq = useDebounce(q.trim(), 250)
  const set = (k: string, v: string) => { const n = new URLSearchParams(sp); if (v) n.set(k, v); else n.delete(k); setSp(n, { replace: true }) }
  useEffect(() => { if ((sp.get('q') || '') !== dq) set('q', dq) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps
  const [rev, setRev] = useState(0)
  const [meta, setMeta] = useState<Pick<LyricsIndexPage, 'counts' | 'status'> | null>(null)
  const [patched, setPatched] = useState<Record<number, LyricsFile>>({})
  const [fetching, setFetching] = useState<Record<number, boolean>>({})
  const [bulk, setBulk] = useState(false)
  const qs = `filter=${filter}&sort=${sort}&q=${encodeURIComponent(dq)}`
  const key = `${qs}|${rev}`
  useEffect(() => setPatched({}), [key])
  const { items, total, busy, error, sentinel } = usePaged<LyricsFile>(off => api.get<LyricsIndexPage>(`${V1}/lyrics-index?${qs}&limit=100&offset=${off}`)
    .then(r => { if (off === 0) setMeta({ counts: r.counts, status: r.status }); return { items: r.items, total: r.total } }), key)

  // while the folder is read or lyrics are fetched: follow along, then show the result
  const status = meta?.status
  const working = !!(status?.index.running || status?.job?.running)
  useEffect(() => {
    if (!working) return
    const t = setInterval(() => {
      api.get<LyricsIndexPage>(`${V1}/lyrics-index?${qs}&limit=1`).then(r => {
        setMeta({ counts: r.counts, status: r.status })
        if (!r.status.index.running && !r.status.job?.running) setRev(v => v + 1)
      }).catch(() => {})
    }, 3000)
    return () => clearInterval(t)
  }, [working, qs])

  const fetchOne = async (f: LyricsFile) => {
    setFetching(m => ({ ...m, [f.id]: true }))
    try {
      const d = await api.post<LyricsFileDetail>(`${V1}/lyrics-files/${f.id}/fetch`)
      setPatched(m => ({ ...m, [f.id]: { ...d.file, snippet: f.snippet } }))
      toast(`${d.file.kind === 'synced' ? 'Synced' : 'Plain'} lyrics saved for “${f.title}”`)
    } catch (e) {
      toast(errMsg(e), 'err')
      setPatched(m => ({ ...m, [f.id]: { ...f, attempt: 'none', tried_at: new Date().toISOString() } }))
    } finally { setFetching(m => ({ ...m, [f.id]: false })) }
  }
  const refresh = async () => {
    try { const st = await api.post<LyricsIndexStatus>(`${V1}/lyrics-index/refresh`); setMeta(m => m && { ...m, status: st }); toast('Reading the music folder again') } catch (e) { toast(errMsg(e), 'err') }
  }
  const stop = async () => { await api.post(`${V1}/lyrics-library/stop`).catch(() => {}) }

  const c = meta?.counts
  const sortOptions = dq
    ? [{ value: '', label: 'Best match' }, { value: 'path', label: 'A to Z by folder' }, { value: 'recent', label: 'Recently changed' }]
    : [{ value: '', label: 'A to Z by folder' }, { value: 'recent', label: 'Recently changed' }]
  if (status && !status.available) {
    return (
      <div>
        <div className="page-header"><div><h1>Lyrics</h1></div></div>
        <Empty title="Lyrics need the music folder" icon="quote">Mount the folder your music server reads into Pocket Music and set <code>MUSIC_DIR</code> — then every song's lyrics show up here, to search and edit.</Empty>
      </div>
    )
  }
  const firstRead = status && !status.index.indexed_at
  return (
    <div>
      <div className="page-header">
        <div>
          <h1>Lyrics</h1>
          <div className="sub">{c ? <>{c.all.toLocaleString()} songs · {c.synced.toLocaleString()} synced · {c.plain.toLocaleString()} plain · {c.missing.toLocaleString()} without{dq ? ` matching “${dq}”` : ''}</> : ' '}</div>
        </div>
        {admin && status && <div className="actions">
          {status.can_fetch && !!c?.missing && !status.job?.running && <button type="button" className="btn primary" onClick={() => setBulk(true)}><Icon name="download" size={15} />Fetch missing ({c.missing.toLocaleString()})</button>}
          <button type="button" className="btn" onClick={refresh} disabled={status.index.running} title="Look through the music folder for new, changed and removed songs"><Icon name="refresh" size={15} />Re-read folder</button>
        </div>}
      </div>
      {status && <StatusLine status={status} admin={admin} onStop={stop} />}
      <div className="filters">
        <input className="filter-input lyr-search" type="search" value={q} onChange={e => setQ(e.target.value)} placeholder="Search the words, a title or an artist" aria-label="Search lyrics" />
        <Segmented size="sm" value={filter} onChange={v => set('filter', v === 'all' ? '' : v)} options={[
          { value: 'all', label: <>All {c ? <span className="n">{c.all.toLocaleString()}</span> : null}</> },
          { value: 'synced', label: <>Synced {c ? <span className="n">{c.synced.toLocaleString()}</span> : null}</> },
          { value: 'plain', label: <>Plain {c ? <span className="n">{c.plain.toLocaleString()}</span> : null}</> },
          { value: 'missing', label: <>Without {c ? <span className="n">{c.missing.toLocaleString()}</span> : null}</> },
        ]} />
        <Select ariaLabel="Sort" className="inline" value={sort} onChange={v => set('sort', v)} options={sortOptions} />
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!items && !error && <Loading />}
      {items && items.length === 0 && (firstRead
        ? <Empty title="Reading the music folder" icon="quote">The first look through every song takes a few minutes; lyrics appear here as it goes.</Empty>
        : <Empty title={dq ? 'Nothing found' : filter === 'missing' ? 'Every song has lyrics' : 'No songs here'} icon="quote">{dq ? <>Try other words — <code>"quoted words"</code> find an exact phrase.</> : null}</Empty>)}
      {items && items.length > 0 && (
        <div className="lyr-list">
          {items.map(it => {
            const f = patched[it.id] || it
            return (
              <div key={f.id} className="lyr-row" role="link" tabIndex={0} onClick={() => nav(`/lyrics/${f.id}`)} onKeyDown={e => { if (e.key === 'Enter') nav(`/lyrics/${f.id}`) }}>
                <button type="button" className="hist-art" disabled={!f.song_id} aria-label={`Play ${f.title}`} title={f.song_id ? 'Play' : "Not in your music server's library"}
                  onClick={e => { e.stopPropagation(); playSongID(f.song_id, f.id).catch(err => toast(errMsg(err), 'err')) }}>
                  <Cover src={f.album_id ? albumCover(f.album_id, 80) : ''} size={38} label={f.album || f.title} />
                </button>
                <div className="grow lyr-main">
                  <div className="ellipsis"><b>{f.title}</b></div>
                  <div className="ellipsis muted small">{f.artist}{f.album ? ` · ${f.album}` : ''}</div>
                  {f.snippet && f.snippet.includes('\x02') && <div className="lyr-snip small"><Snippet s={f.snippet} /></div>}
                </div>
                {f.kind !== 'none' && <span className="muted xsmall tnum hide-mobile">{f.lines} lines</span>}
                <KindBadge f={f} />
                {admin && f.kind === 'none' && status?.can_fetch && (
                  <button type="button" className="btn sm" disabled={!!fetching[f.id]} onClick={e => { e.stopPropagation(); fetchOne(f) }} title="Look the song up at LRCLIB now and save what it finds">
                    {fetching[f.id] ? <Spinner /> : <Icon name="download" size={13} />}Fetch
                  </button>)}
                <Icon name="chevron-right" size={16} className="faint hide-mobile" />
              </div>
            )
          })}
        </div>
      )}
      <div ref={sentinel} className="sentinel">{busy && <Spinner />}</div>
      {bulk && c && status && <FetchMissingModal count={c.missing} q={dq} retryDays={status.nightly?.retry_days || 14} onClose={() => setBulk(false)}
        onStarted={st => { setBulk(false); setMeta(m => m && { ...m, status: st }) }} />}
      {total > 0 && items && items.length >= total && total > 30 && <p className="center muted small">{total.toLocaleString()} songs</p>}
    </div>
  )
}

function StatusLine({ status, admin, onStop }: { status: LyricsIndexStatus; admin: boolean; onStop: () => void }) {
  const ix = status.index
  const run = status.job?.run
  return (
    <div className="lyr-status">
      {ix.running && <span className="row small"><Spinner />{ix.total ? <>Reading the music folder… {ix.done.toLocaleString()} of {ix.total.toLocaleString()} changed songs</> : 'Looking through the music folder…'}</span>}
      {status.job?.running && (
        <span className="row small"><Spinner />
          {run ? <>Fetching lyrics: {run.checked.toLocaleString()} of {run.total.toLocaleString()} songs · {run.fetched} synced{run.plain ? `, ${run.plain} plain` : ''} found</> : 'Fetching lyrics…'}
          {admin && <button type="button" className="btn ghost sm" onClick={onStop}><Icon name="x" size={13} />Stop</button>}
        </span>)}
      {!ix.running && ix.indexed_at && <span className="muted small">Folder read {ago(ix.indexed_at)}</span>}
      {admin && status.nightly && (
        <Link to="/settings/lyrics" className={`chip sm ${status.nightly.enabled ? 'active' : ''}`} title="Change in Settings → Lyrics">
          <Icon name="clock" size={12} />{status.nightly.enabled ? `Nightly fetch at ${pad2(status.nightly.hour)}:00` : 'Nightly fetch is off'}
        </Link>)}
      {admin && status.writable === false && <span className="badge warn" title={status.detail}>Read-only: {status.detail}</span>}
    </div>
  )
}

function FetchMissingModal({ count, q, retryDays, onClose, onStarted }: { count: number; q: string; retryDays: number; onClose: () => void; onStarted: (st: LyricsIndexStatus) => void }) {
  const { toast } = useToast()
  const [again, setAgain] = useState(false)
  const [busy, setBusy] = useState(false)
  const start = async () => {
    setBusy(true)
    try {
      const r = await api.post<{ songs: number; status: LyricsIndexStatus }>(`${V1}/lyrics-index/fetch-missing`, { q, again })
      toast(`Looking up ${r.songs.toLocaleString()} songs at LRCLIB`)
      onStarted(r.status)
    } catch (e) { toast(errMsg(e), 'err'); setBusy(false) }
  }
  const minutes = Math.max(1, Math.round(count * 0.7 / 60))
  return (
    <Modal title="Fetch the missing lyrics" size="sm" onClose={onClose} footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" onClick={start} disabled={busy}>{busy ? <Spinner /> : <Icon name="download" size={14} />}Fetch</button>
    </>}>
      <div className="stack tight">
        <p>Look up {count.toLocaleString()} song{count === 1 ? '' : 's'} without lyrics{q ? <> matching “{q}”</> : ''} at LRCLIB and save what it finds next to them — synced as a .lrc file, plain ones as Settings → Lyrics says.</p>
        <p className="muted small">It runs in the background (about {minutes < 90 ? `${minutes} min` : `${Math.round(minutes / 60)} h`} at most) — you can leave this page or stop it. Songs whose lyrics you deleted on purpose or edited by hand are left alone.</p>
        <label className="row"><Switch checked={again} onChange={setAgain} label="Ask again" /> <span>Also ask again for songs LRCLIB didn't know in the last {retryDays} days</span></label>
      </div>
    </Modal>
  )
}

// ---------- one song: the editor ----------

type Tap = { lines: string[]; times: (number | null)[]; i: number }

export function LyricsEditor() {
  const { id = '' } = useParams()
  const { toast } = useToast()
  const nav = useNavigate()
  const [d, setD] = useState<LyricsFileDetail | null>(null)
  const [err, setErr] = useState('')
  const [text, setText] = useState('')
  const [base, setBase] = useState('')
  const [busy, setBusy] = useState<'' | 'save' | 'fetch' | 'delete'>('')
  const [modal, setModal] = useState<'' | 'matches' | 'delete'>('')
  const [tap, setTap] = useState<Tap | null>(null)
  const p = usePlayer()
  useCrumb(d?.file.title)
  const take = (r: LyricsFileDetail) => { setD(r); setText(r.text); setBase(r.text) }
  useEffect(() => { setD(null); setErr(''); setTap(null); api.get<LyricsFileDetail>(`${V1}/lyrics-files/${id}`).then(take).catch(e => setErr(errMsg(e))) }, [id])
  const dirty = !!d && text !== base
  useEffect(() => {
    if (!dirty) return
    const h = (e: BeforeUnloadEvent) => { e.preventDefault() }
    window.addEventListener('beforeunload', h)
    return () => window.removeEventListener('beforeunload', h)
  }, [dirty])

  const parsed = useMemo(() => parseLrc(text), [text])
  const timed = HAS_STAMP.test(text)
  const untimed = timed ? text.split('\n').filter(l => l.trim() && !HAS_STAMP.test(l) && !META.test(l.trim())).length : 0
  const song = d?.song || null
  const isThis = !!song && p.current?.id === song.id

  if (err) return <Empty title="Can't open these lyrics" icon="quote">{err} <Link to="/lyrics">Back to all lyrics</Link></Empty>
  if (!d) return <Loading />
  const f = d.file
  const where = timed ? 'as a .lrc file beside the song'
    : d.flac && d.plain_to !== 'lrc' ? "in the song's FLAC tag (UNSYNCEDLYRICS)" : 'as a .lrc file without timings'

  const save = async () => {
    setBusy('save')
    try { take(await api.put<LyricsFileDetail>(`${V1}/lyrics-files/${f.id}`, { text })); toast('Saved — Now Playing shows it at once, other apps after the music server’s scan') }
    catch (e) { toast(errMsg(e), 'err') } finally { setBusy('') }
  }
  const fetchNow = async () => {
    setBusy('fetch')
    try { take(await api.post<LyricsFileDetail>(`${V1}/lyrics-files/${f.id}/fetch`)); toast('Lyrics saved from LRCLIB') }
    catch (e) { toast(errMsg(e), 'err') } finally { setBusy('') }
  }
  const play = () => { if (song) player.playSongs([song], 0, sourceFor(f.id)) }
  const startTap = () => {
    const lines = parseLrc(text).lines.map(l => l.text).filter(Boolean)
    if (!lines.length) { toast('Type or paste the words first, then tap along', 'err'); return }
    setTap({ lines, times: lines.map(() => null), i: 0 })
  }
  const finishTap = (t: Tap) => {
    setText(t.lines.map((l, k) => t.times[k] !== null ? `${fmtStamp(t.times[k]!)} ${l}` : l).join('\n'))
    setTap(null)
  }

  return (
    <div className="lyr-edit">
      <div className="page-header">
        <div className="row top lyr-head">
          <Cover src={f.album_id ? albumCover(f.album_id, 160) : ''} size={72} label={f.album || f.title} />
          <div className="min0">
            <p className="eyebrow"><Link to="/lyrics">Lyrics</Link></p>
            <h1 className="ellipsis">{f.title}</h1>
            <div className="sub ellipsis">{f.artist}{f.album ? <> · {f.album_id ? <Link to={`/albums/${f.album_id}`}>{f.album}</Link> : f.album}</> : null}{f.duration ? ` · ${clock(f.duration)}` : ''}</div>
            <div className="faint xsmall ellipsis lyr-path" title={f.path}>{f.path}</div>
          </div>
        </div>
        <div className="actions">
          <button type="button" className="btn" onClick={play} disabled={!song} title={song ? 'Play this song' : "Not in your music server's library"}><Icon name="play" size={15} />Play</button>
          {d.can_fetch && <button type="button" className="btn" onClick={() => setModal('matches')}><Icon name="search" size={15} />Find online</button>}
          {d.can_fetch && f.kind === 'none' && <button type="button" className="btn primary" onClick={fetchNow} disabled={!!busy}>{busy === 'fetch' ? <Spinner /> : <Icon name="download" size={15} />}Fetch now</button>}
          {d.can_edit && f.kind !== 'none' && <button type="button" className="btn ghost danger-text" onClick={() => setModal('delete')}><Icon name="trash" size={15} />Delete</button>}
        </div>
      </div>
      <div className="lyr-meta row wrap small">
        <KindBadge f={f} />
        {f.kind !== 'none' && <span className="muted">{f.source === 'lrc' ? '.lrc file beside the song' : "In the song's own tags"}{f.changed_at ? ` · changed ${ago(f.changed_at)}` : ''}</span>}
        {f.attempt === 'edited' && <span className="muted">· edited by hand — the nightly fetch leaves it alone</span>}
        {f.attempt === 'skip' && <span className="muted">· kept without lyrics — the nightly fetch leaves it alone</span>}
      </div>
      {!d.can_edit && d.edit_detail && <div className="callout warn"><Icon name="alert" size={15} />{d.edit_detail}</div>}

      <div className={`lyr-grid ${d.can_edit ? '' : 'one'}`}>
        {d.can_edit && (
          <section className="card">
            <div className="card-head">
              <div className="grow"><h2>Lyrics</h2><p className="muted small">Lines like <code>[01:23.45] words</code> are synced; without times they're plain.</p></div>
              <span className={`badge ${timed ? 'ok' : ''}`}>{timed ? 'Synced' : text.trim() ? 'Plain' : 'Empty'}</span>
            </div>
            <div className="card-body stack tight">
              <div className="row wrap lyr-tools">
                {timed && <>
                  <span className="muted small">Shift every line</span>
                  {[-500, -100, 100, 500].map(ms => <button key={ms} type="button" className="btn sm" onClick={() => setText(shiftLrc(text, ms))} title={ms < 0 ? 'Earlier' : 'Later'}>{ms > 0 ? '+' : '−'}{Math.abs(ms) / 1000}s</button>)}
                  <button type="button" className="btn sm ghost" onClick={() => setText(stripTimes(text))}>Remove timings</button>
                </>}
                <button type="button" className="btn sm" onClick={startTap} disabled={!!tap}><Icon name="zap" size={13} />Tap to sync</button>
              </div>
              <textarea className="lyr-text" value={text} onChange={e => setText(e.target.value)} spellCheck={false} aria-label="Lyrics"
                placeholder={'Paste or type the lyrics.\n[00:12.30] A synced line\nOr just the words, one line each.'} />
              {untimed > 0 && <p className="small warn-text"><Icon name="alert" size={13} /> {untimed} line{untimed === 1 ? ' has' : 's have'} no time — synced players skip {untimed === 1 ? 'it' : 'them'}.</p>}
              <div className="row wrap">
                <button type="button" className="btn primary" onClick={save} disabled={!dirty || !text.trim() || !!busy}>{busy === 'save' ? <Spinner /> : <Icon name="check" size={15} />}Save</button>
                <button type="button" className="btn" onClick={() => setText(base)} disabled={!dirty}>Revert</button>
                <span className="muted small">{text.trim() ? `Saved ${where}.` : ''}</span>
              </div>
            </div>
          </section>
        )}
        <section className="card lyr-side">
          {tap
            ? <TapPanel tap={tap} setTap={setTap} isThis={isThis} onPlay={play} canPlay={!!song} onDone={finishTap} onCancel={() => setTap(null)} />
            : <Preview lines={parsed.lines} synced={parsed.synced} isThis={isThis} onPlay={play} canPlay={!!song} />}
        </section>
      </div>

      {modal === 'matches' && <MatchesModal fileId={f.id} artist={f.artist} title={f.title} duration={f.duration || song?.duration || 0} onClose={() => setModal('')}
        onUse={m => { setText(m.text); setModal(''); setTap(null); toast('Loaded into the editor — check it, then Save') }} />}
      {modal === 'delete' && <DeleteModal f={f} onClose={() => setModal('')} onDeleted={r => { take(r); setModal(''); toast('Lyrics deleted') }} busy={busy === 'delete'} setBusy={b => setBusy(b ? 'delete' : '')} />}
      {!d.can_edit && <p className="muted small mt">Only an admin can change lyrics — they live in the shared music folder. <button type="button" className="link-btn" onClick={() => nav('/lyrics')}>All lyrics</button></p>}
    </div>
  )
}

function Preview({ lines, synced, isThis, onPlay, canPlay }: { lines: LrcLine[]; synced: boolean; isThis: boolean; onPlay: () => void; canPlay: boolean }) {
  const { position } = useProgress(isThis && synced)
  const box = useRef<HTMLDivElement>(null)
  const ms = position * 1000
  const active = useMemo(() => {
    if (!isThis || !synced) return -1
    let i = -1
    for (let k = 0; k < lines.length; k++) { if ((lines[k].t ?? 0) <= ms + 250) i = k; else break }
    return i
  }, [lines, ms, isThis, synced])
  useEffect(() => {
    const el = box.current?.querySelector<HTMLElement>(`[data-l="${active}"]`)
    if (el && box.current) box.current.scrollTo({ top: el.offsetTop - box.current.clientHeight / 2 + el.clientHeight / 2, behavior: 'smooth' })
  }, [active])
  return (
    <>
      <div className="card-head">
        <div className="grow"><h2>Preview</h2><p className="muted small">{synced ? (isThis ? 'Follows the song — click a line to jump there.' : 'Play the song to see the timings follow it.') : 'Plain lyrics show as they are.'}</p></div>
        {!isThis && canPlay && <button type="button" className="btn sm" onClick={onPlay}><Icon name="play" size={13} />Play</button>}
      </div>
      <div className="lyr-preview" ref={box}>
        {lines.length === 0 && <p className="muted">No lyrics yet.</p>}
        {lines.map((l, i) => (
          <p key={i} data-l={i} className={`ly ${i === active ? 'on' : ''} ${active >= 0 && i < active ? 'past' : ''} ${l.text ? '' : 'gap'}`}
            onClick={() => { if (isThis && synced && l.t !== null) player.seek(l.t / 1000) }}>
            {synced && l.t !== null && <span className="ly-t tnum">{fmtStamp(l.t).slice(1, -1)}</span>}{l.text || '♪'}
          </p>
        ))}
      </div>
    </>
  )
}

function TapPanel({ tap, setTap, isThis, onPlay, canPlay, onDone, onCancel }: { tap: Tap; setTap: (t: Tap) => void; isThis: boolean; onPlay: () => void; canPlay: boolean; onDone: (t: Tap) => void; onCancel: () => void }) {
  const box = useRef<HTMLDivElement>(null)
  const cur = useRef(tap)
  cur.current = tap
  const stamp = () => {
    const t = cur.current
    if (!isThis || t.i >= t.lines.length) return
    const times = t.times.slice()
    times[t.i] = Math.round(player.position * 1000)
    const next = { ...t, times, i: t.i + 1 }
    if (next.i >= next.lines.length) onDone(next)
    else setTap(next)
  }
  const back = () => {
    const t = cur.current
    if (t.i === 0) return
    const times = t.times.slice()
    times[t.i - 1] = null
    setTap({ ...t, times, i: t.i - 1 })
    if (isThis && times[t.i - 2] != null) player.seek(Math.max(0, times[t.i - 2]! / 1000 - 1))
  }
  // Space / Enter stamp, Backspace goes back — before the player's own keys see them
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement
      if (el.closest('input, textarea, select, [contenteditable="true"]') || document.querySelector('.modal-backdrop')) return
      if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); e.stopImmediatePropagation(); stamp() }
      else if (e.key === 'Backspace') { e.preventDefault(); e.stopImmediatePropagation(); back() }
    }
    window.addEventListener('keydown', h, true)
    return () => window.removeEventListener('keydown', h, true)
  })
  useEffect(() => {
    const el = box.current?.querySelector<HTMLElement>(`[data-l="${tap.i}"]`)
    if (el && box.current) box.current.scrollTo({ top: el.offsetTop - box.current.clientHeight / 2 + el.clientHeight / 2, behavior: 'smooth' })
  }, [tap.i])
  const playFromStart = () => { onPlay() }
  return (
    <>
      <div className="card-head">
        <div className="grow"><h2>Tap to sync</h2><p className="muted small">Play the song and press <Kbd>Space</Kbd> as each line starts. <Kbd>⌫</Kbd> goes back a line. Tapped a little late? Shift every line afterwards.</p></div>
      </div>
      <div className="card-body stack tight">
        {!isThis && <div className="callout info"><Icon name="info" size={15} /><span className="grow">The song has to play here while you tap.</span>{canPlay && <button type="button" className="btn primary sm" onClick={playFromStart}><Icon name="play" size={13} />Play from the start</button>}</div>}
        <div className="tap-list" ref={box}>
          {tap.lines.map((l, k) => (
            <div key={k} data-l={k} className={`tap-line ${k === tap.i ? 'next' : ''} ${tap.times[k] !== null ? 'done' : ''}`}>
              <span className="tnum tap-t">{tap.times[k] !== null ? fmtStamp(tap.times[k]!).slice(1, -1) : '··:··.··'}</span><span>{l}</span>
            </div>
          ))}
        </div>
        <div className="row wrap">
          <button type="button" className="btn primary lg tap-btn" onClick={stamp} disabled={!isThis || tap.i >= tap.lines.length}><Icon name="zap" size={16} />Stamp line {Math.min(tap.i + 1, tap.lines.length)} of {tap.lines.length}</button>
          <button type="button" className="btn" onClick={back} disabled={tap.i === 0}><Icon name="undo" size={14} />Back</button>
          <button type="button" className="btn ghost" onClick={() => onDone(tap)} disabled={tap.i === 0} title="Use the times tapped so far">Done</button>
          <button type="button" className="btn ghost" onClick={onCancel}>Cancel</button>
        </div>
      </div>
    </>
  )
}

function MatchesModal({ fileId, artist, title, duration, onUse, onClose }: { fileId: number; artist: string; title: string; duration: number; onUse: (m: LrclibMatch) => void; onClose: () => void }) {
  const [a, setA] = useState(artist)
  const [t, setT] = useState(title)
  const [items, setItems] = useState<LrclibMatch[] | null>(null)
  const [err, setErr] = useState('')
  const [open, setOpen] = useState<number | null>(null)
  const search = () => {
    setItems(null); setErr('')
    api.get<{ items: LrclibMatch[] }>(`${V1}/lyrics-files/${fileId}/candidates?artist=${encodeURIComponent(a)}&title=${encodeURIComponent(t)}`)
      .then(r => setItems(r.items)).catch(e => { setErr(errMsg(e)); setItems([]) })
  }
  useEffect(search, []) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <Modal title="Lyrics at LRCLIB" size="lg" onClose={onClose}>
      <form className="row wrap match-form" onSubmit={e => { e.preventDefault(); search() }}>
        <input value={a} onChange={e => setA(e.target.value)} placeholder="Artist" aria-label="Artist" />
        <input value={t} onChange={e => setT(e.target.value)} placeholder="Title" aria-label="Title" />
        <button type="submit" className="btn" disabled={!t.trim()}><Icon name="search" size={14} />Search</button>
      </form>
      {err && <div className="callout danger mt"><Icon name="alert" size={15} />{err}</div>}
      {!items && <Loading label="Asking LRCLIB…" />}
      {items && !items.length && !err && <Empty title="No matches" icon="quote">Try the title without “(Remastered)” or “feat.”, or another spelling of the artist.</Empty>}
      {items && items.length > 0 && <p className="muted small mt">{items.length} match{items.length === 1 ? '' : 'es'} — synced ones of the right length first{duration ? ` (this song: ${clock(duration)})` : ''}.</p>}
      <div className="match-list">
        {items?.map(m => {
          const diff = duration && m.duration ? Math.round(m.duration - duration) : null
          return (
            <div key={m.id} className="match-row">
              <div className="row">
                <div className="grow min0">
                  <div className="ellipsis"><b>{m.title}</b> <span className="muted">— {m.artist}</span></div>
                  <div className="muted small ellipsis">{m.album || 'No album'} · {clock(Math.round(m.duration))}{diff !== null && <> · <span className={Math.abs(diff) <= 3 ? 'ok-text' : 'warn-text'}>{diff === 0 ? 'same length' : `${diff > 0 ? '+' : '−'}${Math.abs(diff)} s`}</span></>}</div>
                </div>
                {m.instrumental ? <span className="badge">Instrumental</span> : <span className={`badge ${m.synced ? 'ok' : ''}`}>{m.synced ? 'Synced' : 'Plain'}</span>}
                {!!m.text && <button type="button" className="btn ghost sm" onClick={() => setOpen(open === m.id ? null : m.id)}>{open === m.id ? 'Hide' : 'Preview'}</button>}
                <button type="button" className="btn primary sm" disabled={!m.text} onClick={() => onUse(m)}>Use</button>
              </div>
              {open === m.id && <pre className="match-pre">{m.text}</pre>}
            </div>
          )
        })}
      </div>
    </Modal>
  )
}

function DeleteModal({ f, onClose, onDeleted, busy, setBusy }: { f: LyricsFile; onClose: () => void; onDeleted: (d: LyricsFileDetail) => void; busy: boolean; setBusy: (b: boolean) => void }) {
  const { toast } = useToast()
  const [keep, setKeep] = useState(true)
  const del = async () => {
    setBusy(true)
    try { onDeleted(await api.del<LyricsFileDetail>(`${V1}/lyrics-files/${f.id}${keep ? '?keep=1' : ''}`)) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Delete these lyrics?" size="sm" onClose={onClose} footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn danger" onClick={del} disabled={busy}>{busy ? <Spinner /> : <Icon name="trash" size={14} />}Delete</button>
    </>}>
      <div className="stack tight">
        <p>Removes {f.source === 'lrc' ? 'the .lrc file beside' : 'the lyrics stored in'} “{f.title}”{f.source === 'lrc' ? '' : "'s tags"}. Apps stop showing them after the music server's next scan.</p>
        <label className="row"><Switch checked={keep} onChange={setKeep} label="Keep without lyrics" /> <span>Don't look them up again</span></label>
        <p className="muted small">{keep ? 'The nightly fetch and “Fetch missing” leave this song alone.' : 'The nightly fetch looks the song up again — LRCLIB may well have the same lyrics.'}</p>
      </div>
    </Modal>
  )
}
