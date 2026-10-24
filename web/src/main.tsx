import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import './styles/base.css'
import './styles/app.css'
import './styles/shared.css'
import './styles/music.css'
import './styles/settings.css'
import { AppProvider, useApp } from './lib/app-context'
import { Loading, ToastProvider } from './components/ui'
import Layout from './components/Layout'
import { ErrorBoundary } from './components/ErrorBoundary'
import Login from './pages/Login'
import Home from './pages/Home'
import { Albums, Artists, GenrePage, Genres, Songs } from './pages/Library'
import Album from './pages/Album'
import Artist from './pages/Artist'
import { Playlist, Playlists, SmartEditor } from './pages/Playlists'
import { MixPage, Mixes } from './pages/Mixes'
import History from './pages/History'
import Stats, { Recap } from './pages/Stats'
import { Releases, Requests } from './pages/Discover'
import Settings from './pages/Settings'
import Downloads from './pages/Downloads'
import { LyricsEditor, LyricsLibrary } from './pages/Lyrics'
import { registerServiceWorker } from './lib/offline'

function Gate({ children }: { children: React.ReactNode }) {
  const { status, user } = useApp()
  const loc = useLocation()
  if (!status) return <div className="boot"><img src="/favicon.svg" alt="" /><Loading /></div>
  if (status.needs_setup) return <Navigate to="/setup" replace />
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />
  return <>{children}</>
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login mode="login" />} />
      <Route path="/setup" element={<Login mode="setup" />} />
      <Route path="/settings/:tab?" element={<Gate><ErrorBoundary where="settings"><Settings /></ErrorBoundary></Gate>} />
      <Route element={<Gate><ErrorBoundary where="music"><Layout /></ErrorBoundary></Gate>}>
        <Route path="/" element={<Home />} />
        <Route path="/mix" element={<Home />} />
        <Route path="/albums" element={<Albums />} />
        <Route path="/albums/:id" element={<Album />} />
        <Route path="/artists" element={<Artists />} />
        <Route path="/artists/:id" element={<Artist />} />
        <Route path="/songs" element={<Songs />} />
        <Route path="/loved" element={<Songs loved />} />
        <Route path="/genres" element={<Genres />} />
        <Route path="/genres/:name" element={<GenrePage />} />
        <Route path="/lyrics" element={<LyricsLibrary />} />
        <Route path="/lyrics/:id" element={<LyricsEditor />} />
        <Route path="/playlists" element={<Playlists />} />
        <Route path="/playlists/:id" element={<Playlist />} />
        <Route path="/smart/:id" element={<SmartEditor />} />
        <Route path="/mixes" element={<Mixes />} />
        <Route path="/mixes/:key" element={<MixPage />} />
        <Route path="/history" element={<History />} />
        <Route path="/stats" element={<Stats />} />
        <Route path="/recap" element={<Recap />} />
        <Route path="/releases" element={<Releases />} />
        <Route path="/requests" element={<Requests />} />
        <Route path="/downloads" element={<Downloads />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

registerServiceWorker()

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary where="app"><ToastProvider><AppProvider><App /></AppProvider></ToastProvider></ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)
