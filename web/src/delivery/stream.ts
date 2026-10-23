// The live status stream (D36) for one campaign, over EventSource.
import type { Snapshot } from './status'

type Handlers = {
  onStatus: (s: Snapshot) => void
  onEnd: () => void // the campaign is finished; nothing more will change
  onTrouble: (reconnecting: boolean) => void // the browser is reconnecting on its own
}

export function watchStatus(id: number, h: Handlers, Source: typeof EventSource = EventSource) {
  const es = new Source(`/api/campaigns/${id}/events`)
  es.addEventListener('status', (e) => {
    let s: Snapshot
    try {
      s = JSON.parse((e as MessageEvent).data)
    } catch {
      return
    }
    h.onTrouble(false)
    h.onStatus(s)
  })
  // Close at the end, or the browser would reconnect every few seconds.
  es.addEventListener('end', () => {
    es.close()
    h.onEnd()
  })
  es.onerror = () => h.onTrouble(true)
  return { close: () => es.close() }
}
