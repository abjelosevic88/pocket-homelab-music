import { AskButton } from '../components/AskButton'
import React, { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { deviceName } from '../lib/music'
import { onLive } from '../lib/live'
import type { APIToken, Connection, ImportRun, LyricsLibrary, Settings as SettingsT, SubsonicLogin, User } from '../lib/types'
import { DATE_FORMATS, TIME_FORMATS, fmtBytes, fmtDateTime, type DateFormat, type TimeFormat } from '../lib/format'
import { Avatar, Confirm, CopyField, CoreHint, Field, Loading, Modal, Segmented, SettingRow, Spinner, Switch, Tabs, errMsg, useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { ShortcutsSheet } from '../components/ShortcutsSheet'
import { PocketModelSelect } from '../components/PocketModelSelect'
import { AIStatusLine, useAIStatus } from '../components/forms'
import { PlayerBar } from '../components/PlayerBar'

type TabId = 'library' | 'playback' | 'lyrics' | 'apps' | 'mixes' | 'lidarr' | 'history' | 'notifications' | 'profile' | 'people' | 'tokens' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string }[] = [
  { id: 'library', label: 'Library', icon: 'server', sub: 'Your music server account — Navidrome or any Subsonic server — and the library index.' },
  { id: 'playback', label: 'Playback', icon: 'headphones', sub: 'Streaming quality, volume levelling, crossfade and scrobbling.' },
  { id: 'lyrics', label: 'Lyrics', icon: 'mic', sub: 'How lyrics show while you listen, and the lyrics library that saves them next to your songs.' },
  { id: 'apps', label: 'Phone & car apps', icon: 'smartphone', sub: 'Sign in to Amperfy, Symfonium, play:Sub and other Subsonic apps with your Pocket Music account.' },
  { id: 'mixes', label: 'Mixes & AI', icon: 'sparkles', sub: 'What goes into daily mixes and radio, and which AI turns a sentence into a mix.' },
  { id: 'lidarr', label: 'Lidarr', icon: 'download', sub: 'Missing albums on artist pages, new releases and album requests.' },
  { id: 'history', label: 'History', icon: 'history', sub: 'Bring in what you listened to before: Maloja or ListenBrainz.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'A push when a requested album arrives, and a weekly recap.' },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email and password.' },
  { id: 'people', label: 'People', icon: 'users', sub: 'Everyone who has an account on this box.' },
  { id: 'tokens', label: 'API & shortcuts', icon: 'key', sub: 'Personal tokens for Homepage widgets, Siri Shortcuts and Raycast.' },
  { id: 'data', label: 'Backup & data', icon: 'database', sub: 'Download the database, export your plays or import them.' },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, numbers and the server.' },
]

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const tab: TabId = TABS.find(t => t.id === raw)?.id || 'library'
  const current = TABS.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .sel-pop, .np')) navigate('/') }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])
  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back to your music" title="Back to your music (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={TABS.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {TABS.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title"><h2>{current.label}</h2><p>{current.sub}</p></div>
            {tab === 'library' && <Library />}
            {tab === 'playback' && <Playback />}
            {tab === 'lyrics' && <LyricsSettings />}
            {tab === 'apps' && <PhoneApps />}
            {tab === 'mixes' && <Mixes />}
            {tab === 'lidarr' && <Lidarr />}
            {tab === 'history' && <HistoryImport />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'profile' && <Profile />}
            {tab === 'people' && <Users />}
            {tab === 'tokens' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
      <PlayerBar />
    </div>
  )
}

function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico"><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<SettingsT | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async (patch?: Partial<SettingsT>) => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...patch }); setSettings(s); setF(s); toast('Saved') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return { f, setF, save, busy, isAdmin: user?.role === 'admin' }
}

function useTester() {
  const [test, setTest] = useState<Record<string, string>>({})
  const run = async (which: string, url: string, key: string, extra: Record<string, string> = {}) => {
    setTest(t => ({ ...t, [which]: 'testing…' }))
    try { const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/integrations/test`, { which, url, key, ...extra }); setTest(t => ({ ...t, [which]: r.ok ? `✓ ${r.message}` : `✕ ${r.error}` })) } catch (e) { setTest(t => ({ ...t, [which]: `✕ ${errMsg(e)}` })) }
  }
  const status = (which: string) => <span className={`small ${test[which]?.startsWith('✓') ? 'ok-text' : test[which]?.startsWith('✕') ? 'danger-text' : 'muted'}`}>{test[which]}</span>
  return { run, status }
}

// ---------- library ----------

function Library() {
  const { toast } = useToast()
  const { settings } = useApp()
  const [c, setC] = useState<Connection | null>(null)
  const [mode, setMode] = useState<'core' | 'direct' | 'off'>('direct')
  const [form, setForm] = useState({ url: '', username: '', password: '' })
  const [busy, setBusy] = useState(false)
  const [sync, setSync] = useState<{ state: string; progress?: number } | null>(null)
  const load = () => api.get<Connection>(`${V1}/connection`).then(x => { setC(x); setMode(x.mode); setForm(f => ({ ...f, url: x.custom_url || '', username: x.username || x.account || '' })) }).catch(e => toast(errMsg(e), 'err'))
  useEffect(() => { load() }, []) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => onLive(ev => { if (ev.type === 'sync') { setSync(ev.data); if (ev.data.state !== 'syncing') load() } }), []) // eslint-disable-line react-hooks/exhaustive-deps
  if (!c) return <Loading />
  const coreOn = c.core
  const save = async (m = mode) => {
    setBusy(true)
    try { const x = await api.put<Connection>(`${V1}/connection`, { mode: m, ...form }); setC(x); setMode(x.mode); setForm(f => ({ ...f, password: '' })); toast(x.connected ? 'Connected — reading your library' : x.message || 'Saved', x.connected ? 'ok' : 'err') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const s = c.sync
  return (
    <div className="stack loose">
      <Section title="Your music server" icon="server" actions={<span className={`badge ${c.connected ? 'ok' : 'warn'}`}>{c.connected ? `connected${c.via === 'core' ? ' · Pocket Core' : ''}` : 'not connected'}</span>}
        sub="Pocket Music plays your library with your own account on the server, so playlists, loves, ratings and play counts stay yours — and the Navidrome app or any Subsonic player sees the same playlists.">
        <div className="conn-block">
          <div className="conn-mode">
            <Segmented size="sm" value={mode} onChange={m => { setMode(m); if (m !== 'direct') save(m) }} options={[
              { value: 'core', label: 'Pocket Core', title: coreOn ? 'The Navidrome login Pocket Core holds for you' : 'Needs Pocket Core', disabled: !c.core_configured },
              { value: 'direct', label: 'My account' }, { value: 'off', label: 'Off' }]} />
          </div>
          {!c.core_configured && <CoreHint />}
          <div className={`conn-status ${mode === 'off' ? '' : c.connected ? 'ok' : 'bad'}`}><span className="dot" />
            {c.connected ? <>Signed in as <b>{c.account}</b> on {c.server_type} {c.server_version}{c.via === 'core' ? ' — the login Pocket Core keeps for you (Pocket Core → Settings → My logins)' : ''}.</> : c.message}
          </div>
          {mode === 'direct' && (
            <form className="stack" onSubmit={e => { e.preventDefault(); save('direct') }}>
              {!c.server_url && <Field label="Server address" help="Navidrome, Gonic, Airsonic… e.g. http://navidrome:4533"><input value={form.url} onChange={e => setForm({ ...form, url: e.target.value })} placeholder="https://music.example.com" /></Field>}
              {c.server_url && <p className="muted small">The server address is set on this box ({settings?.navidrome_public_url || 'NAVIDROME_URL'}). <button type="button" className="link-btn" onClick={() => setForm({ ...form, url: form.url || 'https://' })}>Use another server</button></p>}
              {c.server_url && form.url && <Field label="Other server address"><input value={form.url} onChange={e => setForm({ ...form, url: e.target.value })} /></Field>}
              <div className="two-col">
                <Field label="Username"><input value={form.username} onChange={e => setForm({ ...form, username: e.target.value })} autoComplete="username" /></Field>
                <Field label="Password" help={c.connected ? 'Leave empty to keep the saved one.' : 'Stored encrypted on this server.'}><input type="password" value={form.password} onChange={e => setForm({ ...form, password: e.target.value })} autoComplete="current-password" /></Field>
              </div>
              <div className="row"><button className="btn primary" disabled={busy}>{busy ? <Spinner /> : null}{c.connected ? 'Save' : 'Connect'}</button></div>
            </form>
          )}
          {c.connected && c.public_url && <div className="row"><a className="btn sm" href={c.public_url} target="_blank" rel="noreferrer"><Icon name="external" size={13} />Open the server</a></div>}
        </div>
      </Section>
      {c.connected && (
        <Section title="Library index" icon="database" sub="A copy of what your account sees — albums, artists, songs, loves — so browsing, search, mixes and stats are instant. It refreshes by itself after every scan on the server.">
          {sync?.state === 'syncing' || c.syncing ? <div className="row"><Spinner /><span>Reading your library… {sync?.progress ? `${sync.progress}%` : ''}</span></div> : <>
            <div className="set-stats">
              <div className="set-stat"><Icon name="music" size={16} /><span className="set-stat-n">{s.songs.toLocaleString()}</span><span className="set-stat-l">Songs</span></div>
              <div className="set-stat"><Icon name="album" size={16} /><span className="set-stat-n">{s.albums.toLocaleString()}</span><span className="set-stat-l">Albums</span></div>
              <div className="set-stat"><Icon name="mic" size={16} /><span className="set-stat-n">{s.artists.toLocaleString()}</span><span className="set-stat-l">Artists</span></div>
            </div>
            <p className="muted small mt-s">{s.synced_at ? `Indexed ${fmtDateTime(s.synced_at)}; server scan ${s.last_scan ? fmtDateTime(s.last_scan) : '—'}.` : 'Not indexed yet.'}{s.error ? <span className="danger-text"> {s.error}</span> : null}</p>
          </>}
          <div className="row wrap mt-s">
            <button type="button" className="btn sm" onClick={() => api.post(`${V1}/sync`).then(() => toast('Reading your library again'))}><Icon name="refresh" size={14} />Re-read now</button>
            <button type="button" className="btn sm ghost" onClick={() => api.post(`${V1}/sync?scan=1`).then(() => toast('The server is scanning for new files')).catch(e => toast(errMsg(e), 'err'))}>Ask the server to scan for new files</button>
          </div>
        </Section>
      )}
    </div>
  )
}

// ---------- playback ----------

const Q_OPTS = [{ value: 'auto', label: 'Automatic', hint: 'original; 256 kbps for lossless on phones' }, { value: 'original', label: 'Original file' }, { value: '320', label: 'High · 320 kbps' }, { value: '256', label: 'Good · 256 kbps' }, { value: '192', label: 'Normal · 192 kbps' }, { value: '128', label: 'Data saver · 128 kbps' }]

function Playback() {
  const { f, setF, save, busy } = useSettingsForm()
  const [dev, setDev] = useState(() => localStorage.getItem('pmu-device-name') || '')
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Streaming" icon="headphones" sub="Lossless files are big: over the phone network a transcoded stream starts faster and costs less data. The player’s ••• menu changes it for one device.">
        <SettingRow title="Quality" help="Transcoding happens on the music server (ffmpeg)."><Select ariaLabel="Quality" value={f.quality} onChange={v => setF({ ...f, quality: v })} options={Q_OPTS} /></SettingRow>
        <SettingRow title="Format when transcoding" help="MP3 plays everywhere; AAC is smaller at the same quality; Opus is best but old Safari can't play it."><Segmented size="sm" value={f.format} onChange={v => setF({ ...f, format: v })} options={[{ value: 'mp3', label: 'MP3' }, { value: 'aac', label: 'AAC' }, { value: 'opus', label: 'Opus' }]} /></SettingRow>
      </Section>
      <Section title="Sound" icon="volume">
        <SettingRow title="Volume levelling" help="Uses the ReplayGain tags of your files so loud and quiet masters play at the same level. Automatic keeps an album's own dynamics when you play an album. (iPhones ignore volume changes from web pages.)">
          <Select ariaLabel="ReplayGain" value={f.replay_gain} onChange={v => setF({ ...f, replay_gain: v })} options={[{ value: 'auto', label: 'Automatic' }, { value: 'track', label: 'Per song' }, { value: 'album', label: 'Per album' }, { value: 'off', label: 'Off' }]} />
        </SettingRow>
        <SettingRow title="Crossfade" help="0 = gapless: the next song is loaded ahead and starts right on time."><span className="row"><input type="range" min={0} max={12} value={f.crossfade} onChange={e => setF({ ...f, crossfade: Number(e.target.value) })} /><span className="tnum small" style={{ width: 40 }}>{f.crossfade ? `${f.crossfade} s` : 'off'}</span></span></SettingRow>
      </Section>
      <Section title="Plays" icon="history">
        <SettingRow title="A play counts after" help="Of the song, or four minutes — like Last.fm. Counted plays build your history, stats and mixes."><span className="row"><input type="number" min={10} max={100} value={f.scrobble_pct} onChange={e => setF({ ...f, scrobble_pct: Number(e.target.value) })} className="set-time" /><span className="muted small">%</span></span></SettingRow>
        <SettingRow title="Tell the music server" help="Navidrome passes plays on to Last.fm, ListenBrainz or Maloja when you set that up there, and shows “now playing”."><Switch checked={f.scrobble} onChange={v => setF({ ...f, scrobble: v })} label="Scrobble to the music server" /></SettingRow>
      </Section>
      <Section title="This device" icon="devices" sub="How your other devices see this one (“Playing on …”).">
        <SettingRow title="Name"><input value={dev} placeholder={deviceName()} onChange={e => { setDev(e.target.value); if (e.target.value.trim()) localStorage.setItem('pmu-device-name', e.target.value.trim()); else localStorage.removeItem('pmu-device-name') }} /></SettingRow>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

// ---------- lyrics ----------

const HOURS = Array.from({ length: 24 }, (_, h) => ({ value: String(h), label: `${String(h).padStart(2, '0')}:00` }))
const RUN_LABEL: Record<string, string> = { schedule: 'Nightly', manual: 'Run now', folder: 'New album', missing: 'Fetch missing' }

function LyricsSettings() {
  const { f, setF, save, busy, isAdmin } = useSettingsForm()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="While you listen" icon="mic" sub="Now Playing shows the lyrics of the song — synced ones follow the music line by line, and a tap on a line jumps there.">
        <SettingRow title="Look up lyrics online" help={f.lyrics_available ? 'When your files have none, LRCLIB (free, open) is asked by artist, title, album and length — mostly synced lyrics.' : 'Switched off on this server (LYRICS_URL=off).'}><Switch checked={f.lyrics_online && f.lyrics_available} disabled={!f.lyrics_available} onChange={v => setF({ ...f, lyrics_online: v })} label="Look up lyrics online" /></SettingRow>
        <SettingRow title="When your files have lyrics without timings" help="Synced lyrics follow the song; your files' own text may be more exact (live versions, translations).">
          <Segmented size="sm" value={f.lyrics_prefer} onChange={v => setF({ ...f, lyrics_prefer: v })} options={[{ value: 'synced', label: 'Prefer synced' }, { value: 'library', label: 'Keep my files’' }]} />
        </SettingRow>
        <SettingRow title="Text size"><Segmented size="sm" value={f.lyrics_size} onChange={v => setF({ ...f, lyrics_size: v })} options={[{ value: 's', label: 'Small' }, { value: 'm', label: 'Medium' }, { value: 'l', label: 'Large' }]} /></SettingRow>
        <SettingRow title="Open on the lyrics" help="On phones Now Playing shows one thing at a time: start on the lyrics when the song has synced ones."><Switch checked={f.lyrics_auto} onChange={v => setF({ ...f, lyrics_auto: v })} label="Open Now Playing on the lyrics" /></SettingRow>
        <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
      </Section>
      {isAdmin ? <LyricsLibrarySection /> : (
        <Section title="Lyrics library" icon="database" sub="An admin can have synced lyrics fetched every night and saved next to the songs, so every app shows them." />
      )}
    </div>
  )
}

function LyricsLibrarySection() {
  const { f, setF, save, busy } = useSettingsForm()
  const { toast } = useToast()
  const [info, setInfo] = useState<LyricsLibrary | null>(null)
  const load = () => api.get<LyricsLibrary>(`${V1}/lyrics-library`).then(setInfo).catch(e => toast(errMsg(e), 'err'))
  useEffect(() => { load() }, []) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!info?.running) return
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [info?.running]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!f || !info) return <Loading />
  const run = info.runs[0]
  const start = async () => { try { setInfo(await api.post<LyricsLibrary>(`${V1}/lyrics-library/run`)); toast('Looking for lyrics') } catch (e) { toast(errMsg(e), 'err') } }
  const stop = async () => { await api.post(`${V1}/lyrics-library/stop`).catch(() => {}); setTimeout(load, 600) }
  return (
    <Section title="Lyrics library" icon="database" actions={<span className={`badge ${!info.writable ? 'warn' : f.lyrics_writer ? 'ok' : ''}`}>{!info.writable ? 'not available' : info.running ? 'running' : f.lyrics_writer ? `nightly at ${String(f.lyrics_hour).padStart(2, '0')}:00` : 'off'}</span>}
      sub={<>For the whole box: every song without lyrics is looked up at LRCLIB and synced lyrics are saved as a .lrc file next to it — so Navidrome, phone apps and the car show them too, offline. Songs LRCLIB doesn't know yet are asked again later, never every night. Browse, search and edit them all on the <Link to="/lyrics">Lyrics</Link> page.</>}>
      {!info.writable && <p className="conn-status bad"><span className="dot" />{info.detail}</p>}
      <SettingRow title="Fetch lyrics every night" help={info.writable ? <>Music folder: <code>{info.music_dir}</code></> : undefined}><Switch checked={f.lyrics_writer} disabled={!info.writable} onChange={v => setF({ ...f, lyrics_writer: v })} label="Fetch lyrics every night" /></SettingRow>
      <SettingRow title="At"><Select ariaLabel="Hour" value={String(f.lyrics_hour)} onChange={v => setF({ ...f, lyrics_hour: Number(v) })} options={HOURS} /></SettingRow>
      <SettingRow title="Lyrics without timings" help="Some songs only have plain text. A .lrc without timings shows as a pane that never scrolls in some apps; the FLAC tag is where most players look for plain lyrics.">
        <Select ariaLabel="Lyrics without timings" value={f.lyrics_plain} onChange={v => setF({ ...f, lyrics_plain: v as SettingsT['lyrics_plain'] })} options={[{ value: 'tag', label: 'Into the FLAC tag', hint: 'UNSYNCEDLYRICS; other formats skipped' }, { value: 'lrc', label: 'As a .lrc file', hint: 'any format' }, { value: 'skip', label: 'Leave them' }]} />
      </SettingRow>
      <SettingRow title="Ask again after" help="Songs LRCLIB didn't know: lyrics get added there every day."><span className="row"><input type="number" min={1} max={365} value={f.lyrics_retry_days} onChange={e => setF({ ...f, lyrics_retry_days: Number(e.target.value) })} className="set-time" /><span className="muted small">days</span></span></SettingRow>
      <SettingRow title="Pause between requests" help="LRCLIB is free: be kind. 500 ms ≈ 7 000 songs an hour."><span className="row"><input type="number" min={0} max={10000} step={100} value={f.lyrics_delay_ms} onChange={e => setF({ ...f, lyrics_delay_ms: Number(e.target.value) })} className="set-time" /><span className="muted small">ms</span></span></SettingRow>
      <SettingRow title="Order"><Segmented size="sm" value={f.lyrics_order} onChange={v => setF({ ...f, lyrics_order: v })} options={[{ value: 'added', label: 'Newest songs first' }, { value: 'path', label: 'A to Z by folder' }]} /></SettingRow>
      <SettingRow title="Ask the music server to scan afterwards" help="So new lyrics show up in every app within minutes."><Switch checked={f.lyrics_rescan} onChange={v => setF({ ...f, lyrics_rescan: v })} label="Scan after a run" /></SettingRow>
      <SettingRow title="Tell me what a run found" help="Through your notifications (Settings → Notifications), only when it found something or failed."><Switch checked={f.lyrics_notify} onChange={v => setF({ ...f, lyrics_notify: v })} label="Notify after a run" /></SettingRow>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        {info.running ? <button type="button" className="btn" onClick={stop}><Icon name="x" size={14} />Stop</button>
          : <button type="button" className="btn" onClick={start} disabled={!info.writable}><Icon name="refresh" size={14} />Run now</button>}
        <span className="muted small">{info.synced_total.toLocaleString()} songs got synced lyrics here · {info.attempts.toLocaleString()} asked so far</span>
      </div>
      {run && run.state === 'running' && <p className="row small"><Spinner />Checked {run.checked.toLocaleString()} of {run.total.toLocaleString()} songs · {run.fetched} found so far</p>}
      {info.runs.length > 0 && (
        <table className="table compact mt-s">
          <thead><tr><th>Run</th><th>When</th><th className="right">Synced</th><th className="right">Plain</th><th className="right">Not found</th><th className="right">Waiting</th><th className="right">Had lyrics</th><th>State</th></tr></thead>
          <tbody>{info.runs.map(r => (
            <tr key={r.id} title={r.note}>
              <td>{RUN_LABEL[r.trigger] || r.trigger}</td><td className="muted">{fmtDateTime(r.started_at)}</td>
              <td className="right tnum">{r.fetched}</td><td className="right tnum">{r.plain}</td><td className="right tnum">{r.not_found}</td><td className="right tnum">{r.backoff.toLocaleString()}</td><td className="right tnum">{r.have.toLocaleString()}</td>
              <td className={r.state === 'error' ? 'danger-text' : r.state === 'done' ? '' : 'muted'}>{r.state === 'error' ? r.note || 'error' : r.state}</td>
            </tr>))}</tbody>
        </table>
      )}
    </Section>
  )
}

// ---------- phone & car apps ----------

const APPS: { name: string; where: string }[] = [
  { name: 'Amperfy', where: 'iPhone, iPad, Mac' }, { name: 'play:Sub', where: 'iPhone, CarPlay' }, { name: 'Substreamer', where: 'iPhone, Android' },
  { name: 'Symfonium', where: 'Android, Android Auto' }, { name: 'Tempo', where: 'Android' }, { name: 'Ultrasonic', where: 'Android' },
  { name: 'Feishin', where: 'Mac, Windows, Linux' }, { name: 'Sonixd', where: 'Mac, Windows, Linux' },
]

function PhoneApps() {
  const { toast } = useToast()
  const [info, setInfo] = useState<SubsonicLogin | null>(null)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [off, setOff] = useState(false)
  useEffect(() => { api.get<SubsonicLogin>(`${V1}/subsonic`).then(x => { setInfo(x); setName(x.username || x.suggested) }).catch(e => toast(errMsg(e), 'err')) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  if (!info) return <Loading />
  const make = async () => {
    setBusy(true)
    try { const x = await api.post<SubsonicLogin>(`${V1}/subsonic/password`, { username: name }); setInfo(x); toast(info.enabled ? 'New password made — the old one stopped working' : 'Your app login is ready') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const server = info.server_url || window.location.origin
  return (
    <div className="stack loose">
      <Section title="Your app login" icon="smartphone" actions={<span className={`badge ${info.enabled ? 'ok' : ''}`}>{info.enabled ? 'on' : 'off'}</span>}
        sub="Pocket Music speaks the Subsonic language, so any Subsonic app can play your library through it — with this login, not your music server password. Plays from those apps count in your history, stats and mixes here, and they see your playlists and loves.">
        {info.enabled ? <>
          <Field label="Server"><CopyField value={server} label="Server address" /></Field>
          <Field label="User name"><CopyField value={info.username || ''} label="User name" /></Field>
          {info.password
            ? <div className="set-fresh"><div className="row"><Icon name="check-circle" size={16} /><b>Your app password — copy it now, it is shown once.</b></div><CopyField value={info.password} label="App password" /></div>
            : <p className="muted small">Password: made {info.created_at ? fmtDateTime(info.created_at) : ''}; it is not shown again. Lost it? Make a new one.</p>}
          <p className="muted small">{info.last_used_at ? <>Last used {fmtDateTime(info.last_used_at)}{info.last_client ? <> by <b>{info.last_client}</b></> : null}.</> : 'Not used by an app yet.'}</p>
          <div className="row wrap">
            <button type="button" className="btn" onClick={make} disabled={busy}><Icon name="refresh" size={14} />New password</button>
            <button type="button" className="btn ghost danger-text" onClick={() => setOff(true)}>Turn off</button>
          </div>
        </> : <>
          <Field label="User name" help="What you type into the app. Letters, digits, dots, dashes."><input value={name} onChange={e => setName(e.target.value)} autoComplete="off" /></Field>
          <div className="row"><button type="button" className="btn primary" onClick={make} disabled={busy || !name.trim()}>{busy ? <Spinner /> : <Icon name="key" size={14} />}Make my app password</button></div>
        </>}
        {off && <Confirm confirmLabel="Turn off" title="Turn off app sign-in?" message="Apps signed in with this login stop playing until you make a new password." onCancel={() => setOff(false)}
          onConfirm={async () => { await api.del(`${V1}/subsonic`); setOff(false); setInfo({ ...info, enabled: false, password: undefined }) }} />}
      </Section>
      <Section title="Setting up an app" icon="info" sub="Add a server of type Subsonic (or Navidrome) with the address, user name and password above. Leave “legacy authentication” off.">
        <table className="table compact">
          <thead><tr><th>App</th><th>Runs on</th></tr></thead>
          <tbody>{APPS.map(a => <tr key={a.name}><td><b>{a.name}</b></td><td className="muted">{a.where}</td></tr>)}</tbody>
        </table>
        <p className="muted small mt-s">The address only answers where this box is reachable (at home or over your VPN). For offline listening use the app's own downloads — or Pocket Music's (album menu → Download).</p>
      </Section>
    </div>
  )
}

// ---------- mixes & AI ----------

function Mixes() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  const ai = useAIStatus()
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  const shown = !coreOn && f.ai_provider === 'pocket' ? 'openai' : (f.ai_provider || 'auto')
  return (
    <div className="stack loose">
      <Section title="What goes into mixes and radio" icon="sparkles" sub="Daily mixes, radio and shuffle come from your own library and plays. Background music makes poor radio — keep it to its own Focus mix.">
        <SettingRow title="Keep background genres out" help="They get their own “Focus” mix and come back when you ask for them by name."><Switch checked={f.skip_ambient} onChange={v => setF({ ...f, skip_ambient: v })} label="Keep background genres out of mixes" /></SettingRow>
        <SettingRow title="Background genres" stack><input value={f.ambient_genres} onChange={e => setF({ ...f, ambient_genres: e.target.value })} /></SettingRow>
        <SettingRow title="Never in mixes or radio" help="Comma-separated genres: audiobooks, comedy, children’s songs…" stack><input value={f.hide_genres} onChange={e => setF({ ...f, hide_genres: e.target.value })} placeholder="Audiobook, Comedy, Spoken Word" /></SettingRow>
      </Section>
      <Section title="Mixes in other apps" icon="playlist" sub="Phone and car apps only see playlists on the music server. With this on, every morning your mixes are written there as playlists (“Pocket · Daily Mix 1” …) and refreshed in place; Pocket Music itself keeps showing them under Mixes.">
        <SettingRow title="Publish mixes as playlists" help="Switching it off removes those playlists from the server again."><Switch checked={f.mixes_to_server} onChange={v => save({ mixes_to_server: v })} label="Publish mixes as playlists" /></SettingRow>
      </Section>
      <Section title="Make a mix: AI" icon="wand" sub="The AI sees your genre names and best-known artists — never your files or plays — and answers with a plan (genres, artists, years) that runs over your library here. Without AI the plan is guessed from the words.">
        <AIStatusLine st={ai} />
        <div className="grid" style={{ gap: 12 }}>
          <Field group label="Who plans the mix">
            <Select ariaLabel="AI provider" value={shown} onChange={v => setF({ ...f, ai_provider: v as SettingsT['ai_provider'] })} options={[{ value: 'auto', label: coreOn ? 'Automatic — prefer Pocket AI' : 'Automatic' }, { value: 'pocket', label: 'Pocket AI', disabled: !coreOn, hint: coreOn ? undefined : 'needs Pocket Core' }, { value: 'openai', label: 'OpenAI / compatible API' }]} />
          </Field>
          {!coreOn && <CoreHint />}
          {coreOn && shown !== 'openai' && <PocketModelSelect value={f.ai_pocket_model || ''} onChange={v => setF({ ...f, ai_pocket_model: v })} />}
          {(shown === 'openai' || (!coreOn && shown === 'auto')) && <>
            <Field label="OpenAI API key" help="Stored on the server; shown masked."><input value={f.openai_api_key} onChange={e => setF({ ...f, openai_api_key: e.target.value })} autoComplete="off" placeholder="sk-…" /></Field>
            <div className="two-col">
              <Field label="Model" help="Default gpt-4o-mini."><input value={f.openai_model} onChange={e => setF({ ...f, openai_model: e.target.value })} placeholder="gpt-4o-mini" /></Field>
              <Field label="Base URL (optional)" help="Ollama, LM Studio, OpenRouter…"><input value={f.openai_base_url} onChange={e => setF({ ...f, openai_base_url: e.target.value })} placeholder="https://api.openai.com/v1" /></Field>
            </div>
          </>}
        </div>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        {(shown === 'openai' || (!coreOn && shown === 'auto')) && <><button type="button" className="btn" onClick={() => run('openai', f.openai_base_url, f.openai_api_key)} disabled={!f.openai_api_key}>Test the key</button>{status('openai')}</>}
      </div>
    </div>
  )
}

// ---------- Lidarr ----------

function Lidarr() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  const mode = !coreOn && f.lidarr_mode === 'core' ? 'off' : f.lidarr_mode
  return (
    <div className="stack loose">
      <Section title="Lidarr" icon="download" sub="Lidarr finds and downloads albums. Pocket Music asks it for an artist’s whole discography (to show what you’re missing), for new releases, and to fetch an album you request. On most boxes Lidarr is for admins only.">
        <div className="conn-block">
          <div className="conn-mode">
            <Segmented size="sm" value={mode} onChange={m => { setF({ ...f, lidarr_mode: m }); if (m !== 'direct') save({ lidarr_mode: m }) }} options={[
              { value: 'core', label: 'Pocket Core', disabled: !coreOn, title: coreOn ? 'Lidarr as set up in Pocket Core' : 'Needs Pocket Core' }, { value: 'direct', label: 'Direct' }, { value: 'off', label: 'Off' }]} />
          </div>
          {!coreOn && <CoreHint />}
          {mode === 'direct' && <div className="two-col">
            <Field label="Lidarr URL" help="Inside Docker: http://host.docker.internal:8686"><input value={f.lidarr_url} onChange={e => setF({ ...f, lidarr_url: e.target.value })} placeholder="http://lidarr:8686" /></Field>
            <Field label="API key" help="Lidarr → Settings → General"><input value={f.lidarr_key} onChange={e => setF({ ...f, lidarr_key: e.target.value })} autoComplete="off" /></Field>
          </div>}
          <div className="row wrap">
            {mode === 'direct' && <button type="button" className="btn primary sm" onClick={() => save()} disabled={busy}>Save</button>}
            {mode !== 'off' && <><button type="button" className="btn sm" onClick={() => run('lidarr', mode === 'direct' ? f.lidarr_url : '', mode === 'direct' ? f.lidarr_key : '')}>Test connection</button>{status('lidarr')}</>}
          </div>
        </div>
      </Section>
    </div>
  )
}

// ---------- history import ----------

function HistoryImport() {
  const { f, setF } = useSettingsForm()
  const { toast } = useToast()
  const { run, status } = useTester()
  const { data, reload } = useAsync(() => api.get<ImportRun[]>(`${V1}/imports`), [])
  useEffect(() => onLive(ev => { if (ev.type === 'import') reload() }), [reload])
  useEffect(() => {
    if (!data?.some(i => i.state === 'running')) return
    const t = setInterval(reload, 3000)
    return () => clearInterval(t)
  }, [data, reload])
  if (!f) return <Loading />
  const start = async (source: string, body: Record<string, string>) => {
    try { await api.post(`${V1}/imports`, { source, ...body }); toast('Import started'); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div className="stack loose">
      <Section title="Maloja" icon="history" sub="Your self-hosted scrobble server. Every scrobble comes in once; plays are matched to your library by artist and title, and the rest still count in your stats.">
        <SettingRow title="Maloja address" stack><input value={f.maloja_url} onChange={e => setF({ ...f, maloja_url: e.target.value })} placeholder={f.maloja_default || 'http://maloja:42010'} /></SettingRow>
        <div className="row wrap"><button type="button" className="btn primary sm" onClick={() => start('maloja', { url: f.maloja_url || f.maloja_default })}><Icon name="download" size={14} />Import from Maloja</button><button type="button" className="btn sm" onClick={() => run('maloja', f.maloja_url || f.maloja_default, '')}>Test</button>{status('maloja')}</div>
      </Section>
      <Section title="ListenBrainz" icon="history" sub="Public profiles need only the user name; a token reads a private one.">
        <div className="two-col">
          <Field label="User name"><input value={f.listenbrainz_user} onChange={e => setF({ ...f, listenbrainz_user: e.target.value })} /></Field>
          <Field label="Token (optional)"><input value={f.listenbrainz_token} onChange={e => setF({ ...f, listenbrainz_token: e.target.value })} autoComplete="off" /></Field>
        </div>
        <div className="row wrap mt-s"><button type="button" className="btn primary sm" disabled={!f.listenbrainz_user} onClick={() => start('listenbrainz', { user: f.listenbrainz_user, token: f.listenbrainz_token })}><Icon name="download" size={14} />Import from ListenBrainz</button><button type="button" className="btn sm" disabled={!f.listenbrainz_user} onClick={() => run('listenbrainz', f.listenbrainz_user, '')}>Test</button>{status('listenbrainz')}</div>
      </Section>
      {data && data.length > 0 && (
        <Section title="Imports">
          <table className="table compact">
            <thead><tr><th>Source</th><th>Started</th><th className="right">Seen</th><th className="right">New</th><th className="right">Matched</th><th>State</th></tr></thead>
            <tbody>{data.map(i => <tr key={i.id}><td>{i.source}</td><td className="muted">{fmtDateTime(i.started_at)}</td><td className="right tnum">{i.seen}</td><td className="right tnum">{i.imported}</td><td className="right tnum">{i.matched}</td>
              <td>{i.state === 'running' ? <span className="row"><Spinner />running</span> : i.state === 'error' ? <span className="danger-text small" title={i.error}>failed: {i.error}</span> : <span className="badge ok">done</span>}</td></tr>)}</tbody>
          </table>
        </Section>
      )}
    </div>
  )
}

// ---------- notifications ----------

function Notifications() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Push" icon="bell" actions={<span className={`badge ${f.notify_configured ? 'ok' : ''}`}>{f.notify_configured ? 'on' : 'not set'}</span>}
        sub={<>Apprise gateway (<code>…/notify/&lt;key&gt;</code>), an ntfy topic URL, or any webhook that takes JSON.</>}>
        <SettingRow title="URL" stack><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="http://apprise:8000/notify/music" /></SettingRow>
        <SettingRow title="Format"><Segmented value={f.notify_format} onChange={v => setF({ ...f, notify_format: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'json', label: 'JSON' }]} size="sm" /></SettingRow>
        <SettingRow title="Requested albums arrive" help="When Lidarr has every track of an album you asked for."><Switch checked={f.notify_arrived} onChange={v => setF({ ...f, notify_arrived: v })} label="Push arrived albums" /></SettingRow>
        <SettingRow title="Weekly recap" help="Monday morning: how much you listened, your top artists, how many songs were new to you."><Switch checked={f.notify_weekly} onChange={v => setF({ ...f, notify_weekly: v })} label="Weekly recap" /></SettingRow>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        <button type="button" className="btn" onClick={() => run('notify', f.notify_url, '', { format: f.notify_format })} disabled={!f.notify_url}><Icon name="mail" size={15} />Send a test</button>
        {status('notify')}
      </div>
      <Section title="Date & time">
        <SettingRow title="Date format"><Select value={f.date_format} onChange={v => setF({ ...f, date_format: v as DateFormat })} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        <SettingRow title="Time format"><Select value={f.time_format} onChange={v => setF({ ...f, time_format: v as TimeFormat })} options={TIME_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
      </Section>
    </div>
  )
}

// ---------- people, profile, tokens, data, about ----------

function Users() {
  const { user, users, reloadMeta, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  return (
    <Section title="People on this box" actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member' })}><Icon name="plus" size={14} />Add person</button>}
      sub="Everyone signs in with their own account and plays with their own music-server account: their own history, stats, mixes and playlists. Admins manage accounts; they don't see anyone else's listening.">
      <div className="set-people">
        {users.map(u => (
          <div key={u.id} className="set-person">
            <Avatar name={u.name} email={u.email} size={34} />
            <div className="grow">
              <div className="set-person-name">{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge">you</span>}{u.role === 'admin' && <span className="badge accent">admin</span>}</div>
              <div className="muted small ellipsis">{u.email}</div>
            </div>
            {isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}
          </div>
        ))}
      </div>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadMeta() }} />}
      {del && <Confirm confirmLabel="Remove" title={`Remove ${del.name || del.email}?`} message="Their account is deleted here with their plays, smart playlists and requests. Their account on the music server stays." onCancel={() => setDel(null)}
        onConfirm={async () => { const d = del; setDel(null); try { await api.del(`${V1}/users/${d.id}`); await reloadMeta(); toast('Removed') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </Section>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f); toast(user.id ? 'Saved' : 'Added'); onSaved() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit person' : 'Add a person'} onClose={onClose} size="sm">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add person'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setUser(await api.put(`${V1}/auth/me`, f)); setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <form onSubmit={save} className="stack loose">
      <div className="set-profile">
        <Avatar name={user?.name} email={user?.email} size={56} />
        <div className="grow"><h3>{user?.name || user?.email}</h3><p className="muted small">{user?.email} · {user?.role}</p></div>
      </div>
      <Section title="You"><div className="stack">
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
        <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
      </div></Section>
      {status?.auth_mode === 'local' && <Section title="Password" sub="Leave empty to keep the current one."><div className="stack">
        <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
        <Field label="New password" help="At least 8 characters."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
      </div></Section>}
      <div className="row"><button className="btn primary" disabled={busy}>Save changes</button></div>
    </form>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { settings } = useApp()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  const base = settings?.base_url || window.location.origin
  return (
    <div className="stack loose">
      <Section title="Personal tokens" icon="key" sub={<>Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you. See <code>docs/api.md</code>.</>}>
        <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. Homepage widget)" /><button className="btn primary"><Icon name="plus" size={15} />Create</button></form>
        {fresh && <div className="set-fresh mb"><div className="row"><Icon name="check-circle" size={16} /><b>Copy it now — it is shown once.</b></div><pre className="mono">{fresh}</pre></div>}
        {data && data.length > 0 ? (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Prefix</th><th>Last used</th><th>Created</th><th /></tr></thead>
            <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="mono">{t.prefix}…</td><td className="muted">{t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'}</td><td className="muted">{fmtDateTime(t.created_at)}</td><td className="right"><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
          </table>
        ) : <p className="muted">No tokens yet.</p>}
        {del && <Confirm confirmLabel="Revoke" title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
      </Section>
      <Section title="Handy endpoints" icon="smartphone" sub={<>At <code>{base}/api/v1</code>.</>}>
        <dl className="set-dl">
          <div><dt><code>GET /dashboard</code></dt><dd className="muted">Now playing, minutes today and this week, top artist, library size — for a Homepage widget.</dd></div>
          <div><dt><code>GET /day/2026-10-24</code></dt><dd className="muted">One day's listening (Pocket Day uses it).</dd></div>
          <div><dt><code>GET /shuffle</code> · <code>GET /radio?artist=…</code></dt><dd className="muted">Songs to play, for scripts.</dd></div>
          <div><dt><code>{base}/?play=shuffle</code></dt><dd className="muted">Opens the app and shuffles your library (a Siri Shortcut or a Stream Deck button).</dd></div>
        </dl>
      </Section>
    </div>
  )
}

function Data() {
  const { toast } = useToast()
  const { user } = useApp()
  const [busy, setBusy] = useState(false)
  const importJSON = async (file: File) => {
    setBusy(true)
    try { const r = await api.post<Record<string, number>>(`${V1}/import`, JSON.parse(await file.text())); toast('Imported: ' + (Object.entries(r).map(([k, v]) => `${v} ${k.replace('_', ' ')}`).join(', ') || 'nothing new')) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <div className="stack loose">
      {user?.role === 'admin' && <Section title="Database backup" icon="database" sub="The whole box, every account — admins only.">
        <p className="muted">A consistent copy of the SQLite database (<code>VACUUM INTO</code>). The library index is rebuilt from the music server, so the part that matters is plays, smart playlists and settings. Restore by stopping the app and replacing <code>pocket-music.db</code>.</p>
        <a className="btn primary" href={`${V1}/backup`}><Icon name="download" size={15} />Download database</a>
      </Section>}
      <Section title="Your plays" icon="archive" sub="Every play as CSV (for a spreadsheet) or JSON with your smart playlists (to move to another box).">
        <div className="row wrap">
          <a className="btn" href={`${V1}/history/export`}><Icon name="download" size={15} />Plays as CSV</a>
          <a className="btn" href={`${V1}/export.json`}><Icon name="download" size={15} />Export JSON</a>
          <label className="btn"><Icon name="upload" size={15} />{busy ? 'Importing…' : 'Import JSON…'}<input type="file" accept="application/json,.json" hidden onChange={e => e.target.files?.[0] && importJSON(e.target.files[0])} /></label>
        </div>
      </Section>
    </div>
  )
}

function About() {
  const { data, error } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const [keys, setKeys] = useState(false)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  const counts = (data.counts || {}) as Record<string, number>
  const tiles: [string, number | undefined, string][] = [['Plays', counts.listens, 'history'], ['Songs indexed', counts.songs, 'music'], ['Smart playlists', counts.smart_playlists, 'zap'], ['Requests', counts.requests, 'download']]
  const core = (data.core || {}) as { configured: boolean; registered: boolean; url: string; error: string }
  const rows: [string, React.ReactNode][] = [
    ['Version', <code key="v">{String(data.version)}</code>],
    ['Uptime', String(data.uptime)],
    ['Runtime', `${String(data.go)} · ${String(data.os)}/${String(data.arch)}`],
    ['Time zone', String(data.timezone)],
    ['Sign-in', String(data.auth_mode)],
    ['Music server', data.navidrome_url ? <code key="n">{String(data.navidrome_url)}</code> : 'each person enters their own'],
    ['Pocket Core', core.configured ? (core.registered ? <span key="c">registered at <code>{core.url}</code></span> : <span key="c" className="danger-text">not registered: {core.error}</span>) : 'not configured'],
    ['Database', <span key="db"><code>{String(data.db_path)}</code> · {fmtBytes(Number(data.db_bytes))}</span>],
    ['People', `${counts.users ?? 0} accounts on this box`],
  ]
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <h3>Pocket Music</h3>
          <p className="muted small">Your music library, played anywhere. Navidrome (or any Subsonic server) keeps the files and streams them; Pocket Music adds the player, your listening history and stats, daily mixes and radio, smart playlists every Subsonic app sees, synced lyrics, and Lidarr for the albums you’re missing. One binary, one SQLite file.</p>
        </div>
      </div>
      <div className="set-stats">
        {tiles.map(([label, n, icon]) => <div key={label} className="set-stat"><Icon name={icon} size={16} /><span className="set-stat-n tnum">{(n ?? 0).toLocaleString()}</span><span className="set-stat-l">{label}</span></div>)}
      </div>
      <Section title="This server"><dl className="set-dl">{rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl></Section>
      <div className="row wrap">
        <button type="button" className="btn" onClick={() => setKeys(true)}><Icon name="keyboard" size={15} />Keyboard shortcuts</button>
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-music" target="_blank" rel="noreferrer"><Icon name="external" size={15} />Project on GitHub</a>
      </div>
      <p className="muted small">Open source (MIT). Part of the Pocket apps: Pocket Core connects them — Day, Calendar, Podcasts, Photos and more.</p>
      {keys && <ShortcutsSheet onClose={() => setKeys(false)} />}
    </div>
  )
}
