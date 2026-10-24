// "Keep offline" for an album, playlist or mix: downloads its songs onto this
// device (lib/offline.ts) and shows how far that got.
import { useState } from 'react'
import { coverage, download, offlineSupported, removeGroup, useOffline, type OfflineGroup } from '../lib/offline'
import type { Song } from '../lib/types'
import { Icon } from './icons'
import { Confirm, Spinner, errMsg, useToast } from './ui'

export function OfflineButton({ songs, group }: { songs: Song[]; group: OfflineGroup }) {
  const st = useOffline()
  const { toast } = useToast()
  const [ask, setAsk] = useState(false)
  if (!offlineSupported() || !songs.length) return null
  const have = coverage(songs)
  const busy = !!st.active && (st.groups.some(g => g.key === group.key) && have !== 'all')
  const get = () => download(songs, group).then(() => toast(`Downloading ${songs.length} song${songs.length === 1 ? '' : 's'} for offline`)).catch(e => toast(errMsg(e), 'err'))
  const n = songs.filter(s => st.songs[s.id]).length
  return <>
    <button type="button" className={`btn lg icon-only offline-btn ${have === 'all' ? 'is-on' : ''}`} disabled={busy}
      aria-label={have === 'all' ? 'Downloaded — remove from this device' : 'Download for offline'}
      title={busy ? `Downloading… ${n} of ${songs.length}` : have === 'all' ? 'On this device — click to remove' : have === 'some' ? `${n} of ${songs.length} songs on this device — download the rest` : 'Download for offline'}
      onClick={() => (have === 'all' ? setAsk(true) : get())}>
      {busy ? <Spinner size={16} /> : <Icon name={have === 'all' ? 'check-circle' : 'download'} size={17} />}
    </button>
    {ask && <Confirm title={`Remove “${group.name}” from this device?`} message="The songs stay in your library; they just won't play without a network any more." confirmLabel="Remove"
      onCancel={() => setAsk(false)} onConfirm={async () => { setAsk(false); await removeGroup(group.key); toast('Removed from this device') }} />}
  </>
}
