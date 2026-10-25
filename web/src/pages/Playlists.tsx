// Playlists (kept on the music server) and smart playlists (rules, refreshed daily).
import React, { useEffect, useState } from 'react'
import { OfflineButton } from '../components/OfflineButton'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, coverURL, longDuration } from '../lib/music'
import { player } from '../lib/player-react'
import type { Playlist as PlaylistT, Rule, SmartPlaylist, Song } from '../lib/types'
import { fmtDateTime } from '../lib/format'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { Collage, Cover, HeroActions, MenuTrigger, PlaylistCard, SongTable, useCoverColor } from '../components/music'
import { announceChange, openCreate, useCrumb } from '../components/Layout'
import { Confirm, Empty, Field, Loading, Modal, Segmented, Spinner, errMsg, useAsync, useDebounce, useToast } from '../components/ui'
import { touches, useLiveRefresh } from '../lib/live'

export function Playlists() {
  const { data, error, reload } = useAsync(() => api.get<PlaylistT[]>(`${V1}/playlists`), [])
  useLiveRefresh(ev => ev.type === 'playlists', reload)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const smart = data.filter(p => p.smart_id)
  const mine = data.filter(p => p.mine && !p.smart_id)
  const shared = data.filter(p => !p.mine)
  return (
    <div>
      <div className="page-header">
        <div><h1>Playlists</h1><div className="sub">Saved on your music server: every Subsonic player you use sees them.</div></div>
        <div className="actions">
          <button type="button" className="btn" onClick={() => openCreate('smart')}><Icon name="zap" size={15} />New smart playlist</button>
          <button type="button" className="btn primary" onClick={() => openCreate('playlist')}><Icon name="plus" size={15} />New playlist</button>
        </div>
      </div>
      {data.length === 0 && <Empty title="No playlists yet" icon="playlist">Make one from the New button, add songs from any song’s menu, or let a smart playlist fill itself.</Empty>}
      {smart.length > 0 && <section className="section"><div className="section-head"><h2>Smart playlists</h2><span className="muted small">refilled every day from their rules</span></div><div className="grid-tiles">{smart.map(p => <PlaylistCard key={p.id} p={p} />)}</div></section>}
      {mine.length > 0 && <section className="section"><div className="section-head"><h2>Yours</h2></div><div className="grid-tiles">{mine.map(p => <PlaylistCard key={p.id} p={p} />)}</div></section>}
      {shared.length > 0 && <section className="section"><div className="section-head"><h2>Shared with you</h2></div><div className="grid-tiles">{shared.map(p => <PlaylistCard key={p.id} p={p} />)}</div></section>}
    </div>
  )
}

interface PlaylistResp { playlist: PlaylistT; songs: Song[]; smart?: SmartPlaylist }

