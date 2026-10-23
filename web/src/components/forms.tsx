// Dialogs opened from the shell: "Make a mix", a new playlist.
import React, { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { V1, albumCover, longDuration, totalDuration } from '../lib/music'
import { player } from '../lib/player-react'
import type { Playlist, Song } from '../lib/types'
import { Icon } from './icons'
import { Cover } from './music'
import { Field, Modal, Segmented, Spinner, errMsg, useToast } from './ui'

export interface AIStatus { ready: boolean; provider: string; detail: string }
export function useAIStatus() {
  const [st, setSt] = useState<AIStatus | null>(null)
  useEffect(() => { api.get<AIStatus>(`${V1}/ai/status`).then(setSt).catch(() => setSt({ ready: false, provider: '', detail: '' })) }, [])
  return st
}
export function AIStatusLine({ st }: { st: AIStatus | null }) {
  if (!st) return null
  return st.ready
    ? <p className="ai-line ok"><Icon name="sparkles" size={14} />{st.provider === 'pocket' ? 'Pocket AI' : 'Your OpenAI key'} plans the mix from your genres and artists.</p>
    : <p className="ai-line"><Icon name="info" size={14} />No AI connected{st.detail ? ` (${st.detail})` : ''}: the mix is matched from the words — genres, artists, decades, moods. Settings → Mixes & AI connects one.</p>
}

const IDEAS = ['Calm piano for a rainy Sunday', 'Upbeat 80s for cleaning the flat', 'Heavy riffs for the gym', 'Soundtracks to focus', 'Old favourites I forgot about', 'Songs I have never played by bands I love']

interface MixResult { name: string; description: string; songs: Song[]; ai: string; playlist_id?: string }

export function MakeMixModal({ onClose, initial = '' }: { onClose: () => void; initial?: string }) {
  const { toast } = useToast()
  const navigate = useNavigate()
  const ai = useAIStatus()
  const [prompt, setPrompt] = useState(initial)
  const [count, setCount] = useState(40)
  const [busy, setBusy] = useState(false)
  const [res, setRes] = useState<MixResult | null>(null)
  const make = async (e?: React.FormEvent) => {
    e?.preventDefault()
    if (!prompt.trim()) return
    setBusy(true); setRes(null)
    try { setRes(await api.post<MixResult>(`${V1}/mix`, { prompt, count })) } catch (err) { toast(errMsg(err), 'err') } finally { setBusy(false) }
  }
  const play = () => { if (res?.songs.length) { player.playSongs(res.songs, 0, { kind: 'mix', name: res.name }); onClose() } }
  const save = async () => {
    if (!res) return
    try {
      const p = await api.post<Playlist>(`${V1}/playlists`, { name: res.name, comment: res.description || `Made by Pocket Music from: ${prompt}`, song_ids: res.songs.map(s => s.id) })
      window.dispatchEvent(new CustomEvent('pmu:playlists'))
      toast(`Saved as “${res.name}”`)
      onClose(); navigate(`/playlists/${p.id}`)
    } catch (err) { toast(errMsg(err), 'err') }
  }
  return (
    <Modal title="Make a mix" onClose={onClose} size="lg">
      <form onSubmit={make} className="stack">
        <Field label="What would you like to hear?">
          <div className="mix-input">
            <Icon name="sparkles" size={18} />
            <input autoFocus value={prompt} onChange={e => setPrompt(e.target.value)} placeholder="e.g. mellow guitar for a long drive at night" maxLength={400} />
            <button className="btn primary" disabled={busy || !prompt.trim()}>{busy ? <Spinner /> : 'Make it'}</button>
          </div>
        </Field>
        {!res && !busy && <div className="chips">{IDEAS.map(i => <button key={i} type="button" className="chip" onClick={() => setPrompt(i)}>{i}</button>)}</div>}
        <div className="row wrap"><span className="muted small">Length</span><Segmented size="sm" value={count} onChange={setCount} options={[{ value: 20, label: '20 songs' }, { value: 40, label: '40' }, { value: 80, label: '80' }]} /></div>
        <AIStatusLine st={ai} />
      </form>
      {busy && <div className="loading"><Spinner /> Listening through your library…</div>}
      {res && (
        <div className="mix-result">
          <div className="mix-result-head">
            <div className="grow">
              <h3>{res.name}</h3>
              <p className="muted small">{[res.description, res.songs.length ? `${res.songs.length} songs · ${longDuration(totalDuration(res.songs))}` : ''].filter(Boolean).join(' · ')}</p>
            </div>
            {res.songs.length > 0 && <>
              <button type="button" className="btn" onClick={save}><Icon name="playlist" size={15} />Save as playlist</button>
              <button type="button" className="btn primary" onClick={play}><Icon name="play" size={15} />Play</button>
            </>}
          </div>
          <div className="mix-preview">
            {res.songs.slice(0, 40).map(s => (
              <div key={s.id} className="mix-song"><Cover src={albumCover(s.album_id, 64)} size={34} label={s.album} /><span className="grow ellipsis"><b>{s.title}</b><span className="muted"> · {s.artist}</span></span></div>
            ))}
          </div>
        </div>
      )}
    </Modal>
  )
}

export function NewPlaylistModal({ onClose, onSaved, songs = [] }: { onClose: () => void; onSaved: (p: Playlist) => void; songs?: Song[] }) {
  const { toast } = useToast()
  const [name, setName] = useState('')
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { const p = await api.post<Playlist>(`${V1}/playlists`, { name, comment, song_ids: songs.map(s => s.id) }); toast(`Created “${name}”`); onSaved(p) } catch (err) { toast(errMsg(err), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="New playlist" onClose={onClose} size="sm">
      <form onSubmit={save} className="stack">
        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} autoFocus required maxLength={120} /></Field>
        <Field label="Description (optional)"><input value={comment} onChange={e => setComment(e.target.value)} maxLength={300} /></Field>
        <p className="muted small">Playlists are saved on your music server, so the Navidrome app, Subsonic players on your phone and in the car see them too.</p>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy || !name.trim()}>Create</button></div>
      </form>
    </Modal>
  )
}
