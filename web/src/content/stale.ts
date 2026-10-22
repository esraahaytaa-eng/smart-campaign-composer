// D40: after the brief is re-interpreted, the existing messages may no longer
// match it. The flag lives in this browser tab (sessionStorage) until the
// messages are saved or rewritten; without storage, only the page that
// re-interpreted shows the warning.
const key = (id: number) => `scc.staleContent.${id}`

export function markContentStale(id: number) {
  try {
    sessionStorage.setItem(key(id), '1')
  } catch {
    /* storage unavailable */
  }
}

export function markContentFresh(id: number) {
  try {
    sessionStorage.removeItem(key(id))
  } catch {
    /* storage unavailable */
  }
}

export function contentWasStale(id: number): boolean {
  try {
    return sessionStorage.getItem(key(id)) === '1'
  } catch {
    return false
  }
}