export function Playlist() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const { data, error, reload } = useAsync(() => api.get<PlaylistResp>(`${V1}/playlists/${id}`), [id])
  // changed in another tab, a phone app or by a smart refresh; stars on its songs too
  useLiveRefresh(ev => (ev.type === 'playlists' && (ev.data.ids.length === 0 || ev.data.ids.includes(id))) || touches(ev, (data?.songs || []).map(s => s.id)), reload)
  const [edit, setEdit] = useState(false)
  const [del, setDel] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const color = useCoverColor(data?.playlist.covers[0] ? coverURL('al-' + data.playlist.covers[0], 64) : '')
  useCrumb(data?.playlist.name)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const { playlist: p, songs, smart } = data
  const source = { kind: 'playlist' as const, name: p.name, link: `/playlists/${p.id}` }
  const remove = async (i: number) => {
    try { await api.put(`${V1}/playlists/${p.id}`, { remove: [i] }); toast('Removed'); reload(); announceChange() } catch (e) { toast(errMsg(e), 'err') }
  }
  const refresh = async () => {
    if (!smart) return
    setRefreshing(true)
    try { await api.post(`${V1}/smart/${smart.id}/refresh`); await reload(); announceChange(); toast('Refreshed') } catch (e) { toast(errMsg(e), 'err') } finally { setRefreshing(false) }
  }
  return (
    <div className="detail" style={color ? { ['--art' as string]: color } : undefined}>
      <div className="detail-glow" />
      <header className="detail-hero">
        {p.covers.length >= 4 ? <span className="hero-cover"><Collage albums={p.covers} /></span> : <Cover src={coverURL(p.coverArt || 'pl-' + p.id, 600)} className="hero-cover" label={p.name} fallbackIcon="playlist" />}
        <div className="hero-text">
          <p className="eyebrow">{smart ? <><Icon name="zap" size={12} /> Smart playlist</> : p.public ? 'Public playlist' : 'Playlist'}</p>
          <h1 className="hero-title">{p.name}</h1>
          {p.comment && <p className="hero-desc">{p.comment.replace(/ · Smart playlist from Pocket Music.*$/, '')}</p>}
          <p className="hero-meta">
            {!p.mine && <span>by {p.owner}</span>}
            <span>{songs.length} songs, {longDuration(songs.reduce((t, s) => t + s.duration, 0))}</span>
            {smart && <span>{smart.refresh === 'daily' ? 'refreshed daily' : 'refreshed by hand'}{smart.refreshed_at ? `, last ${fmtDateTime(smart.refreshed_at)}` : ''}</span>}
          </p>
          {smart?.error && <p className="danger-text small">{smart.error}</p>}
          <HeroActions onPlay={() => player.playSongs(songs, 0, source)} onShuffle={() => player.playSongs(songs, -1, source, true)}>
            <OfflineButton songs={songs} group={{ key: `playlist:${p.id}`, name: p.name, kind: 'playlist' }} />
            {smart && <button type="button" className="btn lg" onClick={() => navigate(`/smart/${smart.id}`)}><Icon name="filter" size={16} />Rules</button>}
            {smart && <button type="button" className="btn lg icon-only" onClick={refresh} disabled={refreshing} aria-label="Refresh now" title="Refresh now">{refreshing ? <Spinner /> : <Icon name="refresh" size={17} />}</button>}
            {p.mine && <MenuTrigger items={() => [
              { label: 'Play next', icon: 'queue', onSelect: () => player.playNext(songs) },
              { label: 'Add to queue', icon: 'list-plus', onSelect: () => player.addToQueue(songs) },
              'sep',
              ...(!smart ? [{ label: 'Rename / describe', icon: 'edit', onSelect: () => setEdit(true) }] : []),
              { label: 'Delete playlist', icon: 'trash', danger: true, onSelect: () => setDel(true) },
            ]} />}
          </HeroActions>
        </div>
      </header>
      {songs.length === 0 ? <Empty title={smart ? 'Nothing matches the rules right now' : 'This playlist is empty'} icon="playlist">{smart ? 'Change the rules, or wait: it refills every day.' : 'Add songs from any song’s ••• menu → Add to playlist.'}</Empty>
        : <SongTable songs={songs} source={source} showCover showPlays onRemove={p.mine && !smart ? remove : undefined} />}
      {edit && <EditPlaylist p={p} onClose={() => setEdit(false)} onSaved={() => { setEdit(false); reload(); announceChange() }} />}
      {del && <Confirm title={`Delete “${p.name}”?`} message={smart ? 'The rules and the playlist on your music server are deleted. The songs stay in your library.' : 'The playlist is deleted on your music server for every player. The songs stay in your library.'} confirmLabel="Delete"
        onCancel={() => setDel(false)} onConfirm={async () => {
          try { if (smart) await api.del(`${V1}/smart/${smart.id}`); else await api.del(`${V1}/playlists/${p.id}`); announceChange(); navigate('/playlists') } catch (e) { toast(errMsg(e), 'err') }
        }} />}
    </div>
  )
}

