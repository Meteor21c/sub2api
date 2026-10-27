import fs from 'node:fs/promises'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

// The published function contains no key or allowlist. Both values are set in
// EdgeOne separately; VIDEO_RELAY_SECRET must use the platform Secret type.
export async function renderEdgeScript(output) {
  const destination = path.resolve(output)
  if (destination === new URL('./edge.mjs', import.meta.url).pathname) throw new Error('Choose a new output path')
  const source = await fs.readFile(new URL('./edge.mjs', import.meta.url), 'utf8')
  const script = source.replace(/^export (?=(?:async )?function )/gm, '')
  const file = await fs.open(destination, 'wx', 0o600)
  try { await file.writeFile(script) } finally { await file.close() }
  return destination
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  if (process.argv.length !== 3) throw new Error('Usage: node render.mjs /absolute/new/path/edge.js')
  await renderEdgeScript(process.argv[2])
  console.log('EdgeOne script prepared; configure the key as a platform Secret before deployment.')
}
