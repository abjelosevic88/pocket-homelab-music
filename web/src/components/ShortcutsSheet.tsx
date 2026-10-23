import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Playing', keys: [[['space'], 'Play / pause'], [['k'], 'Play / pause'], [['←', '→'], 'Back / forward 10 s'], [['⇧', '←'], 'Previous song'], [['⇧', '→'], 'Next song'], [['s'], 'Shuffle'], [['r'], 'Repeat: off / all / one'], [['m'], 'Mute']] },
  { title: 'Now Playing', keys: [[['n'], 'Now Playing'], [['l'], 'Lyrics'], [['q'], 'Up next (the queue)'], [['esc'], 'Close']] },
  { title: 'Go to', keys: [[['h'], 'Home'], [['a'], 'Albums'], [['i'], 'Artists'], [['g'], 'Genres'], [['p'], 'Playlists'], [['y'], 'History'], [['t'], 'Stats'], [[','], 'Settings']] },
  { title: 'Find & make', keys: [[['/'], 'Search & commands'], [['⌘', 'K'], 'Search & commands'], [['x'], 'Make a mix'], [['['], 'Show or hide the sidebar'], [['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
      <p className="muted small mt">Media keys and the lock screen work too. In search, <kbd>⇧</kbd><kbd>↵</kbd> on a song starts radio from it.</p>
    </Modal>
  )
}