function EditPlaylist({ p, onClose, onSaved }: { p: PlaylistT; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [name, setName] = useState(p.name)
  const [comment, setComment] = useState(p.comment)
  const [pub, setPub] = useState(p.public)
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    try { await api.put(`${V1}/playlists/${p.id}`, { name, comment, public: pub }); onSaved() } catch (err) { toast(errMsg(err), 'err') }
  }
  return (
    <Modal title="Edit playlist" onClose={onClose} size="sm">
      <form onSubmit={save} className="stack">
        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} required autoFocus /></Field>
        <Field label="Description"><input value={comment} onChange={e => setComment(e.target.value)} /></Field>
        <Field group label="Who sees it" help="Public playlists show up for the other people on your music server."><Segmented value={pub ? 'pub' : 'priv'} onChange={v => setPub(v === 'pub')} options={[{ value: 'priv', label: 'Only me' }, { value: 'pub', label: 'Everyone on the server' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

// ---------- the smart playlist editor ----------

const FIELDS: { value: string; label: string; kind: 'text' | 'number' | 'days' | 'bool'; unit?: string }[] = [
  { value: 'genre', label: 'Genre', kind: 'text' }, { value: 'artist', label: 'Artist', kind: 'text' }, { value: 'album_artist', label: 'Album artist', kind: 'text' },
  { value: 'album', label: 'Album', kind: 'text' }, { value: 'title', label: 'Title', kind: 'text' }, { value: 'year', label: 'Year', kind: 'number' },
  { value: 'plays', label: 'Your plays', kind: 'number' }, { value: 'last_played', label: 'Last played', kind: 'days' }, { value: 'added', label: 'Added', kind: 'days' },
  { value: 'starred', label: 'Loved', kind: 'bool' }, { value: 'never_played', label: 'Never played', kind: 'bool' }, { value: 'rating', label: 'Rating', kind: 'number', unit: '★' },
  { value: 'duration', label: 'Length', kind: 'number', unit: 'seconds' }, { value: 'lossless', label: 'Lossless', kind: 'bool' }, { value: 'format', label: 'File format', kind: 'text' },
  { value: 'bitrate', label: 'Bitrate', kind: 'number', unit: 'kbps' }, { value: 'bpm', label: 'BPM', kind: 'number' }, { value: 'path', label: 'Folder / path', kind: 'text' },
]
const OPS: Record<string, { value: string; label: string }[]> = {
  text: [{ value: 'is', label: 'is' }, { value: 'is_not', label: 'is not' }, { value: 'contains', label: 'contains' }, { value: 'not_contains', label: 'does not contain' }, { value: 'starts_with', label: 'starts with' }],
  number: [{ value: 'is', label: 'is' }, { value: 'is_not', label: 'is not' }, { value: 'gt', label: 'more than' }, { value: 'lt', label: 'less than' }, { value: 'between', label: 'between' }],
  days: [{ value: 'within', label: 'in the last' }, { value: 'not_within', label: 'not in the last' }, { value: 'between', label: 'between' }],
  bool: [{ value: 'is', label: 'is' }],
}
const SORTS = [['random', 'Random (reshuffled daily)'], ['added', 'Recently added'], ['added_asc', 'Oldest added'], ['plays', 'Most played'], ['least_played', 'Least played'], ['last_played', 'Recently played'], ['year', 'Newest'], ['year_asc', 'Oldest'], ['artist', 'Artist, album, track'], ['album', 'Album, track'], ['rating', 'Rating'], ['title', 'Title']]
const TEMPLATES: { name: string; description: string; rules: Rule[]; sort: string; limit: number }[] = [
  { name: 'Not played in a year', description: 'Songs you played before, but not in the last 12 months', rules: [{ field: 'plays', op: 'gt', value: '0' }, { field: 'last_played', op: 'not_within', value: '365' }], sort: 'random', limit: 100 },
  { name: 'Fresh & unplayed', description: 'Added in the last 30 days, never played', rules: [{ field: 'added', op: 'within', value: '30' }, { field: 'never_played', op: 'is', value: 'true' }], sort: 'added', limit: 100 },
  { name: 'Heavy rotation', description: 'Played at least 10 times', rules: [{ field: 'plays', op: 'gt', value: '9' }], sort: 'plays', limit: 100 },
  { name: 'Hi-res', description: 'Lossless files at more than 1,000 kbps', rules: [{ field: 'lossless', op: 'is', value: 'true' }, { field: 'bitrate', op: 'gt', value: '1000' }], sort: 'random', limit: 200 },
  { name: 'The 80s', description: 'Everything from 1980–1989', rules: [{ field: 'year', op: 'between', value: '1980', value2: '1989' }], sort: 'random', limit: 150 },
  { name: 'Loved, not lately', description: 'Loved songs you haven’t heard in two months', rules: [{ field: 'starred', op: 'is', value: 'true' }, { field: 'last_played', op: 'not_within', value: '60' }], sort: 'random', limit: 80 },
]

export function SmartEditor() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const isNew = !id || id === 'new'
  const [p, setP] = useState<Omit<SmartPlaylist, 'id' | 'playlist_id' | 'song_count' | 'duration' | 'refreshed_at' | 'error' | 'created_at' | 'updated_at'> & { id?: number } | null>(isNew ? { name: '', description: '', rules: { match: 'all', conditions: [{ field: 'genre', op: 'is', value: '' }] }, sort: 'random', limit_count: 100, limit_minutes: 0, refresh: 'daily' } : null)
  const [preview, setPreview] = useState<{ count: number; duration: number; songs: Song[] } | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [genres, setGenres] = useState<{ name: string }[]>([])
  useCrumb(isNew ? 'New smart playlist' : p?.name)
  useEffect(() => { api.get<{ name: string }[]>(`${V1}/genres`).then(setGenres).catch(() => {}) }, [])
  useEffect(() => { if (!isNew) api.get<SmartPlaylist>(`${V1}/smart/${id}`).then(setP).catch(e => toast(errMsg(e), 'err')) }, [id, isNew, toast])
  const dp = useDebounce(JSON.stringify(p), 400)
  useEffect(() => {
    if (!p) return
    const complete = p.rules.conditions.every(c => c.value !== '' || FIELDS.find(f => f.value === c.field)?.kind === 'bool')
    if (!complete) return
    setPreviewing(true)
    api.post<{ count: number; duration: number; songs: Song[] }>(`${V1}/smart/preview`, { ...p, name: p.name || 'preview' }).then(setPreview).catch(() => setPreview(null)).finally(() => setPreviewing(false))
  }, [dp]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!p) return <Loading />
  const setRule = (i: number, r: Partial<Rule>) => setP({ ...p, rules: { ...p.rules, conditions: p.rules.conditions.map((c, k) => k === i ? { ...c, ...r } : c) } })
  const save = async () => {
    if (!p.name.trim()) { toast('Give it a name', 'err'); return }
    setSaving(true)
    try {
      const saved = isNew ? await api.post<SmartPlaylist>(`${V1}/smart`, p) : await api.put<SmartPlaylist>(`${V1}/smart/${id}`, p)
      announceChange()
      toast(`“${saved.name}”: ${saved.song_count} songs`)
      navigate(saved.playlist_id ? `/playlists/${saved.playlist_id}` : '/playlists')
    } catch (e) { toast(errMsg(e), 'err') } finally { setSaving(false) }
  }
  return (
    <div className="smart-editor">
      <div className="page-header">
        <div><h1>{isNew ? 'New smart playlist' : `Rules of “${p.name}”`}</h1><div className="sub">Songs that match fill a real playlist on your music server, refreshed every day — so your phone app and car stereo see it too.</div></div>
      </div>
      {isNew && (
        <div className="chips mb">
          <span className="muted small">Start from:</span>
          {TEMPLATES.map(t => <button key={t.name} type="button" className="chip" onClick={() => setP({ ...p, name: t.name, description: t.description, rules: { match: 'all', conditions: t.rules }, sort: t.sort, limit_count: t.limit })}>{t.name}</button>)}
        </div>
      )}
      <div className="smart-grid">
        <div className="stack">
          <div className="two-col">
            <Field label="Name"><input value={p.name} onChange={e => setP({ ...p, name: e.target.value })} placeholder="Not played in a year" /></Field>
            <Field label="Description"><input value={p.description} onChange={e => setP({ ...p, description: e.target.value })} placeholder="Optional" /></Field>
          </div>
          <div className="card rules-card">
            <div className="rules-head">
              Songs matching
              <Segmented size="sm" value={p.rules.match} onChange={v => setP({ ...p, rules: { ...p.rules, match: v } })} options={[{ value: 'all', label: 'all' }, { value: 'any', label: 'any' }]} />
              of these rules
            </div>
            {p.rules.conditions.map((c, i) => {
              const f = FIELDS.find(x => x.value === c.field) || FIELDS[0]
              return (
                <div key={i} className="rule-row">
                  <Select ariaLabel="Field" className="inline" value={c.field} onChange={v => { const nf = FIELDS.find(x => x.value === v)!; setRule(i, { field: v, op: OPS[nf.kind][0].value, value: nf.kind === 'bool' ? 'true' : '', value2: '' }) }} options={FIELDS.map(x => ({ value: x.value, label: x.label }))} />
                  {f.kind !== 'bool' && <Select ariaLabel="Condition" className="inline" value={c.op} onChange={v => setRule(i, { op: v })} options={OPS[f.kind]} />}
                  {f.kind === 'bool' ? <Segmented size="sm" value={c.value === 'false' ? 'false' : 'true'} onChange={v => setRule(i, { value: v })} options={[{ value: 'true', label: 'yes' }, { value: 'false', label: 'no' }]} />
                    : c.field === 'genre' && (c.op === 'is' || c.op === 'is_not') ? <Select ariaLabel="Genre" className="inline grow" value={c.value} onChange={v => setRule(i, { value: v })} options={genres.map(g => ({ value: g.name, label: g.name }))} placeholder="Pick a genre" />
                      : <>
                        <input className="rule-val" value={c.value} onChange={e => setRule(i, { value: e.target.value })} inputMode={f.kind === 'text' ? undefined : 'numeric'} placeholder={f.kind === 'days' ? 'days' : f.unit || ''} />
                        {c.op === 'between' && <><span className="muted small">and</span><input className="rule-val" value={c.value2 || ''} onChange={e => setRule(i, { value2: e.target.value })} inputMode="numeric" /></>}
                        {f.kind === 'days' && <span className="muted small">days</span>}
                      </>}
                  <button type="button" className="btn ghost icon-only sm" aria-label="Remove rule" onClick={() => setP({ ...p, rules: { ...p.rules, conditions: p.rules.conditions.filter((_, k) => k !== i) } })}><Icon name="x" size={14} /></button>
                </div>
              )
            })}
            <button type="button" className="btn sm ghost" onClick={() => setP({ ...p, rules: { ...p.rules, conditions: [...p.rules.conditions, { field: 'genre', op: 'is', value: '' }] } })}><Icon name="plus" size={14} />Add a rule</button>
          </div>
          <div className="two-col">
            <Field label="Order"><Select ariaLabel="Order" value={p.sort} onChange={v => setP({ ...p, sort: v })} options={SORTS.map(([v, l]) => ({ value: v, label: l }))} /></Field>
            <Field label="At most"><span className="row"><input type="number" min={0} max={5000} value={p.limit_count} onChange={e => setP({ ...p, limit_count: Number(e.target.value) })} /><span className="muted small">songs</span><input type="number" min={0} max={6000} value={p.limit_minutes || ''} onChange={e => setP({ ...p, limit_minutes: Number(e.target.value) })} placeholder="—" /><span className="muted small">min</span></span></Field>
          </div>
          <Field group label="Refresh"><Segmented value={p.refresh} onChange={v => setP({ ...p, refresh: v })} options={[{ value: 'daily', label: 'Every day' }, { value: 'manual', label: 'Only when I ask' }]} /></Field>
          <div className="row"><button type="button" className="btn primary" onClick={save} disabled={saving}>{saving ? <Spinner /> : <Icon name="check" size={15} />}{isNew ? 'Create' : 'Save'}</button><Link to="/playlists" className="btn ghost">Cancel</Link></div>
        </div>
        <aside className="smart-preview card">
          <div className="row"><h3 className="grow">Preview</h3>{previewing && <Spinner />}</div>
          {preview ? <>
            <p className="muted small">{preview.count.toLocaleString()} songs · {longDuration(preview.duration)}</p>
            <div className="preview-list">{(preview.songs || []).slice(0, 30).map(s => <div key={s.id} className="mix-song"><Cover src={coverURL('al-' + s.album_id, 64)} size={30} label={s.album} /><span className="grow ellipsis"><b>{s.title}</b><span className="muted"> · {s.artist}</span></span></div>)}</div>
          </> : <p className="muted small">Fill in the rules to see what matches.</p>}
        </aside>
      </div>
    </div>
  )
}
