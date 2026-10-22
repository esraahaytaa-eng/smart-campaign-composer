const MAX = 60

// briefTitle is how lists show a campaign (D43): the brief's first ~60
// characters, cut at a word, with "…" when shortened.
export function briefTitle(brief: string): string {
  const chars = [...brief.trim().replace(/\s+/g, ' ')]
  if (chars.length <= MAX) return chars.join('')
  let cut = chars.slice(0, MAX - 1)
  if (chars[MAX - 1] !== ' ') {
    const space = cut.lastIndexOf(' ')
    if (space > 0) cut = cut.slice(0, space)
  }
  return cut.join('').trimEnd() + '…'
}
