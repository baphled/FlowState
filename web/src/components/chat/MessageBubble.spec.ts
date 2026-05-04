import { describe, expect, it, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import MessageBubble from './MessageBubble.vue'
import type { Message } from '@/types'
import { registerTool } from '@/tools/toolRegistry'
import { useChatStore } from '@/stores/chatStore'

// Mock chat store
vi.mock('@/stores/chatStore', () => ({
  useChatStore: vi.fn(),
}))

// Stub all tool components to avoid deep rendering
const ToolBubble = {
  template: '<div data-component="tool" :data-tool="toolName" :data-status="status"><slot /></div>',
  props: ['toolName', 'title', 'subtitle', 'status', 'defaultOpen'],
}
const ToolErrorCard = {
  template: '<div data-testid="tool-error-renderer" :data-tool="toolName" />',
  props: ['toolName', 'heading', 'body'],
}
// Stub specific tool renderers
const BashTool = { template: '<div data-tool="bash" />' }
const ReadTool = { template: '<div data-tool="read" />' }
const GenericTool = { template: '<div data-tool="generic" />' }

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

function mountWithRouter(message: Message, agentName?: string) {
  const router = makeRouter()
  return mount(MessageBubble, {
    props: { message, agentName },
    global: {
      plugins: [router],
      stubs: {
        ToolBubble,
        ToolErrorCard,
        GenericTool,
      },
    },
  })
}

function mountWithStubs(message: Message, agentName?: string) {
  return mount(MessageBubble, {
    props: { message, agentName },
    global: {
      stubs: {
        ToolBubble,
        ToolErrorCard,
        GenericTool,
      },
    },
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
  let mockChatStore: any

  beforeEach(() => {
    setActivePinia(createPinia())
    registerTool({ name: 'bash', component: BashTool })
    registerTool({ name: 'read', component: ReadTool })

    mockChatStore = {
      loadSessionByAgentId: vi.fn(),
    }
    vi.mocked(useChatStore).mockReturnValue(mockChatStore)
  })

  describe('plain assistant / user / system roles', () => {
    it('renders the content of an assistant message in plain text', () => {
      const wrapper = mountWithStubs(makeMessage({ role: 'assistant', content: 'hi there' }))

      expect(wrapper.text()).toContain('hi there')
      expect(wrapper.attributes('data-role')).toBe('assistant')
    })

    it('renders a user message and tags it accordingly', () => {
      const wrapper = mountWithStubs(makeMessage({ role: 'user', content: 'ping' }))

      expect(wrapper.attributes('data-role')).toBe('user')
      expect(wrapper.text()).toContain('ping')
    })

    it('renders the agent display name instead of the raw role for assistant messages', () => {
      const wrapper = mountWithStubs(
        makeMessage({ role: 'assistant', agentId: 'planner', content: 'hi' }),
        'Planner',
      )

      const role = wrapper.find('.message-role')
      expect(role.exists()).toBe(true)
      expect(role.text()).toBe('Planner')
      expect(role.text()).not.toBe('assistant')
    })

    it('falls back to the raw role when no agentName prop is supplied', () => {
      const wrapper = mountWithStubs(makeMessage({ role: 'assistant', content: 'hi' }))

      expect(wrapper.find('.message-role').text()).toBe('assistant')
    })
  })

  describe('tool roles', () => {
    it('renders a tool_result with BashTool via ToolBubble', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_result',
          content: 'output',
          toolName: 'bash',
          toolInput: 'ls',
        }),
      )

      const tool = wrapper.find('[data-component="tool"]')
      expect(tool.exists()).toBe(true)
      expect(tool.attributes('data-tool')).toBe('bash')
      expect(wrapper.find('[data-tool="bash"]').exists()).toBe(true)
    })

    it('renders a tool_result with ReadTool via ToolBubble', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_result',
          content: 'file content',
          toolName: 'read',
          toolInput: 'foo.txt',
        }),
      )

      const tool = wrapper.find('[data-component="tool"]')
      expect(tool.exists()).toBe(true)
      expect(tool.attributes('data-tool')).toBe('read')
      expect(wrapper.find('[data-tool="read"]').exists()).toBe(true)
    })

    it('renders an unknown tool_result with GenericTool via ToolBubble', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_result',
          content: 'some output',
          toolName: 'unknown',
        }),
      )

      const tool = wrapper.find('[data-component="tool"]')
      expect(tool.exists()).toBe(true)
      expect(wrapper.find('[data-tool="generic"]').exists()).toBe(true)
    })

    it('renders a tool_error with ToolErrorCard', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_error',
          content: 'permission denied',
          toolName: 'bash',
        }),
      )

      expect(wrapper.attributes('data-role')).toBe('tool_error')
      expect(wrapper.find('[data-testid="tool-error-renderer"]').exists()).toBe(true)
      expect(wrapper.find('[data-testid="tool-error-renderer"]').attributes('data-tool')).toBe('bash')
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

    it('renders the target agent name as a button (not an anchor pointing at AgentInfoView)', () => {
      // Previous revision rendered the affordance as <router-link to="/agents/:id">,
      // which combined with `@click.prevent` failed to suppress the route push and
      // landed users on AgentInfoView instead of the delegated child session.
      // The delegation card is a session-load action, not navigation — the
      // affordance must be a button so middle-click / right-click / @click handling
      // all behave consistently with that intent.
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
      expect(link.element.tagName).toBe('BUTTON')
      expect(link.attributes('href')).toBeUndefined()
    })

    it('calls loadSessionByAgentId when clicking the delegation agent link', async () => {
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
      await link.trigger('click')

      expect(mockChatStore.loadSessionByAgentId).toHaveBeenCalledWith('planner')
    })

    // Regression cover for the bug where clicking a delegation card navigated
    // to /agents/:id (AgentInfoView) before the chat store had loaded the
    // delegated session. The previous assertion above only proved the click
    // handler ran, not that vue-router had been suppressed; in the live app
    // <RouterLink> still pushed the route. The card must not navigate at all.
    it('does not push the /agents/:id route when the delegation card is clicked', async () => {
      const router = makeRouter()
      await router.push('/')
      await router.isReady()
      const pushSpy = vi.spyOn(router, 'push')

      const wrapper = mount(MessageBubble, {
        props: {
          message: makeMessage({
            role: 'delegation_started',
            content: 'delegating to planner',
            targetAgent: 'planner',
            chainId: 'chain-1',
            status: 'running',
          }),
        },
        global: {
          plugins: [router],
          stubs: { ToolBubble, ToolErrorCard, GenericTool },
        },
      })

      const link = wrapper.find('[data-testid="delegation-agent-link"]')
      await link.trigger('click')

      expect(mockChatStore.loadSessionByAgentId).toHaveBeenCalledWith('planner')
      // The route MUST stay where the user was — clicking a delegation card
      // is a session-load action, not navigation. AgentInfoView is reached
      // from the agents picker, never from this affordance.
      expect(router.currentRoute.value.path).toBe('/')
      const pushedToAgents = pushSpy.mock.calls.some((call) => {
        const target = call[0]
        if (typeof target === 'string') return target.startsWith('/agents/')
        if (target && typeof target === 'object' && 'path' in target) {
          return typeof target.path === 'string' && target.path.startsWith('/agents/')
        }
        return false
      })
      expect(pushedToAgents).toBe(false)
    })

    it('does not push /agents/:id when the terminal delegation card is clicked', async () => {
      const router = makeRouter()
      await router.push('/')
      await router.isReady()
      const pushSpy = vi.spyOn(router, 'push')

      const wrapper = mount(MessageBubble, {
        props: {
          message: makeMessage({
            role: 'delegation',
            content: 'done',
            targetAgent: 'planner',
            chainId: 'chain-1',
            status: 'completed',
          }),
        },
        global: {
          plugins: [router],
          stubs: { ToolBubble, ToolErrorCard, GenericTool },
        },
      })

      const link = wrapper.find('[data-testid="delegation-agent-link"]')
      await link.trigger('click')

      expect(mockChatStore.loadSessionByAgentId).toHaveBeenCalledWith('planner')
      const pushedToAgents = pushSpy.mock.calls.some((call) => {
        const target = call[0]
        if (typeof target === 'string') return target.startsWith('/agents/')
        if (target && typeof target === 'object' && 'path' in target) {
          return typeof target.path === 'string' && target.path.startsWith('/agents/')
        }
        return false
      })
      expect(pushedToAgents).toBe(false)
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
      const wrapper = mountWithStubs(makeMessage({ role: 'thinking', content: 'considering options' }))

      expect(wrapper.attributes('data-role')).toBe('thinking')
      expect(wrapper.text()).toContain('considering options')
    })
  })
})
