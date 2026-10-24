// Left column: create button, library sections, your music, discover, playlists — the Pocket Calendar sidebar.
import { type ReactNode, useState } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { coverURL } from '../lib/music'
import type { Playlist } from '../lib/types'
import { Icon } from './icons'
import { createItems, type CreateKind } from './Layout'
import { type Anchor, Menu, rectOf } from './ui'

type NavItem = { to: string; label: string; icon: string; end?: boolean }
const LIBRARY: NavItem[] = [
  { to: '/albums', label: 'Albums', icon: 'album' },
  { to: '/artists', label: 'Artists', icon: 'mic' },
  { to: '/songs', label: 'Songs', icon: 'music' },
  { to: '/genres', label: 'Genres', icon: 'tag' },
  { to: '/lyrics', label: 'Lyrics', icon: 'quote' },
]
const YOURS: NavItem[] = [
  { to: '/mixes', label: 'Mixes', icon: 'sparkles' },
  { to: '/loved', label: 'Loved', icon: 'heart' },
  { to: '/history', label: 'History', icon: 'history' },
  { to: '/stats', label: 'Stats', icon: 'bar' },
  { to: '/downloads', label: 'Downloads', icon: 'check-circle' },
]
const DISCOVER: NavItem[] = [
  { to: '/releases', label: 'New releases', icon: 'compass' },
  { to: '/requests', label: 'Requests', icon: 'download' },
]

export function Sidebar({ onCreate, onNavigate, playlists, sync }: { onCreate: (k: CreateKind) => void; onNavigate: () => void; playlists: Playlist[]; sync: { state: string; progress?: number } | null }) {
  const { status, settings } = useApp()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const title = settings?.title || status?.title || 'Pocket Music'
  const mine = playlists.filter(p => p.mine)
  const shared = playlists.filter(p => !p.mine)
  return (
    <aside className="sidebar" aria-label="Music">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Music' ? title : <>Pocket <b>Music</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^\d/.test(status.version) ? 'v' + status.version : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="plus" size={17} strokeWidth={2.2} />New<Icon name="chevron-down" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={createItems(onCreate)} onClose={() => setMenu(null)} width={248} />}

      <nav className="sb-nav" aria-label="Sections">
        <NavLink to="/" end onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}><Icon name="home" size={16} />Home</NavLink>
      </nav>
      <Group title="Library"><Nav items={LIBRARY} onNavigate={onNavigate} /></Group>
      <Group title="Your music"><Nav items={YOURS} onNavigate={onNavigate} /></Group>
      <Group title="Discover"><Nav items={DISCOVER} onNavigate={onNavigate} /></Group>

      <Group title="Playlists" add={() => onCreate('playlist')} addLabel="New playlist" to="/playlists">
        {mine.length === 0
          ? <p className="sb-empty">Your playlists live on your music server, so every player sees them. <button type="button" className="link-btn" onClick={() => onCreate('playlist')}>Make one</button></p>
          : <ul className="cal-list">{mine.map(p => <PlaylistRow key={p.id} p={p} onNavigate={onNavigate} />)}</ul>}
        {shared.length > 0 && <ul className="cal-list sb-shared">{shared.map(p => <PlaylistRow key={p.id} p={p} onNavigate={onNavigate} />)}</ul>}
      </Group>

      {sync && <div className="sb-sync" role="status"><span className="spinner" /><span className="grow">Indexing your library… {sync.progress ? `${sync.progress}%` : ''}</span></div>}
      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pmu:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

function Nav({ items, onNavigate }: { items: NavItem[]; onNavigate: () => void }) {
  return (
    <nav className="sb-nav tight">
      {items.map(n => (
        <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
          <Icon name={n.icon} size={16} />{n.label}
        </NavLink>
      ))}
    </nav>
  )
}

function PlaylistRow({ p, onNavigate }: { p: Playlist; onNavigate: () => void }) {
  return (
    <li className="cal-row sb-row">
      <NavLink to={`/playlists/${p.id}`} onClick={onNavigate} className={({ isActive }) => `cal-check ${isActive ? 'is-active' : ''}`} title={`${p.name}${p.mine ? '' : ` · by ${p.owner}`}`}>
        <span className="sb-ico img" style={{ backgroundImage: `url(${coverURL(p.coverArt || 'pl-' + p.id, 64)})` }}><Icon name="playlist" size={13} /></span>
        <span className="cal-name ellipsis">{p.name}</span>
        {p.smart_id ? <Icon name="zap" size={12} className="muted" title="Smart playlist" /> : <span className="sb-count">{p.songCount}</span>}
      </NavLink>
    </li>
  )
}

function Group({ title, add, addLabel, to, children }: { title: string; add?: () => void; addLabel?: string; to?: string; children: ReactNode }) {
  const key = `pmu-sb-${title}`
  const [open, setOpen] = useState(() => localStorage.getItem(key) !== '0')
  const toggle = () => setOpen(o => { localStorage.setItem(key, o ? '0' : '1'); return !o })
  return (
    <section className="sb-sec">
      <h4 className="sb-h">
        <button type="button" className="sb-h-btn" aria-expanded={open} onClick={toggle}><Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} />{title}</button>
        <span className="row" style={{ gap: 0 }}>
          {to && <Link to={to} className="btn ghost icon-only xs" aria-label={`All ${title.toLowerCase()}`} title={`All ${title.toLowerCase()}`}><Icon name="grid" size={13} /></Link>}
          {add && <button type="button" className="btn ghost icon-only xs" aria-label={addLabel} title={addLabel} onClick={add}><Icon name="plus" size={14} /></button>}
        </span>
      </h4>
      {open && children}
    </section>
  )
}
