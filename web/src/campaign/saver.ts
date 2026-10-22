import { readonly, ref } from 'vue'
import { ApiError } from '../api'

export type SaveState = 'idle' | 'saving' | 'saved' | 'error' | 'conflict'

// createSaver runs autosaves one at a time, each with the version the previous
// one returned, so quick edits never collide on the version check (D10). A 409
// means someone else changed the draft: saving stops until it is reloaded.
export function createSaver<T extends { version: number }>(currentVersion: () => number, apply: (doc: T) => void) {
  const state = ref<SaveState>('idle')
  let queue: Promise<unknown> = Promise.resolve()

  function save(op: (version: number) => Promise<T>): Promise<T> {
    const run = queue.then(async () => {
      if (state.value === 'conflict') throw new ApiError('This draft was changed elsewhere. Reload to see the latest version.', 409)
      state.value = 'saving'
      try {
        const doc = await op(currentVersion())
        apply(doc)
        state.value = 'saved'
        return doc
      } catch (err) {
        state.value = err instanceof ApiError && err.status === 409 ? 'conflict' : 'error'
        throw err
      }
    })
    queue = run.catch(() => {})
    return run
  }

  return { state: readonly(state), save }
}
