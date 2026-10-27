import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import '../video-asset-status.js'

const assets = globalThis.MeteorVideoAssetStatus

test('asset preparation metadata survives direct and nested MCP response shapes', () => {
  const expected = {
    assetStage: 'asset_reviewing',
    assetProgress: { total: 3, active: 1, processing: 1, failed: 0 },
  }
  const raw = { status: 'pending', stage: 'asset_reviewing', asset_progress: { total: 3, active: 1, processing: 1, failed: 0 } }
  assert.deepEqual(assets.read(raw), expected)
  assert.deepEqual(assets.read({ status: 'IN_PROGRESS', data: { raw } }), expected)
  assert.deepEqual(assets.read({ structuredContent: { raw } }), expected)
  assert.deepEqual(assets.read({ ...expected, apiKey: 'do-not-copy' }), expected)
})

test('invalid stages and inconsistent counts never become display or history metadata', () => {
  assert.deepEqual(assets.read({ stage: '<img onerror=alert(1)>', asset_progress: { total: 2, active: 3 } }), {})
  for (const value of [null, [], { total: -1 }, { total: '2' }, { total: 1, active: NaN }, { total: 2, active: 1, processing: 1, failed: 1 }, { total: 1000000 }]) {
    assert.equal(assets.progress(value), null)
  }
  assert.deepEqual(assets.progress({ total: 0 }), { total: 0, active: 0, processing: 0, failed: 0 })
  assert.deepEqual(assets.read({ stage: 'future-stage' }), {})
  assert.deepEqual(assets.describe({ status: 'pending', assetStage: 'future-stage' }), { label: '处理中', detail: '' })
})

test('preparation and uncertain submission stay pending while known failures remain failures', () => {
  for (const stage of ['asset_pending', 'asset_reviewing', 'video_submitting', 'video_generating']) {
    assert.equal(assets.taskStatus('pending', '', stage), 'pending')
  }
  assert.equal(assets.taskStatus('error', '', 'submission_unknown'), 'pending')
  assert.equal(assets.taskStatus('pending', '', 'failed'), 'failure')
  assert.equal(assets.taskStatus('failed'), 'failure')
  assert.equal(assets.taskStatus('done', '', 'completed'), 'success')
  assert.equal(assets.taskStatus('running', 'https://example.test/result.mp4'), 'success')
  assert.match(assets.describe({ status: 'pending', assetStage: 'submission_unknown' }).detail, /请勿重复提交/)
})

test('review status shows readiness and failure refund guidance without claiming completion', () => {
  const reviewing = assets.describe({
    status: 'pending', assetStage: 'asset_reviewing',
    assetProgress: { total: 2, active: 1, processing: 1, failed: 0 },
  })
  assert.equal(reviewing.label, '素材审核中')
  assert.match(reviewing.detail, /已就绪 1\/2/)
  assert.match(reviewing.detail, /未通过会退回预扣/)
  const failed = assets.describe({ status: 'failure', assetStage: 'failed' })
  assert.equal(failed.label, '失败')
  assert.match(failed.detail, /预扣费用将退回/)
  assert.equal(assets.describe({ status: 'success', assetStage: 'completed' }).label, '已完成')
})

test('preparation and uncertain submissions reduce polling without changing normal video cadence', () => {
  assert.equal(assets.pollInterval({ assetStage: 'asset_pending' }), 10000)
  assert.equal(assets.pollInterval({ assetStage: 'asset_reviewing' }), 10000)
  assert.equal(assets.pollInterval({ assetStage: 'submission_unknown' }), 30000)
  assert.equal(assets.pollInterval({ assetStage: 'video_generating' }), 5000)
  assert.equal(assets.pollInterval({ assetStage: 'future-stage' }), 5000)
})

test('automatic asset UI loads before history and only shows its hint for Seedance', async () => {
  const html = await readFile(new URL('../index.html', import.meta.url), 'utf8')
  const app = await readFile(new URL('../app.js', import.meta.url), 'utf8')
  assert.ok(html.indexOf('video-asset-status.js') < html.indexOf('media-history.js'))
  assert.ok(html.indexOf('video-asset-status.js') < html.indexOf('app.js'))
  assert.match(html, /id="video-asset-hint" hidden/)
  assert.match(app, /\$\("video-asset-hint"\)\.hidden = !model\.startsWith\("doubao-seedance-"\)/)
  assert.match(app, /MeteorVideoAssetStatus\.read\(data\)/)
  assert.match(app, /detail\.textContent = preparation\.detail/)
  assert.match(app, /sleep\(MeteorVideoAssetStatus\.pollInterval\(info\)\)/)
})
