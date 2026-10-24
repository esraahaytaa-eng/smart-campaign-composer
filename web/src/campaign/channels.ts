import type { Channel, Variants } from './types'

// channelsKeepingText lists the channels a change would remove that already
// have text in some language; the text is kept and comes back with the
// channel (D67), so the composer says so instead of asking.
export function channelsKeepingText(current: Channel[], next: Channel[], content: Variants | null): Channel[] {
  return current.filter((c) => !next.includes(c) && Object.values(content ?? {}).some((v) => v[c] !== undefined))
}
