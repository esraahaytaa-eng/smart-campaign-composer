import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api'
import { createSaver } from './saver'

type Doc = { version: number; value: string }

function setup(start = 1) {
  let doc: Doc = { version: start, value: '' }
  const saver = createSaver<Doc>(
    () => doc.version,
    (d) => {
      doc = d
    },
  )
  return { saver, current: () => doc }
}

describe('createSaver', () => {
  // Autosave: two quick edits must not both send the same version.
  it('runs edits one after another with the latest version', async () => {
    const { saver, current } = setup()
    const seen: number[] = []
    const edit = (value: string) => async (version: number) => {
      seen.push(version)
      await new Promise((r) => setTimeout(r, 5))
      return { version: version + 1, value }
    }

    await Promise.all([saver.save(edit('a')), saver.save(edit('b'))])

    expect(seen).toEqual([1, 2])
    expect(current()).toEqual({ version: 3, value: 'b' })
    expect(saver.state.value).toBe('saved')
  })

  it('reports a change made elsewhere and stops saving', async () => {
    const { saver } = setup()
    const later = vi.fn(async (v: number) => ({ version: v + 1, value: 'x' }))

    await expect(saver.save(async () => Promise.reject(new ApiError('changed', 409)))).rejects.toThrow()
    await expect(saver.save(later)).rejects.toThrow()

    expect(saver.state.value).toBe('conflict')
    expect(later).not.toHaveBeenCalled()
  })

  it('keeps going after an ordinary error, and reports it', async () => {
    const { saver, current } = setup()

    await expect(saver.save(async () => Promise.reject(new ApiError('invalid', 422)))).rejects.toThrow('invalid')
    expect(saver.state.value).toBe('error')

    await saver.save(async (v) => ({ version: v + 1, value: 'ok' }))
    expect(current().value).toBe('ok')
    expect(saver.state.value).toBe('saved')
  })
})
