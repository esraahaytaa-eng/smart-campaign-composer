import type { Channel, Content } from './types'

// channelsLosingContent lists the channels a change would remove that already
// have content; the backend deletes it (D38), so the composer asks first.
export function channelsLosingContent(current: Channel[], next: Channel[], content: Content | null): Channel[] {
  return current.filter((c) => !next.includes(c) && content?.[c] !== undefined)
}
