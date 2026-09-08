import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ChannelTestModal from '../ChannelTestModal.vue'

const { getAvailableModels } = vi.hoisted(() => ({
  getAvailableModels: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (!params) return key
        return `${key} ${Object.values(params).join(' ')}`
      }
    })
  }
})

function streamResponse(events: object[]) {
  const encoder = new TextEncoder()
  const chunks = events.map((event) => encoder.encode(`data: ${JSON.stringify(event)}\n\n`))
  let index = 0
  return {
    ok: true,
    status: 200,
    body: {
      getReader: () => ({
        read: vi.fn().mockImplementation(async () => {
          if (index >= chunks.length) return { done: true, value: undefined }
          return { done: false, value: chunks[index++] }
        })
      })
    }
  } as unknown as Response
}

function mountModal() {
  return mount(ChannelTestModal, {
    props: {
      show: false,
      account: {
        id: 42,
        name: 'Test channel',
        platform: 'openai',
        type: 'apikey',
        status: 'active'
      }
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Icon: true
      }
    }
  })
}

describe('ChannelTestModal', () => {
  beforeEach(() => {
    getAvailableModels.mockReset()
    getAvailableModels.mockResolvedValue([
      { id: 'z-model', display_name: 'Z model' },
      { id: 'a-model', display_name: 'A model' }
    ])
    global.fetch = vi.fn().mockResolvedValue(streamResponse([
      { type: 'test_start', model: 'a-model' },
      { type: 'content', text: 'ok' },
      { type: 'test_complete', success: true }
    ])) as any
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      value: { getItem: vi.fn(() => 'test-token') }
    })
  })

  it('loads and sorts all account models, then tests one model through the admin SSE endpoint', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.text()).toContain('A model')
    expect(wrapper.text()).toContain('Z model')
    const modelRows = wrapper.findAll('tbody tr')
    expect(modelRows[0].text()).toContain('A model')

    const testButton = modelRows[0].find('button')
    await testButton.trigger('click')
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [url, init] = (global.fetch as any).mock.calls[0]
    expect(url).toContain('/admin/accounts/42/test')
    expect(JSON.parse(init.body)).toEqual({ model_id: 'a-model', prompt: '' })
    expect(modelRows[0].text()).toContain('admin.accounts.channelTest.success')
  })

  it('runs a batch test for selected models and reports each result independently', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'one', display_name: 'One' },
      { id: 'two', display_name: 'Two' }
    ])
    global.fetch = vi.fn()
      .mockResolvedValueOnce(streamResponse([{ type: 'test_complete', success: true }]))
      .mockResolvedValueOnce(streamResponse([{ type: 'error', error: 'upstream failed' }])) as any

    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    const checkboxes = wrapper.findAll('tbody input[type="checkbox"]')
    await checkboxes[0].setValue(true)
    await checkboxes[1].setValue(true)
    const batchButton = wrapper.findAll('button').find((button) => button.text().includes('testSelected'))
    expect(batchButton).toBeTruthy()
    await batchButton!.trigger('click')
    await flushPromises()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('upstream failed')
    expect(wrapper.text()).toContain('admin.accounts.channelTest.success')
    expect(wrapper.text()).toContain('admin.accounts.channelTest.failed')
  })

  it('skips image and video models in batch tests but keeps explicit single-model testing', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-image-1' },
      { id: 'text-model' }
    ])
    global.fetch = vi.fn().mockResolvedValue(streamResponse([
      { type: 'test_complete', success: true }
    ])) as any

    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    const rows = wrapper.findAll('tbody tr')
    const mediaRow = rows.find((row) => row.text().includes('gpt-image-1'))!
    const textRow = rows.find((row) => row.text().includes('text-model'))!
    await mediaRow.find('input[type="checkbox"]').setValue(true)
    await textRow.find('input[type="checkbox"]').setValue(true)

    const batchButton = wrapper.findAll('button').find((button) => button.text().includes('testSelected'))
    await batchButton!.trigger('click')
    await flushPromises()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    expect(JSON.parse((global.fetch as any).mock.calls[0][1].body)).toEqual({ model_id: 'text-model', prompt: '' })
    expect(wrapper.text()).toContain('batchSkippedMedia')

    global.fetch = vi.fn().mockResolvedValue(streamResponse([
      { type: 'test_complete', success: true }
    ])) as any
    await mediaRow.find('button').trigger('click')
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    expect(JSON.parse((global.fetch as any).mock.calls[0][1].body)).toEqual({ model_id: 'gpt-image-1', prompt: '' })
  })
})
