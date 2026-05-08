import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import BashTool, { RENDER_MAX_LINES, RENDER_MAX_BYTES } from './BashTool.vue'

const CopyButton = {
  props: {
    text: {
      type: String,
      required: true,
    },
  },
  template: '<span data-testid="copy-btn">{{ text }}</span>',
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

describe('BashTool', () => {
  it('renders the bash command and output blocks', () => {
    const wrapper = mount(BashTool, {
      props: {
        toolName: 'bash',
        heading: 'ls -la',
        body: 'file-a\nfile-b',
        status: 'completed',
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
    expect(wrapper.find('[data-component="bash-tool"]').exists()).toBe(true)
    expect(wrapper.get('[data-component="bash-command"]').text()).toContain('ls -la')
    expect(wrapper.get('[data-component="bash-output"]').text()).toContain('file-a')
    expect(wrapper.findAll('[data-testid="copy-btn"]')).toHaveLength(2)
  })

  it('renders only the command block when output is empty', () => {
    const wrapper = mount(BashTool, {
      props: {
        toolName: 'bash',
        heading: 'pwd',
        body: '',
      },
      global: {
        stubs: {
          CopyButton,
          ToolBubble,
        },
      },
    })

    expect(wrapper.get('[data-component="bash-command"]').text()).toContain('pwd')
    expect(wrapper.find('[data-component="bash-output"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="copy-btn"]')).toHaveLength(1)
  })

  describe('render-cap', () => {
    function buildBody(lineCount: number, lineContent: (i: number) => string = (i) => `line-${i}`): string {
      const lines: string[] = []
      for (let i = 1; i <= lineCount; i += 1) {
        lines.push(lineContent(i))
      }
      return lines.join('\n')
    }

    it('renders the full body when under the render cap', () => {
      const body = buildBody(50)
      const wrapper = mount(BashTool, {
        props: {
          toolName: 'bash',
          heading: 'seq 1 50',
          body,
        },
        global: {
          stubs: { CopyButton, ToolBubble },
        },
      })

      const output = wrapper.get('[data-component="bash-output"]').text()
      expect(output).toContain('line-50')
      expect(wrapper.find('[data-component="bash-output-toggle"]').exists()).toBe(false)
      expect(wrapper.find('[data-component="bash-output-truncation-hint"]').exists()).toBe(false)
    })

    it('truncates the rendered body when over the line cap, with a show-full-output button', () => {
      const body = buildBody(500)
      const wrapper = mount(BashTool, {
        props: {
          toolName: 'bash',
          heading: 'seq 1 500',
          body,
        },
        global: {
          stubs: { CopyButton, ToolBubble },
        },
      })

      const output = wrapper.get('[data-component="bash-output"]').text()
      expect(output).toContain(`line-${RENDER_MAX_LINES}`)
      expect(output).not.toContain(`line-${RENDER_MAX_LINES + 1}`)

      const hint = wrapper.get('[data-component="bash-output-truncation-hint"]')
      expect(hint.text()).toContain(`${500 - RENDER_MAX_LINES} lines hidden`)

      const toggle = wrapper.get('[data-component="bash-output-toggle"]')
      expect(toggle.attributes('aria-label')).toBe('Show full output')
      expect(toggle.attributes('aria-expanded')).toBe('false')
      expect(toggle.text()).toContain('Show full output')
    })

    it('expands to the full body when the toggle is clicked', async () => {
      const body = buildBody(500)
      const wrapper = mount(BashTool, {
        props: {
          toolName: 'bash',
          heading: 'seq 1 500',
          body,
        },
        global: {
          stubs: { CopyButton, ToolBubble },
        },
      })

      await wrapper.get('[data-component="bash-output-toggle"]').trigger('click')

      const output = wrapper.get('[data-component="bash-output"]').text()
      expect(output).toContain('line-500')

      const toggle = wrapper.get('[data-component="bash-output-toggle"]')
      expect(toggle.attributes('aria-label')).toBe('Hide full output')
      expect(toggle.attributes('aria-expanded')).toBe('true')
      expect(toggle.text()).toContain('Show less')
      expect(wrapper.find('[data-component="bash-output-truncation-hint"]').exists()).toBe(false)
    })

    it('truncates by byte cap when the byte budget hits before the line cap', () => {
      // 50 lines, each ~200 bytes → ~10KB total, well over RENDER_MAX_BYTES (8KB)
      // but well under RENDER_MAX_LINES (200), so the byte cap must be the trigger.
      const padding = 'x'.repeat(199)
      const body = buildBody(50, (i) => `${i}-${padding}`)
      expect(body.length).toBeGreaterThan(RENDER_MAX_BYTES)

      const wrapper = mount(BashTool, {
        props: {
          toolName: 'bash',
          heading: 'long-lines',
          body,
        },
        global: {
          stubs: { CopyButton, ToolBubble },
        },
      })

      const output = wrapper.get('[data-component="bash-output"]').text()
      // Rendered slice must be byte-bounded under the line count.
      // Allow some slack for the trailing newline boundary; the rendered slice
      // of body characters must be <= RENDER_MAX_BYTES.
      const bashOutputCode = wrapper.get('[data-component="bash-output"] code').text()
      expect(bashOutputCode.length).toBeLessThanOrEqual(RENDER_MAX_BYTES)
      expect(output).not.toContain('50-')
      expect(wrapper.find('[data-component="bash-output-toggle"]').exists()).toBe(true)
    })
  })
})
