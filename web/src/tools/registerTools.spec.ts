import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import BashTool from '@/components/tools/BashTool.vue'
import EditTool from '@/components/tools/EditTool.vue'
import GenericTool from '@/components/tools/GenericTool.vue'
import GlobTool from '@/components/tools/GlobTool.vue'
import GrepTool from '@/components/tools/GrepTool.vue'
import ReadTool from '@/components/tools/ReadTool.vue'
import RecallSearchTool from '@/components/tools/RecallSearchTool.vue'
import TodoTool from '@/components/tools/TodoTool.vue'
import WriteTool from '@/components/tools/WriteTool.vue'
import { getToolComponent } from './toolRegistry'
import { registerTools } from './registerTools'

const CopyButton = {
  template: '<span data-testid="copy-btn" />',
}

const ToolBubble = {
  props: ['toolName', 'title', 'subtitle', 'status'],
  template: `
    <div data-testid="tool-bubble" data-component="tool" :data-tool="toolName" :data-status="status">
      <span data-testid="tool-title">{{ title }}</span>
      <span v-if="subtitle" data-testid="tool-subtitle">{{ subtitle }}</span>
      <slot />
    </div>
  `,
}

describe('registerTools', () => {
  it('registers the expected components and aliases', () => {
    registerTools()

    expect(getToolComponent('bash')).toBe(BashTool)
    expect(getToolComponent('read')).toBe(ReadTool)
    expect(getToolComponent('write')).toBe(WriteTool)
    expect(getToolComponent('edit')).toBe(EditTool)
    expect(getToolComponent('multiedit')).toBe(EditTool)
    expect(getToolComponent('apply_patch')).toBe(EditTool)
    expect(getToolComponent('glob')).toBe(GlobTool)
    expect(getToolComponent('list')).toBe(GlobTool)
    expect(getToolComponent('grep')).toBe(GrepTool)
    expect(getToolComponent('skill_load')).toBe(GenericTool)
    expect(getToolComponent('webfetch')).toBe(GenericTool)
    expect(getToolComponent('websearch')).toBe(GenericTool)
    expect(getToolComponent('task')).toBe(GenericTool)
    expect(getToolComponent('todowrite')).toBe(TodoTool)
    expect(getToolComponent('search_context')).toBe(RecallSearchTool)
    expect(getToolComponent('chain_search_context')).toBe(RecallSearchTool)
    expect(getToolComponent('get_messages')).toBe(RecallSearchTool)
    expect(getToolComponent('chain_get_messages')).toBe(RecallSearchTool)
    expect(getToolComponent('summarize_context')).toBe(GenericTool)
  })

  it('returns mountable components after registration', () => {
    registerTools()
    const component = getToolComponent('bash')

    if (!component) {
      throw new Error('bash component was not registered')
    }

    const wrapper = mount(component, {
      props: {
        toolName: 'bash',
        heading: 'pwd',
        body: '/tmp',
      },
      global: {
        stubs: {
          CopyButton,
          ToolBubble,
        },
      },
    })

    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-component')).toBe('tool')
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-tool')).toBe('bash')
  })
})
