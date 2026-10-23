import { describe, expect, it } from 'vitest'
import { insertVariable, VARIABLES } from './variables'

describe('insertVariable', () => {
  it('inserts at the cursor and puts the cursor after it', () => {
    expect(insertVariable('Merhaba !', 8, 8, 'first_name')).toEqual({ text: 'Merhaba {{first_name}}!', cursor: 22 })
  })

  it('replaces a selection', () => {
    expect(insertVariable('Hi NAME!', 3, 7, 'first_name')).toEqual({ text: 'Hi {{first_name}}!', cursor: 17 })
  })

  it('offers only the allowed variables (D12)', () => {
    expect(VARIABLES).toEqual(['first_name', 'last_name', 'city'])
  })
})
