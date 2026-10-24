// The app shell: sidebar, top bar, search palette, shortcuts, the player — the Pocket Calendar layout.
import { signedOut } from '../lib/offline'
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { onLive } from '../lib/live'
import { player, startLive } from '../lib/player-react'
import type { Playlist, Song } from '../lib/types'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { PlayerBar, usePlayerKeys } from './PlayerBar'
import { MakeMixModal, NewPlaylistModal } from './forms'
import { Avatar, type MenuItem, MenuButton, errMsg, modalOpen, useIsMobile, useMediaQuery, useToast } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pmu-sidebar'

/** Something changed from the shell (or a page): the sidebar reloads its playlists. */
export const announceChange = () => window.dispatchEvent(new CustomEvent('pmu:playlists'))

/** The page's own name for the top bar breadcrumb (an album's or an artist's name). */
const CrumbCtx = createContext<(t: string) => void>(() => {})
export function useCrumb(title: string | undefined) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || ''); return () => set('') }, [title, set])
}

export type CreateKind = 'mix' | 'playlist' | 'smart' | 'shuffle' | 'request'
/** Open a create dialog from anywhere (a page's button). */
export const openCreate = (k: CreateKind) => window.dispatchEvent(new CustomEvent('pmu:create', { detail: k }))

export function createItems(onCreate: (k: CreateKind) => void): MenuItem[] {
  return [
    { label: 'Make a mix…', icon: 'sparkles', hint: 'X', onSelect: () => onCreate('mix') },
    { label: 'Shuffle my library', icon: 'shuffle', onSelect: () => onCreate('shuffle') },
    'sep',
    { label: 'New playlist', icon: 'playlist', onSelect: () => onCreate('playlist') },
    { label: 'New smart playlist', icon: 'zap', onSelect: () => onCreate('smart') },
    'sep',
    { label: 'Request an album', icon: 'download', onSelect: () => onCreate('request') },
  ]
}

export async function shuffleLibrary(params = '') {
  const r = await api.get<{ songs: Song[] }>(`${V1}/shuffle${params ? '?' + params : ''}`)
  player.playSongs(r.songs, 0, { kind: 'songs', name: 'Your library, shuffled', link: '/songs' })
}

export default function Layout() {
  const navigate = useNavigate()
  const loc = useLocation()
  const [sp, setSp] = useSearchParams()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])
  usePlayerKeys()
  useEffect(() => { startLive(); player.restore() }, [])

  const [crumb, setCrumbRaw] = useState('')
  const setCrumb = useCallback((t: string) => setCrumbRaw(t), [])

  const [playlists, setPlaylists] = useState<Playlist[]>([])
  const [sync, setSync] = useState<{ state: string; progress?: number } | null>(null)
  const load = useCallback(() => { api.get<Playlist[]>(`${V1}/playlists`).then(setPlaylists).catch(() => setPlaylists([])) }, [])
  useEffect(() => { load() }, [load])
  useEffect(() => {
    const h = () => load()
    window.addEventListener('pmu:playlists', h)
    return () => window.removeEventListener('pmu:playlists', h)
  }, [load])
  useEffect(() => onLive(ev => {
    if (ev.type === 'sync') {
      setSync(ev.data.state === 'syncing' ? ev.data : null)
      if (ev.data.state === 'idle') window.dispatchEvent(new CustomEvent('pmu:synced'))
    }
  }), [])

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [modal, setModal] = useState<CreateKind | null>(null)

  const create = (k: CreateKind) => {
    closeDrawer()
    if (k === 'shuffle') { shuffleLibrary().catch(e => toast(errMsg(e), 'err')); return }
    if (k === 'smart') { navigate('/smart/new'); return }
    if (k === 'request') { navigate('/requests?find=1'); return }
    setModal(k)
  }
  const createRef = useRef(create)
  createRef.current = create

  // /mix (the PWA shortcut) opens "Make a mix"; ?play=shuffle shuffles the library
  useEffect(() => { if (loc.pathname === '/mix') { setModal('mix'); navigate('/', { replace: true }) } }, [loc.pathname, navigate])
  useEffect(() => { if (sp.get('play') === 'shuffle') { sp.delete('play'); setSp(sp, { replace: true }); createRef.current('shuffle') } }, [sp, setSp])

  useEffect(() => {
    const open = () => setShortcuts(true)
    const make = (e: Event) => createRef.current((e as CustomEvent<CreateKind>).detail)
    const pal = () => setPalette(true)
    window.addEventListener('pmu:shortcuts', open)
    window.addEventListener('pmu:create', make)
    window.addEventListener('pmu:palette', pal)
    return () => { window.removeEventListener('pmu:shortcuts', open); window.removeEventListener('pmu:create', make); window.removeEventListener('pmu:palette', pal) }
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop, .np')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'x': e.preventDefault(); createRef.current('mix'); break
        case 'h': go('/'); break
        case 'a': go('/albums'); break
        case 'i': go('/artists'); break
        case 'g': go('/genres'); break
        case 'p': go('/playlists'); break
        case 'y': go('/history'); break
        case 't': go('/stats'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <CrumbCtx.Provider value={setCrumb}>
      <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
        {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
        <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
          <Sidebar onCreate={create} onNavigate={closeDrawer} playlists={playlists} sync={sync} />
        </div>
        <div className="main">
          <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
          <main className="content"><div className="content-inner"><Outlet /></div></main>
          <PlayerBar />
        </div>
        {palette && <CommandPalette onClose={() => setPalette(false)} playlists={playlists} onCreate={create} onShortcuts={() => setShortcuts(true)} />}
        {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
        {modal === 'mix' && <MakeMixModal onClose={() => setModal(null)} />}
        {modal === 'playlist' && <NewPlaylistModal onClose={() => setModal(null)} onSaved={p => { setModal(null); load(); navigate(`/playlists/${p.id}`) }} />}
      </div>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = {
  '': ['Home', '/'], albums: ['Albums', '/albums'], artists: ['Artists', '/artists'], songs: ['Songs', '/songs'], genres: ['Genres', '/genres'],
  playlists: ['Playlists', '/playlists'], smart: ['Playlists', '/playlists'], mixes: ['Mixes', '/mixes'], loved: ['Loved', '/loved'],
  history: ['History', '/history'], stats: ['Stats', '/stats'], releases: ['New releases', '/releases'], requests: ['Requests', '/requests'], recap: ['Stats', '/stats'],
}

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [section, sectionTo] = SECTION[loc.pathname.split('/')[1] || ''] || ['', '/']
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); player.pause(); signedOut(); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pmu:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {crumb ? <><button type="button" className="crumb" onClick={() => navigate(sectionTo)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb}</span></> : section}
      </h1>
      <span className="spacer" />
      {!mobile && <button type="button" className="top-search" onClick={onPalette}><Icon name="search" size={15} /><span>Search your music</span><kbd>/</kbd></button>}
      {mobile && <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>}
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
