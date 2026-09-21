import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppHeader.vue')
const componentSource = readFileSync(componentPath, 'utf8')

describe('AppHeader promotion entries', () => {
  it('shows the shop beside MeteorAgent for signed-in users', () => {
    expect(componentSource).toContain('<MeteorAgentPromo variant="header" />')
    expect(componentSource).toContain('<ShopPromo variant="header" />')
  })
})
