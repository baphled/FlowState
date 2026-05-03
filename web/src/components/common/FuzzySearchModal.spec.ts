import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import FuzzySearchModal from './FuzzySearchModal.vue'
import type { FuzzySearchItem } from '@/composables/useFuzzyFilter'

const SAMPLE_ITEMS: FuzzySearchItem[] = [
  { id: '1', label: 'Claude Opus 4', group: 'Anthropic', meta: 'Most capable' },
  { id: '2', label: 'Claude Sonnet 4', group: 'Anthropic', meta: 'Balanced' },
  { id: '3', label: 'GPT-4o', group: 'OpenAI', meta: 'Fast' },
]

function mountModal(props: Record<string, unknown> = {}) {
  return mount(FuzzySearchModal, {
    props: {
      items: SAMPLE_ITEMS,
      open: true,
      ...props,
    },
    attachTo: document.body,
  })
}

function dispatchDocumentKey(key: string): void {
  document.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
}

describe('FuzzySearchModal', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders nothing when open is false', () => {
    const wrapper = mount(FuzzySearchModal, {
      props: { items: SAMPLE_ITEMS, open: false },
    })

    expect(wrapper.find('[data-testid="fuzzy-search-modal"]').exists()).toBe(false)
  })

  it('renders the modal overlay when open is true', () => {
    const wrapper = mountModal()

    expect(wrapper.find('[data-testid="fuzzy-search-modal"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-input"]').exists()).toBe(true)
  })

  it('renders all items in the list', () => {
    const wrapper = mountModal()

    const listItems = wrapper.findAll('[data-testid^="fuzzy-search-item-"]')
    expect(listItems.length).toBe(3)
    expect(listItems[0].text()).toContain('Claude Opus 4')
    expect(listItems[1].text()).toContain('Claude Sonnet 4')
    expect(listItems[2].text()).toContain('GPT-4o')
  })

  it('renders group headers when items have groups', () => {
    const wrapper = mountModal()

    expect(wrapper.find('[data-testid="fuzzy-search-group-Anthropic"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-group-OpenAI"]').exists()).toBe(true)
  })

  it('filters items when typing in the search input', async () => {
    const wrapper = mountModal()

    const input = wrapper.find('[data-testid="fuzzy-search-input"]')
    await input.setValue('gpt')
    await flushPromises()

    const listItems = wrapper.findAll('[data-testid^="fuzzy-search-item-"]')
    expect(listItems.length).toBe(1)
    expect(listItems[0].text()).toContain('GPT-4o')
  })

  it('shows empty message when no results match', async () => {
    const wrapper = mountModal({ emptyMessage: 'Nothing found' })

    const input = wrapper.find('[data-testid="fuzzy-search-input"]')
    await input.setValue('xyznonexistent')
    await flushPromises()

    expect(wrapper.find('[data-testid="fuzzy-search-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-empty"]').text()).toContain('Nothing found')
  })

  it('emits select and close when an item is clicked', async () => {
    const wrapper = mountModal()

    const item = wrapper.find('[data-testid="fuzzy-search-item-1"]')
    await item.trigger('click')

    expect(wrapper.emitted('select')).toBeTruthy()
    expect(wrapper.emitted('select')![0][0]).toEqual(
      expect.objectContaining({ id: '1', label: 'Claude Opus 4' }),
    )
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('emits close when backdrop is clicked', async () => {
    const wrapper = mountModal()

    await wrapper.find('[data-testid="fuzzy-search-backdrop"]').trigger('click')

    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('emits close when Escape is pressed', async () => {
    const wrapper = mountModal()

    dispatchDocumentKey('Escape')
    await flushPromises()

    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('moves highlight down on ArrowDown and selects on Enter', async () => {
    const wrapper = mountModal()

    dispatchDocumentKey('ArrowDown')
    dispatchDocumentKey('Enter')
    await flushPromises()

    expect(wrapper.emitted('select')).toBeTruthy()
    expect(wrapper.emitted('select')![0][0]).toEqual(
      expect.objectContaining({ id: '2', label: 'Claude Sonnet 4' }),
    )
  })

  it('moves highlight up on ArrowUp with wrapping', async () => {
    const wrapper = mountModal()

    dispatchDocumentKey('ArrowUp')
    dispatchDocumentKey('Enter')
    await flushPromises()

    expect(wrapper.emitted('select')).toBeTruthy()
    expect(wrapper.emitted('select')![0][0]).toEqual(
      expect.objectContaining({ id: '3', label: 'GPT-4o' }),
    )
  })

  it('uses default placeholder and empty message when not provided', () => {
    const wrapper = mountModal()

    const input = wrapper.find('[data-testid="fuzzy-search-input"]')
    expect(input.attributes('placeholder')).toBe('Search...')
  })

  it('uses custom placeholder when provided', () => {
    const wrapper = mountModal({ placeholder: 'Type a model name...' })

    const input = wrapper.find('[data-testid="fuzzy-search-input"]')
    expect(input.attributes('placeholder')).toBe('Type a model name...')
  })

  it('auto-focuses the search input when opened', async () => {
    const focusSpy = vi.spyOn(HTMLInputElement.prototype, 'focus').mockImplementation(() => {})

    mountModal()
    await flushPromises()

    expect(focusSpy).toHaveBeenCalled()
    focusSpy.mockRestore()
  })

  it('removes document event listeners on unmount', async () => {
    const wrapper = mountModal()

    const removeSpy = vi.spyOn(document, 'removeEventListener')
    wrapper.unmount()

    expect(removeSpy).toHaveBeenCalledWith('keydown', expect.any(Function))
  })

  it('renders meta text when provided', () => {
    const wrapper = mountModal()

    const firstItem = wrapper.find('[data-testid="fuzzy-search-item-1"]')
    expect(firstItem.text()).toContain('Most capable')
  })
})
