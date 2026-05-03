import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { defineComponent, h } from 'vue'
import MessageBubble from './MessageBubble.vue'
import type { Message } from '@/types'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: defineComponent({ render: () => h('div') }) },
      {
        path: '/agents/:id',
        name: 'agent-info',
        component: defineComponent({ render: () => h('div') }),
      },
    ],
  })
}

function mountWithRouter(message: Message) {
  const router = makeRouter()
  return mount(MessageBubble, {
    props: { message },
    global: { plugins: [router] },
  })
}

function makeMessage(overrides: Partial<Message> = {}): Message {
  return {
    id: 'msg-1',
    role: 'assistant',
    content: 'hello',
    timestamp: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('MessageBubble', () => {
  describe('plain assistant / user / system roles', () => {
    it('renders the content of an assistant message in plain text', () => {
      const wrapper = mount(MessageBubble, {
        props: { message: makeMessage({ role: 'assistant', content: 'hi there' }) },
      })

      expect(wrapper.text()).toContain('hi there')
      expect(wrapper.attributes('data-role')).toBe('assistant')
    })

    it('renders a user message and tags it accordingly', () => {
      const wrapper = mount(MessageBubble, {
        props: { message: makeMessage({ role: 'user', content: 'ping' }) },
      })

      expect(wrapper.attributes('data-role')).toBe('user')
      expect(wrapper.text()).toContain('ping')
    })
  })

  describe('tool roles', () => {
    it('renders a tool_call inside a collapsed <details> showing the tool name', () => {
      const wrapper = mount(MessageBubble, {
        props: {
          message: makeMessage({
            role: 'tool_call',
            content: 'read',
            toolName: 'read',
            toolInput: '/etc/hosts',
          }),
        },
      })

      const details = wrapper.find('details')
      expect(details.exists()).toBe(true)
      expect(details.attributes('open')).toBeUndefined()
      expect(details.find('summary').text()).toContain('read')
    })

    it('renders a tool_result inside a collapsed <details>', () => {
      const wrapper = mount(MessageBubble, {
        props: {
          message: makeMessage({
            role: 'tool_result',
            content: '127.0.0.1 localhost',
            toolName: 'read',
          }),
        },
      })

      const details = wrapper.find('details')
      expect(details.exists()).toBe(true)
      expect(details.attributes('open')).toBeUndefined()
      expect(wrapper.text()).toContain('127.0.0.1 localhost')
    })

    it('marks tool_error with an error role attribute so it can be styled distinctly', () => {
      const wrapper = mount(MessageBubble, {
        props: {
          message: makeMessage({
            role: 'tool_error',
            content: 'permission denied',
            toolName: 'bash',
          }),
        },
      })

      expect(wrapper.attributes('data-role')).toBe('tool_error')
      expect(wrapper.find('details').exists()).toBe(true)
    })
  })

  describe('delegation roles', () => {
    it('renders delegation_started with a waiting indicator', () => {
      const wrapper = mountWithRouter(
        makeMessage({
          role: 'delegation_started',
          content: '│ planner [started]',
        }),
      )

      expect(wrapper.attributes('data-role')).toBe('delegation_started')
      expect(wrapper.find('[data-testid="delegation-spinner"]').exists()).toBe(true)
    })

    it('renders a terminal delegation message without a spinner', () => {
      const wrapper = mountWithRouter(
        makeMessage({
          role: 'delegation',
          content: '│ planner [completed]',
        }),
      )

      expect(wrapper.attributes('data-role')).toBe('delegation')
      expect(wrapper.find('[data-testid="delegation-spinner"]').exists()).toBe(false)
      expect(wrapper.text()).toContain('planner')
    })

    it('renders the target agent name as a router link to /agents/:id when targetAgent is set', () => {
      const wrapper = mountWithRouter(
        makeMessage({
          role: 'delegation_started',
          content: 'delegating to planner',
          targetAgent: 'planner',
          chainId: 'chain-1',
          status: 'running',
        }),
      )

      const link = wrapper.find('[data-testid="delegation-agent-link"]')
      expect(link.exists()).toBe(true)
      expect(link.text()).toContain('planner')
      expect(link.attributes('href')).toBe('/agents/planner')
    })

    it('shows live progress (tool count, current tool, elapsed time) for in-flight delegations', () => {
      const wrapper = mountWithRouter(
        makeMessage({
          role: 'delegation_started',
          content: 'working',
          targetAgent: 'planner',
          chainId: 'chain-1',
          status: 'running',
          toolCalls: 4,
          lastTool: 'read',
        }),
      )

      const progress = wrapper.find('[data-testid="delegation-progress"]')
      expect(progress.exists()).toBe(true)
      expect(progress.text()).toContain('4')
      expect(progress.text()).toContain('read')
      expect(wrapper.find('[data-testid="delegation-elapsed"]').exists()).toBe(true)
    })

    it('does not show the live progress block on terminal delegation messages', () => {
      const wrapper = mountWithRouter(
        makeMessage({
          role: 'delegation',
          content: 'done',
          targetAgent: 'planner',
          chainId: 'chain-1',
          status: 'completed',
          toolCalls: 4,
          lastTool: 'read',
        }),
      )

      expect(wrapper.find('[data-testid="delegation-progress"]').exists()).toBe(false)
    })
  })

  describe('thinking role', () => {
    it('renders thinking content in a dimmed italic block', () => {
      const wrapper = mount(MessageBubble, {
        props: {
          message: makeMessage({ role: 'thinking', content: 'considering options' }),
        },
      })

      expect(wrapper.attributes('data-role')).toBe('thinking')
      expect(wrapper.text()).toContain('considering options')
    })
  })
})
