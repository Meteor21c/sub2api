import crypto from 'node:crypto'
import fs from 'node:fs/promises'
import path from 'node:path'

const DAY = 86400000
const REVIEW_TIMEOUT = 30 * 60000
const MAX_STATE = 16 * 1024 * 1024
const LIVE = new Set(['asset_pending', 'asset_reviewing', 'video_submitting', 'video_generating', 'submission_unknown'])
const WAITING = new Set(['asset_pending', 'asset_reviewing'])
const MEDIA = { image_url: 'Image', video_url: 'Video', audio_url: 'Audio' }
const AAD = Buffer.from('meteor-fzy-asset-queue:v1')
const validProviderID = value => typeof value === 'string' && /^[A-Za-z0-9_-]{1,256}$/.test(value)
const digest = value => crypto.createHash('sha256').update(value).digest('hex')
const validOwner = owner => typeof owner === 'string' && /^user-[1-9]\d{0,18}$/.test(owner)
const scopeFor = (owner, token) => digest(`${owner}\0${token}`)
const canonical = value => JSON.stringify(value, (_, item) => item && !Array.isArray(item) && typeof item === 'object'
  ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b))) : item)

/** A single-process, low-concurrency queue. dataDir must be a dedicated directory. */
export async function createFzyAssetQueue(options = {}) {
  const { dataDir, secret, baseURL, fingerprint, log = () => {} } = options
  const fetchImpl = options.fetchImpl || globalThis.fetch
  const now = options.now || Date.now
  const intervalMs = options.intervalMs ?? 5000
  const retentionMs = options.retentionMs ?? 7 * DAY
  if (!Number.isFinite(intervalMs) || intervalMs < 10 || !Number.isFinite(retentionMs) || retentionMs <= 0) throw new Error('Invalid FZY asset queue interval')
  if (typeof secret !== 'string' || !secret.trim()) throw new Error('FZY asset queue requires MCP_ASSET_SIGNING_SECRET')
  if (!dataDir || !baseURL || typeof fetchImpl !== 'function') throw new Error('FZY asset queue configuration is incomplete')
  const base = new URL(baseURL)
  if (!['http:', 'https:'].includes(base.protocol)) throw new Error('Invalid FZY provider URL')
  const root = String(baseURL).replace(/\/$/, '')
  const key = crypto.createHash('sha256').update(AAD).update(secret).digest()
  const file = path.join(dataDir, 'encrypted-state.json')
  await fs.mkdir(dataDir, { recursive: true, mode: 0o700 })
  let state = { version: 1, groups: {}, assets: {}, assetIndex: {}, jobs: {}, idempotency: {}, cleanup: { lastScanAt: null, pending: [] } }
  try {
    const bytes = await fs.readFile(file)
    if (bytes.length > MAX_STATE * 1.5) throw new Error('Queue state exceeds capacity')
    const envelope = JSON.parse(bytes.toString('utf8'))
    if (envelope.version !== 1) throw new Error('Unsupported queue state version')
    const decipher = crypto.createDecipheriv('aes-256-gcm', key, Buffer.from(envelope.iv, 'base64url'))
    decipher.setAAD(AAD)
    decipher.setAuthTag(Buffer.from(envelope.tag, 'base64url'))
    const plaintext = Buffer.concat([decipher.update(Buffer.from(envelope.data, 'base64url')), decipher.final()])
    if (plaintext.length > MAX_STATE) throw new Error('Queue state exceeds capacity')
    const restored = JSON.parse(plaintext.toString('utf8'))
    if (restored.version !== 1 || !restored.jobs || !restored.assets || !restored.groups || !restored.assetIndex || !restored.idempotency || !restored.cleanup) throw new Error('Invalid queue state')
    state = restored
  } catch (error) {
    if (error.code !== 'ENOENT') throw new Error('Cannot decrypt or read FZY asset queue state; original state was preserved', { cause: error })
  }

  // Commit only after the encrypted rename succeeds. An enqueue rejected due to
  // disk/capacity errors must never leave an executable job in memory.
  let writes = Promise.resolve()
  async function mutate(change, onWriteFailure) {
    const operation = writes.then(async () => {
      const draft = structuredClone(state)
      const result = change(draft)
      try {
        const plaintext = Buffer.from(JSON.stringify(draft))
        if (plaintext.length > MAX_STATE) throw new Error('FZY asset queue storage capacity has been reached')
        const iv = crypto.randomBytes(12)
        const cipher = crypto.createCipheriv('aes-256-gcm', key, iv)
        cipher.setAAD(AAD)
        const encrypted = Buffer.concat([cipher.update(plaintext), cipher.final()])
        const envelope = JSON.stringify({ version: 1, iv: iv.toString('base64url'), tag: cipher.getAuthTag().toString('base64url'), data: encrypted.toString('base64url') })
        const temp = `${file}.${crypto.randomUUID()}.tmp`
        let handle
        try {
          handle = await fs.open(temp, 'wx', 0o600)
          await handle.writeFile(envelope)
          await handle.sync()
          await handle.close()
          handle = null
          await fs.rename(temp, file)
        } catch (error) {
          if (handle) await handle.close().catch(() => {})
          // Exactly one freshly created, known temporary path is eligible here.
          await fs.unlink(temp).catch(() => {})
          throw error
        }
        state = draft
        return result
      } catch (error) {
        if (onWriteFailure) {
          const fallback = structuredClone(state)
          onWriteFailure(fallback)
          state = fallback
        }
        throw error
      }
    })
    writes = operation.catch(() => {})
    return operation
  }

  function progress(job, snapshot = state) {
    if (job.finalProgress) return { ...job.finalProgress }
    const rows = job.assetIDs.map(id => snapshot.assets[id])
    return { total: rows.length, active: rows.filter(a => a?.status === 'active').length,
      processing: rows.filter(a => a?.status === 'new' || a?.status === 'processing').length,
      failed: rows.filter(a => !a || a.status === 'failed').length }
  }
  function publicJob(job) {
    return { id: job.id, model: job.model, stage: job.stage, assetProgress: progress(job),
      ...(job.upstreamTaskID ? { upstreamTaskID: job.upstreamTaskID } : {}), ...(job.error ? { error: job.error } : {}) }
  }
  function authorised(job, token, owner) {
    return !!job && validOwner(owner) && typeof token === 'string' && job.scope === scopeFor(owner, token)
  }
  function lookup(id, token, owner) {
    const job = state.jobs[id]
    return authorised(job, token, owner) ? publicJob(job) : null
  }
  function finish(draft, job, stage, error) {
    if (job.stage === 'completed' || job.stage === 'failed') return
    job.finalProgress = progress(job, draft)
    for (const id of job.assetIDs) if (draft.assets[id]) draft.assets[id].lastUsedAt = now()
    job.stage = stage
    job.terminalAt = now()
    if (error) job.error = error
    delete job.requestBody
    delete job.refs
    delete job.submissionKey
    job.assetIDs = []
  }
  function trimHistory(draft) {
    for (const job of Object.values(draft.jobs)) {
      if ((job.stage === 'completed' || job.stage === 'failed') && job.terminalAt < now() - 30 * DAY) {
        delete draft.jobs[job.id]
        if (job.dedupeKey && draft.idempotency[job.dedupeKey] === job.id) delete draft.idempotency[job.dedupeKey]
      }
    }
  }

  async function enqueue({ token, owner, model, requestBody, idempotencyKey } = {}) {
    if (!validOwner(owner)) throw new Error('A trusted user identity is required for FZY assets')
    if (typeof token !== 'string' || !token || token.length > 8192) throw new Error('Invalid provider token')
    if (typeof model !== 'string' || !model || model.length > 160) throw new Error('Invalid video model')
    const bodyJSON = JSON.stringify(requestBody)
    if (!bodyJSON || Buffer.byteLength(bodyJSON) > 65536) throw new Error('FZY asset request exceeds capacity')
    const body = JSON.parse(bodyJSON)
    if (!body || typeof body !== 'object' || !Array.isArray(body.content)) throw new Error('Video content is required')
    const scope = scopeFor(owner, token)
    const requestHash = digest(canonical({ model, body }))
    if (idempotencyKey !== undefined && (typeof idempotencyKey !== 'string' || !idempotencyKey || idempotencyKey.length > 512)) throw new Error('Invalid reservation idempotency key')
    const dedupeKey = idempotencyKey ? digest(`${scope}\0${idempotencyKey}`) : null
    const media = body.content.map((item, index) => ({ item, index, type: MEDIA[item?.type] })).filter(row => row.type)
    if (media.length > 11) throw new Error('At most 11 media inputs are allowed')
    const refs = []
    for (const { item, index, type } of media) {
      const url = item[item.type]?.url
      let parsed
      try { parsed = new URL(url) } catch { throw new Error('Invalid asset source URL') }
      if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) throw new Error('Asset source URLs must use HTTP/HTTPS')
      const print = fingerprint ? await fingerprint(url, type) : digest(`${type}\0${url}`)
      if (typeof print !== 'string' || !print || print.length > 1024) throw new Error('Invalid asset fingerprint')
      refs.push({ index, field: item.type, type, url, baseKey: digest(`${scope}\0${type}\0${print}`) })
    }
    const id = await mutate(draft => {
      if (dedupeKey && draft.idempotency[dedupeKey]) {
        const previous = draft.jobs[draft.idempotency[dedupeKey]]
        if (previous) {
          if (previous.requestHash !== requestHash) throw new Error('The reservation idempotency key was already used for another request')
          return previous.id
        }
      }
      trimHistory(draft)
      const pending = Object.values(draft.jobs).filter(job => LIVE.has(job.stage))
      if (pending.length >= 200 || pending.filter(job => job.scope === scope).length >= 20 || Object.keys(draft.jobs).length >= 10000) throw new Error('FZY asset queue is busy; please try again later')
      if (draft.groups[scope]?.deleting) throw new Error('This material library is being cleaned; please retry shortly')
      if (!draft.groups[scope]) {
        if (Object.keys(draft.groups).length >= 4000) throw new Error('FZY asset library capacity has been reached')
        draft.groups[scope] = { scope, owner, token, createdAt: now(), lastUsedAt: now(), nextAttemptAt: 0 }
      }
      draft.groups[scope].lastUsedAt = now()
      const jobRefs = []
      for (const ref of refs) {
        let asset = draft.assets[draft.assetIndex[ref.baseKey]]
        if (!asset || !['new', 'processing', 'active'].includes(asset.status)) {
          if (Object.keys(draft.assets).length >= 4000) throw new Error('FZY asset library capacity has been reached')
          const assetID = `material-${crypto.randomUUID()}`
          asset = { id: assetID, scope, baseKey: ref.baseKey, type: ref.type, url: ref.url, status: 'new', createdAt: now(), lastUsedAt: now(), nextAttemptAt: 0 }
          draft.assets[assetID] = asset
          draft.assetIndex[ref.baseKey] = assetID
        }
        asset.lastUsedAt = now()
        jobRefs.push({ index: ref.index, field: ref.field, assetID: asset.id })
      }
      const jobID = `meteorasset-${crypto.randomUUID()}`
      draft.jobs[jobID] = { id: jobID, scope, owner, model, requestBody: body, requestHash, dedupeKey,
        submissionKey: `meteor-fzy-video-${jobID}`, refs: jobRefs, assetIDs: [...new Set(jobRefs.map(ref => ref.assetID))],
        stage: 'asset_pending', createdAt: now(), lastWorkAt: 0 }
      if (dedupeKey) draft.idempotency[dedupeKey] = jobID
      return jobID
    })
    return publicJob(state.jobs[id])
  }

  function message(result, token) {
    const value = result.payload?.error?.message || result.payload?.msg || result.payload?.message || `HTTP ${result.status}`
    return String(value).split(token).join('[redacted]').slice(0, 400)
  }
  async function request(endpoint, group, body, method = 'POST', idempotencyKey) {
    const response = await fetchImpl(`${root}${endpoint}`, { method, headers: {
      Authorization: `Bearer ${group.token}`, Accept: 'application/json', 'Content-Type': 'application/json',
      ...(group.groupId ? { group_id: String(group.groupId) } : {}), ...(idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {}),
    }, ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(20000) })
    const maximum = 1024 * 1024
    let text
    if (Number(response.headers?.get('content-length')) > maximum) {
      await response.body?.cancel().catch(() => {})
      throw new Error('FZY control response exceeds capacity')
    }
    if (response.body?.getReader) {
      const reader = response.body.getReader()
      const chunks = []
      let length = 0
      for (;;) {
        const part = await reader.read()
        if (part.done) break
        length += part.value.byteLength
        if (length > maximum) { await reader.cancel().catch(() => {}); throw new Error('FZY control response exceeds capacity') }
        chunks.push(part.value)
      }
      text = Buffer.concat(chunks, length).toString('utf8')
    } else {
      text = await response.text()
      if (Buffer.byteLength(text) > maximum) throw new Error('FZY control response exceeds capacity')
    }
    let payload
    try { payload = JSON.parse(text) } catch { payload = null }
    const code = Number(payload?.code)
    const status = response.status >= 400 ? response.status : code >= 400 ? code : response.status
    return { status, ok: status >= 200 && status < 300, payload, data: payload?.data || payload }
  }
  function safeLog(event, fields) { try { log(event, fields) } catch { /* diagnostics never affect billing */ } }
  function classifyStatus(value) {
    const status = String(value || '').toLowerCase()
    return status === 'active' ? 'active' : status === 'failed' ? 'failed' : 'processing'
  }
  async function reviewOperation(job, asset) {
    const group = state.groups[job.scope]
    try {
      if (!group.groupId) {
        const result = await request('/v3/asset-groups', group, { name: `meteor-${job.scope.slice(0, 20)}`, description: 'User-isolated video reference materials' }, 'POST', `meteor-fzy-group-${job.scope}`)
        if (!result.ok) throw Object.assign(new Error(message(result, group.token)), { definitive: result.status >= 400 && result.status < 500 })
        if (!validProviderID(result.data?.groupId)) throw Object.assign(new Error('Invalid provider group ID'), { definitive: true })
        await mutate(draft => { draft.groups[job.scope].groupId = result.data.groupId; draft.groups[job.scope].nextAttemptAt = 0 })
        return
      }
      const creating = !asset.providerAssetID
      const result = creating
        ? await request('/v3/assets', group, { url: asset.url, name: `meteor-${asset.id}`, assetType: asset.type }, 'POST', `meteor-fzy-upload-${asset.id}`)
        : await request('/v3/assets/detail', group, { assetId: asset.providerAssetID })
      if (!result.ok || !result.data) throw Object.assign(new Error(message(result, group.token)), { definitive: result.status >= 400 && result.status < 500 })
      if ((creating && !validProviderID(result.data.id)) || (result.data.id !== undefined && !validProviderID(result.data.id))) throw Object.assign(new Error('Invalid provider asset ID'), { definitive: true })
      await mutate(draft => {
        const row = draft.assets[asset.id]
        if (creating) row.providerAssetID = result.data.id
        row.status = classifyStatus(result.data.status)
        row.nextAttemptAt = now() + 15000
        if (row.status === 'failed') row.error = '素材审核未通过，请更换素材后重新提交'
      })
    } catch (error) {
      await mutate(draft => {
        if (!group.groupId) {
          draft.groups[job.scope].nextAttemptAt = now() + 30000
          if (error.definitive) for (const row of Object.values(draft.jobs)) if (row.scope === job.scope && WAITING.has(row.stage)) finish(draft, row, 'failed', '上游素材组创建失败，请检查素材库权限')
        } else {
          const row = draft.assets[asset.id]
          row.nextAttemptAt = now() + 30000
          if (error.definitive) { row.status = 'failed'; row.error = '上游素材入库或审核失败，请更换素材后重试' }
        }
      })
      safeLog('fzy_asset_request_failed', { job_id: job.id, definitive: !!error.definitive })
    }
  }
  async function submit(job) {
    const group = state.groups[job.scope]
    const body = structuredClone(job.requestBody)
    for (const ref of job.refs) body.content[ref.index][ref.field].url = `asset://${state.assets[ref.assetID].providerAssetID}`
    await mutate(draft => { draft.jobs[job.id].stage = 'video_submitting' })
    let result
    try { result = await request('/v3/video/tasks', group, body, 'POST', job.submissionKey) } catch {
      result = { status: 0, ok: false, payload: null, data: null }
    }
    const markUnknown = draft => {
      draft.jobs[job.id].stage = 'submission_unknown'
      draft.jobs[job.id].error = '上游视频提交结果待核实，请勿重复提交；系统不会自动重发'
    }
    try { await mutate(draft => {
      const row = draft.jobs[job.id]
      if (result.ok && validProviderID(result.data?.id)) {
        row.stage = 'video_generating'
        row.upstreamTaskID = result.data.id
        delete row.requestBody
        delete row.refs
      } else if (result.status >= 400 && result.status < 500 && ![408, 409].includes(result.status)) {
        finish(draft, row, 'failed', `视频任务被上游拒绝：${message(result, group.token)}`)
      } else {
        markUnknown(draft)
      }
    }, markUnknown) } catch {
      // Disk failure after the paid POST is not a failed video. The serialized
      // writer installs the conservative RAM state under the same mutex; the
      // older durable submitting state also recovers as unknown on restart.
      safeLog('fzy_asset_video_result_persistence_failed', { job_id: job.id })
      await mutate(markUnknown).catch(() => {})
    }
  }

  let running = false
  let operationDone = Promise.resolve()
  async function tick() {
    if (running) return false
    running = true
    let release
    operationDone = new Promise(resolve => { release = resolve })
    try {
      const expired = Object.values(state.jobs).some(job => WAITING.has(job.stage) && (now() - job.createdAt >= REVIEW_TIMEOUT || job.assetIDs.some(id => state.assets[id]?.status === 'failed')))
      if (expired) await mutate(draft => {
        for (const job of Object.values(draft.jobs)) if (WAITING.has(job.stage)) {
          const failed = job.assetIDs.map(id => draft.assets[id]).find(asset => !asset || asset.status === 'failed')
          if (failed || job.assetIDs.some(id => !draft.assets[id])) finish(draft, job, 'failed', failed?.error || '素材入库失败')
          else if (now() - job.createdAt >= REVIEW_TIMEOUT) finish(draft, job, 'failed', '素材入库或审核超时，请稍后重试')
        }
      })
      const jobs = Object.values(state.jobs).filter(job => WAITING.has(job.stage)).sort((a, b) => a.lastWorkAt - b.lastWorkAt || a.createdAt - b.createdAt)
      for (const job of jobs) {
        const assets = job.assetIDs.map(id => state.assets[id])
        if (assets.every(asset => asset?.status === 'active')) {
          await mutate(draft => { draft.jobs[job.id].lastWorkAt = now() })
          await submit(state.jobs[job.id])
          return true
        }
        const group = state.groups[job.scope]
        const asset = assets.find(row => row && ['new', 'processing'].includes(row.status) && row.nextAttemptAt <= now())
        if ((!group.groupId && group.nextAttemptAt > now()) || !asset) continue
        await mutate(draft => { const row = draft.jobs[job.id]; row.lastWorkAt = now(); row.stage = 'asset_reviewing' })
        await reviewOperation(state.jobs[job.id], state.assets[asset.id])
        return true
      }
      return false
    } finally { running = false; release() }
  }
  async function observeTerminal(id, token, owner, status) {
    const job = state.jobs[id]
    if (!authorised(job, token, owner)) return null
    const terminal = ['done', 'completed', 'success', 'succeeded'].includes(status) ? 'completed'
      : ['failed', 'cancelled', 'canceled', 'expired'].includes(status) ? 'failed' : null
    if (!terminal || job.stage !== 'video_generating') return publicJob(job)
    await mutate(draft => {
      if (draft.jobs[id].stage === 'video_generating') finish(draft, draft.jobs[id], terminal, terminal === 'failed' ? '上游视频生成失败' : undefined)
    })
    return publicJob(state.jobs[id])
  }

  function leased(snapshot, assetID) {
    return Object.values(snapshot.jobs).some(job => LIVE.has(job.stage) && job.assetIDs.includes(assetID))
  }
  async function cleanupEmptyGroup() {
    const occupied = new Set(Object.values(state.assets).map(asset => asset.scope))
    for (const job of Object.values(state.jobs)) if (LIVE.has(job.stage)) occupied.add(job.scope)
    const group = Object.values(state.groups).find(row => !occupied.has(row.scope) &&
      (row.lastUsedAt || row.createdAt) <= now() - retentionMs && (row.nextDeleteAt || 0) <= now())
    if (!group) return { deleted: 0 }
    const reserved = await mutate(draft => {
      // enqueue can be ahead of this operation in the writer queue. Recheck
      // within the same transaction that installs the deletion reservation.
      if (!draft.groups[group.scope] || Object.values(draft.assets).some(asset => asset.scope === group.scope) ||
        Object.values(draft.jobs).some(job => job.scope === group.scope && LIVE.has(job.stage))) return false
      draft.groups[group.scope].deleting = true
      return true
    })
    if (!reserved) return { deleted: 0 }
    let deleted = !group.groupId
    let rejected = false
    if (group.groupId) {
      try {
        const result = await request(`/v3/asset-groups/${encodeURIComponent(group.groupId)}`, group, undefined, 'DELETE')
        // DELETE is idempotent: after a crash or failed final persistence the
        // provider can legitimately report that the group is already absent.
        deleted = result.ok || result.status === 404
        rejected = result.status >= 400 && result.status < 500 && ![408, 409].includes(result.status)
      } catch { /* retry without exposing credentials */ }
    }
    await mutate(draft => {
      if (deleted) delete draft.groups[group.scope]
      else {
        // A timeout may have deleted the group. Keep it quarantined until an
        // idempotent retry confirms absence; do not enqueue into a stale group.
        const uncertain = !rejected || !!group.deleting
        draft.groups[group.scope].deleting = uncertain
        draft.groups[group.scope].nextDeleteAt = now() + (uncertain ? 30000 : 3600000)
      }
    })
    if (deleted) safeLog('fzy_asset_empty_scope_removed', { provider_group_deleted: !!group.groupId })
    return { deleted: 0, groupsDeleted: deleted && group.groupId ? 1 : 0, scopesRemoved: deleted ? 1 : 0 }
  }
  async function cleanup({ force = false } = {}) {
    if (running) return { deleted: 0, skipped: true }
    running = true
    let release
    operationDone = new Promise(resolve => { release = resolve })
    try {
      if (force || state.cleanup.lastScanAt === null || now() - state.cleanup.lastScanAt >= DAY) await mutate(draft => {
        draft.cleanup.lastScanAt = now()
        draft.cleanup.pending = Object.values(draft.assets).filter(asset => asset.lastUsedAt <= now() - retentionMs && !leased(draft, asset.id)).sort((a, b) => a.lastUsedAt - b.lastUsedAt).map(asset => asset.id)
        trimHistory(draft)
      })
      const id = state.cleanup.pending.find(key => state.assets[key] && !leased(state, key) && state.assets[key].lastUsedAt <= now() - retentionMs && (state.assets[key].nextDeleteAt || 0) <= now())
      if (!id) return await cleanupEmptyGroup()
      const asset = state.assets[id]
      // Reserve before awaiting DELETE. Concurrent enqueue must create a new
      // asset rather than lease a material currently being deleted.
      const reserved = await mutate(draft => {
        const row = draft.assets[id]
        if (!row || row.lastUsedAt > now() - retentionMs || leased(draft, id)) return false
        row.status = 'deleting'
        return true
      })
      if (!reserved) return { deleted: 0 }
      let deleted = !asset.providerAssetID
      let rejected = false
      if (asset.providerAssetID) {
        try {
          const result = await request(`/v3/assets/${encodeURIComponent(asset.providerAssetID)}`, state.groups[asset.scope], undefined, 'DELETE')
          deleted = result.ok || result.status === 404
          rejected = result.status >= 400 && result.status < 500 && ![408, 409].includes(result.status)
        } catch { /* retain credentials and material for a later retry */ }
      }
      await mutate(draft => {
        if (deleted) {
          if (draft.assetIndex[asset.baseKey] === id) delete draft.assetIndex[asset.baseKey]
          delete draft.assets[id]
          draft.cleanup.pending = draft.cleanup.pending.filter(key => key !== id)
        } else {
          const uncertain = !rejected || asset.status === 'deleting'
          draft.assets[id].status = uncertain ? 'deleting' : asset.status
          draft.assets[id].nextDeleteAt = now() + (uncertain ? 30000 : 3600000)
        }
      })
      return { deleted: deleted ? 1 : 0 }
    } finally { running = false; release() }
  }

  // A crash after the submitting write may have occurred after the provider
  // accepted the paid task. Recover conservatively and never send it again.
  if (Object.values(state.jobs).some(job => job.stage === 'video_submitting')) await mutate(draft => {
    for (const job of Object.values(draft.jobs)) if (job.stage === 'video_submitting') {
      job.stage = 'submission_unknown'
      job.error = '服务重启前的视频提交结果待核实，请勿重复提交'
    }
  })
  let timer = null
  let pulsing = false
  function start() {
    if (timer) return
    timer = setInterval(async () => {
      if (pulsing) return
      pulsing = true
      try { await tick(); if (timer) await cleanup() } catch { safeLog('fzy_asset_queue_tick_failed', {}) } finally { pulsing = false }
    }, intervalMs)
    timer.unref()
  }
  async function stop() { if (timer) clearInterval(timer); timer = null; await operationDone; await writes }
  return { enqueue, lookup, tick, observeTerminal, cleanup, start, stop }
}
