import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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

// Stub all tool components to avoid deep rendering. The per-tool components
// own their own ToolBubble chrome; MessageBubble no longer wraps them in an
// outer card layer (one tool invocation = one card).
const ToolErrorCard = {
  template: '<div data-testid="tool-error-renderer" :data-tool="toolName" />',
  props: ['toolName', 'heading', 'body'],
}
// Stub specific tool renderers — each carries the data-tool attr the spec
// asserts against, mirroring what the real component would render.
const BashTool = {
  template: '<div data-component="tool-renderer" data-tool="bash" />',
  props: ['toolName', 'heading', 'body', 'status', 'toolInput'],
}
const ReadTool = {
  template: '<div data-component="tool-renderer" data-tool="read" />',
  props: ['toolName', 'heading', 'body', 'status', 'toolInput'],
}
const GenericTool = {
  template: '<div data-component="tool-renderer" data-tool="generic" />',
  props: ['toolName', 'heading', 'body', 'status', 'toolInput'],
}

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
      // Bug Hunt (May 2026) sibling-confusion fix — MessageBubble's
      // delegation-card click now routes through loadSessionForDelegation
      // so the chainId disambiguates same-agent siblings. The agent-id
      // resolver is still mounted as the fallback path.
      loadSessionForDelegation: vi.fn(),
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
    it('renders a tool_result with the registered BashTool component', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_result',
          content: 'output',
          toolName: 'bash',
          toolInput: 'ls',
        }),
      )

      const tool = wrapper.find('[data-component="tool-renderer"]')
      expect(tool.exists()).toBe(true)
      expect(tool.attributes('data-tool')).toBe('bash')
    })

    it('renders a tool_result with the registered ReadTool component', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_result',
          content: 'file content',
          toolName: 'read',
          toolInput: 'foo.txt',
        }),
      )

      const tool = wrapper.find('[data-component="tool-renderer"]')
      expect(tool.exists()).toBe(true)
      expect(tool.attributes('data-tool')).toBe('read')
    })

    it('renders an unknown tool_result via GenericTool', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_result',
          content: 'some output',
          toolName: 'unknown',
        }),
      )

      const tool = wrapper.find('[data-component="tool-renderer"]')
      expect(tool.exists()).toBe(true)
      expect(tool.attributes('data-tool')).toBe('generic')
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

    // Regression cover for the unmatched tool_call rendering path. When a
    // tool_call has no paired tool_result (collapseToolPairs leaves it intact),
    // the previous revision fell through to the plain-message branch and
    // surfaced the role as a "TOOL_CALL" label (uppercased by .message-role
    // CSS). The collapsable tool card already signals "this is a tool call",
    // so the role label is redundant noise — route tool_call through the
    // same per-tool component the tool_result path uses.
    it('renders an unmatched tool_call with the registered tool component, not a TOOL_CALL role label', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_call',
          content: '',
          toolName: 'bash',
          toolInput: JSON.stringify({ command: 'ls' }),
        }),
      )

      const tool = wrapper.find('[data-component="tool-renderer"]')
      expect(tool.exists()).toBe(true)
      expect(tool.attributes('data-tool')).toBe('bash')
      // The literal "TOOL_CALL" label (rendered via uppercased .message-role)
      // must not appear in the DOM — the card chrome already conveys it.
      expect(wrapper.find('.message-role').exists()).toBe(false)
      expect(wrapper.text()).not.toContain('tool_call')
      expect(wrapper.text().toUpperCase()).not.toContain('TOOL_CALL')
    })

    it('renders an unmatched tool_call for an unknown tool via GenericTool', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_call',
          content: '',
          toolName: 'mystery',
        }),
      )

      const tool = wrapper.find('[data-component="tool-renderer"]')
      expect(tool.exists()).toBe(true)
      expect(tool.attributes('data-tool')).toBe('generic')
      expect(wrapper.find('.message-role').exists()).toBe(false)
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

    it('calls loadSessionForDelegation with chainId + targetAgent when clicking the delegation agent link', async () => {
      // Sibling-confusion fix — the resolver routes via chainId so a
      // parent with two delegations to the same agent doesn't collapse
      // both cards onto the most-recent sibling. Both fields are
      // load-bearing: chainId routes when known, agentId is the
      // fallback for the chainId-missing case.
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

      expect(mockChatStore.loadSessionForDelegation).toHaveBeenCalledWith({
        chainId: 'chain-1',
        agentId: 'planner',
      })
      // The agent-id-only resolver must NOT be the entry point any
      // more — going through loadSessionForDelegation is what makes
      // chainId routing possible.
      expect(mockChatStore.loadSessionByAgentId).not.toHaveBeenCalled()
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
          stubs: { ToolErrorCard, GenericTool },
        },
      })

      const link = wrapper.find('[data-testid="delegation-agent-link"]')
      await link.trigger('click')

      expect(mockChatStore.loadSessionForDelegation).toHaveBeenCalledWith({
        chainId: 'chain-1',
        agentId: 'planner',
      })
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
          stubs: { ToolErrorCard, GenericTool },
        },
      })

      const link = wrapper.find('[data-testid="delegation-agent-link"]')
      await link.trigger('click')

      expect(mockChatStore.loadSessionForDelegation).toHaveBeenCalledWith({
        chainId: 'chain-1',
        agentId: 'planner',
      })
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

  // Copy affordance on plain user/assistant bubbles. The tool-call/result
  // branches already expose copy via their own per-tool components, so this
  // contract is scoped to plain text bubbles only (assistant, user, system).
  // Delegation, thinking, and tool roles must not surface a duplicate
  // bubble-level copy button.
  describe('copy affordance', () => {
    const writeText = vi.fn()

    beforeEach(() => {
      writeText.mockReset()
      vi.stubGlobal('navigator', {
        clipboard: { writeText },
      } as unknown as Navigator)
    })

    afterEach(() => {
      vi.unstubAllGlobals()
    })

    it('renders a copy button on assistant message bubbles', () => {
      const wrapper = mountWithStubs(makeMessage({ role: 'assistant', content: 'hi there' }))

      expect(wrapper.find('[data-testid="message-copy-btn"]').exists()).toBe(true)
    })

    it('renders a copy button on user message bubbles', () => {
      const wrapper = mountWithStubs(makeMessage({ role: 'user', content: 'ping' }))

      expect(wrapper.find('[data-testid="message-copy-btn"]').exists()).toBe(true)
    })

    it('copies the assistant message content to the clipboard when clicked', async () => {
      writeText.mockResolvedValueOnce(undefined)
      const wrapper = mountWithStubs(
        makeMessage({ role: 'assistant', content: 'the assistant body' }),
      )

      await wrapper.get('[data-testid="message-copy-btn"]').trigger('click')

      expect(writeText).toHaveBeenCalledWith('the assistant body')
    })

    it('copies the user message content to the clipboard when clicked', async () => {
      writeText.mockResolvedValueOnce(undefined)
      const wrapper = mountWithStubs(makeMessage({ role: 'user', content: 'ping pong' }))

      await wrapper.get('[data-testid="message-copy-btn"]').trigger('click')

      expect(writeText).toHaveBeenCalledWith('ping pong')
    })

    it('does not render a bubble-level copy button on tool_result messages', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'tool_result',
          content: 'output',
          toolName: 'bash',
          toolInput: 'ls',
        }),
      )

      expect(wrapper.find('[data-testid="message-copy-btn"]').exists()).toBe(false)
    })

    it('does not render a copy button on delegation cards', () => {
      const wrapper = mountWithRouter(
        makeMessage({
          role: 'delegation_started',
          content: 'delegating',
          targetAgent: 'planner',
        }),
      )

      expect(wrapper.find('[data-testid="message-copy-btn"]').exists()).toBe(false)
    })

    it('does not render a copy button on thinking messages', () => {
      const wrapper = mountWithStubs(
        makeMessage({ role: 'thinking', content: 'considering options' }),
      )

      expect(wrapper.find('[data-testid="message-copy-btn"]').exists()).toBe(false)
    })
  })

  describe('failed-send marker (compounding bug C-2)', () => {
    // chatStore marks a user-message bubble status='failed' when
    // sendSessionMessage rejects. The user sees a persistent inline marker
    // alongside the existing toast surfacing — minimum viable failure
    // visibility per the PR-2 brief.

    it('renders a visible "Failed to send" marker on a user bubble with status=failed', () => {
      const wrapper = mountWithStubs(
        makeMessage({ role: 'user', content: 'lost message', status: 'failed' }),
      )

      const marker = wrapper.find('[data-testid="message-failed-marker"]')
      expect(marker.exists()).toBe(true)
      expect(marker.text()).toMatch(/failed/i)
    })

    it('does NOT render the failed marker when the user message has no status', () => {
      const wrapper = mountWithStubs(
        makeMessage({ role: 'user', content: 'normal message' }),
      )

      expect(wrapper.find('[data-testid="message-failed-marker"]').exists()).toBe(false)
    })

    it('does NOT render the failed marker on assistant messages even with status=failed', () => {
      // Assistants don't go through the optimistic-send path; failed-status
      // on an assistant has different semantics (a separate concern). The
      // marker is user-message specific.
      const wrapper = mountWithStubs(
        makeMessage({ role: 'assistant', content: 'a reply', status: 'failed' }),
      )

      expect(wrapper.find('[data-testid="message-failed-marker"]').exists()).toBe(false)
    })

    it('exposes the failed status via data-status so e2e specs can assert on it', () => {
      const wrapper = mountWithStubs(
        makeMessage({ role: 'user', content: 'lost', status: 'failed' }),
      )

      expect(wrapper.attributes('data-status')).toBe('failed')
    })
  })

  // Thinking-only degraded turn — the bug-fix follow-up flagged in
  // `Empty-Content Thinking-Only Assistant Turn (May 2026)`. When the
  // backend session accumulator synthesises a placeholder assistant
  // message (empty content + thinking blocks + a non-empty stop_reason)
  // because the provider produced reasoning tokens but never emitted
  // visible content, the chat must NOT render that as a blank bubble —
  // a stalled-stream lookalike. The bubble must show a soft-error
  // affordance distinct from the critical-error banner and from a true
  // stall.
  //
  // User-feedback rationale (May 7 2026): the original copy ("No response
  // produced / The agent thought through this turn but produced no
  // response.") read as a system bug report rather than a recovery hint
  // and gave the user nothing actionable. The reword pins the same render
  // branch but with conversational, action-bearing copy: tells the user
  // the model stopped before replying AND that they should try again.
  // The affordance must remain (a) informational and (b) actionable.
  describe('thinking-only degraded turn affordance', () => {
    it('renders a soft-error affordance when content is empty but thinkingBlocks + stopReason are present', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: '',
          stopReason: 'end_turn',
          thinkingBlocks: [
            { thinking: 'considering options', signature: 'sig-1' },
          ],
        }),
      )

      const affordance = wrapper.find('[data-testid="thinking-only-affordance"]')
      expect(affordance.exists()).toBe(true)
      // Pin the user outcome, not the exact phrasing: the affordance
      // must communicate (a) the model stopped without replying, and
      // (b) what the user can do next (re-prompt). Two assertions, one
      // for each user-visible contract.
      const text = affordance.text()
      expect(text).toMatch(/stopped before replying|didn't (come through|reply)|no reply/i)
      expect(text).toMatch(/try (sending|asking|again)|send.*again|ask again/i)
    })

    it('does NOT render the affordance for a normal content-bearing assistant message', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: 'here is a real reply',
          stopReason: 'end_turn',
          thinkingBlocks: [
            { thinking: 'considering options', signature: 'sig-1' },
          ],
        }),
      )

      expect(wrapper.find('[data-testid="thinking-only-affordance"]').exists()).toBe(false)
      expect(wrapper.text()).toContain('here is a real reply')
    })

    it('does NOT render the affordance for a true stall (empty content, no thinkingBlocks)', () => {
      // The placeholder shape is specifically (content === "") AND
      // thinkingBlocks.length > 0 AND stopReason !== "". A true stall —
      // an empty bubble with nothing else attached — must NOT trigger
      // the soft-error affordance, because it really is a stall and a
      // different surface owns that signal.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: '',
        }),
      )

      expect(wrapper.find('[data-testid="thinking-only-affordance"]').exists()).toBe(false)
    })

    it('does NOT render the affordance when thinkingBlocks is empty even with a stopReason', () => {
      // A placeholder synthesised by something other than the backend
      // accumulator's degraded-turn path (e.g. a mid-stream hard error
      // that surfaced via stream_critical) must not collide with this
      // rendering branch. CriticalErrorBanner owns the critical case.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: '',
          stopReason: 'end_turn',
          thinkingBlocks: [],
        }),
      )

      expect(wrapper.find('[data-testid="thinking-only-affordance"]').exists()).toBe(false)
    })

    it('uses role="status" so the affordance is announced as informational, not assertive', () => {
      // Distinct from CriticalErrorBanner which uses role="alert" — this
      // is a soft, post-hoc notification of a degraded turn, not an
      // urgent failure.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: '',
          stopReason: 'end_turn',
          thinkingBlocks: [
            { thinking: 'reasoning step', signature: 'sig-2' },
          ],
        }),
      )

      const affordance = wrapper.find('[data-testid="thinking-only-affordance"]')
      expect(affordance.exists()).toBe(true)
      expect(affordance.attributes('role')).toBe('status')
    })
  })

  // Empty-content assistant turn — the May 10 2026 follow-up flagged by the
  // user: "Are we outputting an agent response, along with a tool call? If
  // so, this seems broken. We should just return the tool calls. Agent
  // blocks are for when an agent *actually* has a response."
  //
  // Two store paths produce a sealed assistant message with empty content:
  //
  //   1. handleToolCallEvent at chatStore.ts:2509 seals any in-flight
  //      assistant placeholder when a tool_call SSE event arrives. If no
  //      content chunk had landed yet (the turn went straight to tool use),
  //      the sealed placeholder carries content === ''. Adjacent tool_call /
  //      tool_result rows in the message list ARE the response.
  //
  //   2. The Streaming Coherence Slice C "empty_turn" placeholder pushed by
  //      handleStreamDone (chatStore.ts) carries content === '' +
  //      stopReason === 'empty_turn' and no thinkingBlocks. This DOES render
  //      a soft-error affordance — see bug fix #27 (May 11 2026). Pre-fix the
  //      placeholder was pushed into the store but no MessageBubble v-else-if
  //      consumed it, so true empty turns (no content, no thinking, no
  //      tool_calls — Anthropic / OpenAI occasionally return this shape) were
  //      silently swallowed. The new branch reuses the thinking-only-degraded
  //      copy ("Reply didn't come through") so the user gets the same
  //      affordance the thinking-only case already provides.
  //
  // The mid-stream sealed placeholder (case 1) must NOT render the plain
  // assistant chrome (role label, empty MarkdownRenderer, copy button on
  // empty content). The user's outcome: tool cards stand alone; an empty
  // assistant bubble alongside them is phantom UI.
  //
  // Distinct from the thinking-only-degraded branch — that branch handles
  // the (empty content + thinkingBlocks + stopReason) shape and MUST keep
  // firing. The empty_turn branch fires when content + thinkingBlocks are
  // BOTH empty and stopReason === 'empty_turn' specifically.
  describe('empty-content assistant suppression', () => {
    it('does NOT render the message-bubble wrapper when content is empty (mid-stream sealed placeholder)', () => {
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: '',
          // status='completed' is what handleToolCallEvent leaves on the
          // sealed placeholder — see chatStore.ts:2509-2511.
          status: 'completed',
        }),
      )

      // The entire bubble wrapper must not appear — pre-fix the inner
      // chrome was suppressed but the outer `<div class="message-bubble
      // assistant">` still rendered as an empty styled box (padding,
      // border, border-radius), surfacing as a visible blank card.
      // The user's contract: no data → no DOM at all for this bubble.
      expect(wrapper.find('.message-bubble').exists()).toBe(false)
      expect(wrapper.find('.message-role').exists()).toBe(false)
      expect(wrapper.find('[data-testid="message-copy-btn"]').exists()).toBe(false)
    })

    it('does NOT render the message-bubble wrapper when content is whitespace-only', () => {
      // Defensive: a whitespace-only assistant carries no visible
      // response either, even if it slipped past the seal predicate.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: '   \n  ',
          status: 'completed',
        }),
      )

      expect(wrapper.find('.message-bubble').exists()).toBe(false)
      expect(wrapper.find('.message-role').exists()).toBe(false)
      expect(wrapper.find('[data-testid="message-copy-btn"]').exists()).toBe(false)
    })

    it('renders the empty-turn soft-error affordance for the empty_turn placeholder (bug fix #27)', () => {
      // Streaming Coherence Slice C placeholder shape (see chatStore.ts
      // handleStreamDone). No content, no thinkingBlocks, stopReason
      // === 'empty_turn'. Pre-fix #27 (May 11 2026) the placeholder was
      // pushed into the store but no MessageBubble v-else-if consumed it,
      // so true empty turns were silently swallowed — the user saw their
      // prompt sit there with no follow-up artefact at all. The new
      // branch reuses the "Reply didn't come through" copy from the
      // thinking-only-degraded affordance (commit 87c114c8 wording) —
      // the UX vocabulary is the same: "the model didn't reply, try
      // again". role='status' (informational, not alert) matches.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: '',
          status: 'completed',
          stopReason: 'empty_turn',
        }),
      )

      // The wrapper renders (no longer suppressed by hasRenderableContent).
      expect(wrapper.find('.message-bubble').exists()).toBe(true)
      // The empty-turn branch surfaces its own affordance with a distinct
      // testid so the thinking-only assertions stay independent.
      const affordance = wrapper.find('[data-testid="empty-turn-affordance"]')
      expect(affordance.exists()).toBe(true)
      expect(affordance.attributes('role')).toBe('status')
      // Same copy as the thinking-only-degraded affordance (commit
      // 87c114c8 — "Reply didn't come through"). The two cases share
      // the user-facing message intentionally; the underlying state
      // (empty placeholder vs thinking-only) is invisible to the user.
      expect(wrapper.text()).toContain("Reply didn't come through")
      expect(wrapper.text()).toContain('Try sending the prompt again')
      // Negative checks: the plain assistant chrome (role label,
      // MarkdownRenderer, copy button) must NOT appear — this is an
      // affordance, not a content bubble.
      expect(wrapper.find('.message-role').exists()).toBe(false)
      expect(wrapper.find('[data-testid="message-copy-btn"]').exists()).toBe(false)
      // And it must not collide with the thinking-only affordance —
      // that one fires only when thinkingBlocks are present.
      expect(wrapper.find('[data-testid="thinking-only-affordance"]').exists()).toBe(false)
    })

    it('does NOT render the empty-turn affordance for a plain empty assistant (no stopReason)', () => {
      // Defensive: the new branch must be narrow. A sealed mid-stream
      // empty placeholder (case 1 above, no stopReason set) still goes
      // through the existing hasVisibleAssistantContent gate and stays
      // suppressed. Only the explicit stopReason='empty_turn' shape
      // pushed by handleStreamDone gets the affordance.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: '',
          status: 'completed',
        }),
      )

      expect(wrapper.find('[data-testid="empty-turn-affordance"]').exists()).toBe(false)
      expect(wrapper.find('.message-bubble').exists()).toBe(false)
    })

    it('does NOT render the empty-turn affordance when content-bearing turn has stopReason="empty_turn"', () => {
      // Defensive: if a future code path sets stopReason='empty_turn' on
      // a content-bearing assistant message (shouldn't happen, but
      // belt-and-braces), the content takes precedence and the
      // affordance does not collide with the plain render.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: 'real content',
          status: 'completed',
          stopReason: 'empty_turn',
        }),
      )

      expect(wrapper.find('[data-testid="empty-turn-affordance"]').exists()).toBe(false)
      // Plain render path takes over.
      expect(wrapper.find('.message-role').exists()).toBe(true)
      expect(wrapper.text()).toContain('real content')
    })

    it('still renders normally when an assistant message has actual content', () => {
      // Regression cover: the suppression must be narrow. A real
      // content-bearing assistant turn that happens to involve tool
      // calls (assistant text BEFORE the tool, sealed normally) must
      // still render its bubble.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: 'Let me plan this out.',
          status: 'completed',
        }),
      )

      expect(wrapper.find('.message-role').exists()).toBe(true)
      expect(wrapper.text()).toContain('Let me plan this out.')
      expect(wrapper.find('[data-testid="message-copy-btn"]').exists()).toBe(true)
    })

    it('still renders the thinking-only-degraded affordance when its predicate matches (suppression must not collide)', () => {
      // The thinking-only-degraded `v-else-if` branch must continue to
      // win when its three signals are all present (empty content +
      // thinkingBlocks + stopReason). Sanity check that the new
      // empty-content gate did not accidentally swallow that branch.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'assistant',
          content: '',
          stopReason: 'end_turn',
          thinkingBlocks: [{ thinking: 'considering', signature: 'sig' }],
        }),
      )

      const affordance = wrapper.find('[data-testid="thinking-only-affordance"]')
      expect(affordance.exists()).toBe(true)
    })

    it('still renders an empty user bubble (the gate is assistant-specific)', () => {
      // Defensive: an empty user message (e.g. accidental Enter on the
      // composer) is a separate concern. The suppression here is
      // assistant-only because the bug is about phantom AGENT
      // responses; user-side empty bubbles already have failed-send
      // affordances elsewhere.
      const wrapper = mountWithStubs(
        makeMessage({
          role: 'user',
          content: '',
        }),
      )

      // Role label still visible — user-bubble suppression is out of scope.
      expect(wrapper.find('.message-role').exists()).toBe(true)
    })
  })
})
