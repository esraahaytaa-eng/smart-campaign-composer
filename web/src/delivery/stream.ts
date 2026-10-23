// The live status stream (D36) for one campaign, over EventSource.
import type { Snapshot } from './status'

type Handlers = {
  onStatus: (s: Snapshot) => void
  onEnd: () => void // the campaign is finished; nothing more will change
  onTrouble: (reconnecting: boolean) => void // the browser is reconnecting on its own
  onClosed: () => void // the browser gave up (e.g. 401, 404, 500 on reconnect)
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
  // A non-200 answer to a reconnect closes the source for good.
  es.onerror = () => (es.readyState === 2 ? h.onClosed() : h.onTrouble(true))
  return { close: () => es.close() }
}
