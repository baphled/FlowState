import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ContextToolGroup from './ContextToolGroup.vue'
import type { Message } from '@/types'

function makeToolMessage(
  id: string,
  toolName: string,
  content: string,
  toolInput: string = '{}',
): Message {
  return {
    id,
    role: 'tool_result',
    toolName,
    content,
    toolInput,
    timestamp: '2026-05-03T00:00:00Z',
  }
}

describe('ContextToolGroup', () => {
  const messages = [
    makeToolMessage('1', 'read', 'content 1', '{"filePath":"file1.ts"}'),
    makeToolMessage('2', 'read', 'content 2', '{"filePath":"file2.ts"}'),
  ]
  const toolCounts = { read: 2 }

  it('renders with correct data attributes', () => {
    const wrapper = mount(ContextToolGroup, {
      props: { messages, toolCounts },
    })
    expect(wrapper.attributes('data-component')).toBe('context-tool-group')
    expect(wrapper.attributes('data-testid')).toBe('context-tool-group')
  })

  it('shows count summary in header', () => {
    const wrapper = mount(ContextToolGroup, {
      props: { messages, toolCounts },
    })
    expect(wrapper.find('.summary').text()).toBe('2 files read')
  })

  it('starts collapsed by default', () => {
    const wrapper = mount(ContextToolGroup, {
      props: { messages, toolCounts },
    })
    expect(wrapper.attributes('data-open')).toBe('false')
    expect(wrapper.find('.group-content').exists()).toBe(false)
  })

  it('toggles expanded state when clicking header', async () => {
    const wrapper = mount(ContextToolGroup, {
      props: { messages, toolCounts },
    })

    await wrapper.find('.group-header').trigger('click')
    expect(wrapper.attributes('data-open')).toBe('true')
    expect(wrapper.find('.group-content').exists()).toBe(true)

    await wrapper.find('.group-header').trigger('click')
    expect(wrapper.attributes('data-open')).toBe('false')
    expect(wrapper.find('.group-content').exists()).toBe(false)
  })

  it('renders message entries when expanded', async () => {
    const wrapper = mount(ContextToolGroup, {
      props: { messages, toolCounts },
    })

    await wrapper.find('.group-header').trigger('click')
    const entries = wrapper.findAll('.entry')
    expect(entries).toHaveLength(2)
    expect(entries[0].find('.entry-heading').text()).toBe('read file1.ts')
    expect(entries[0].find('.entry-body').text()).toBe('content 1')
    expect(entries[1].find('.entry-heading').text()).toBe('read file2.ts')
    expect(entries[1].find('.entry-body').text()).toBe('content 2')
  })
})
