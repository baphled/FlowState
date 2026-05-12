<script setup lang="ts">
import { computed, type Component } from 'vue'
import {
  MessageCircle,
  Plus,
  Trash2,
  Search,
  Bot,
  FileText,
  Clock,
  Inbox,
  Square,
  Paperclip,
  X,
} from 'lucide-vue-next'

defineOptions({ name: 'Icon' })

/**
 * UI Parity PR2 I1 (May 2026) — thin Lucide wrapper.
 *
 * One component, one prop. Centralises the icon catalogue so call sites
 * stay terse (`<Icon name="trash" />` vs `<Trash2 :size="16" />`) and
 * the mapping from semantic name to library glyph lives in exactly one
 * place.
 *
 * Stroke is `currentColor` (set on `.icon` below) so the icon inherits
 * the surrounding text colour — theme switches (light / dark / contrast)
 * propagate without touching this component. No hardcoded fill or
 * stroke colour.
 */
export type IconName =
  | 'message'
  | 'plus'
  | 'trash'
  | 'search'
  | 'bot'
  | 'document'
  | 'clock'
  | 'inbox'
  | 'stop'
  | 'attach'
  | 'close'

// Catalogue: semantic name -> Lucide component. Add new entries here as
// call sites grow; the IconName union above keeps consumers honest.
const REGISTRY: Record<IconName, Component> = {
  message: MessageCircle,
  plus: Plus,
  trash: Trash2,
  search: Search,
  bot: Bot,
  document: FileText,
  clock: Clock,
  inbox: Inbox,
  stop: Square,
  attach: Paperclip,
  close: X,
}

const props = withDefaults(
  defineProps<{
    name: IconName
    /** Pixel size for both width and height. Defaults to 16 (inline-text). */
    size?: number | string
    /** Stroke width forwarded to Lucide. Default 2 matches the library default. */
    strokeWidth?: number | string
    /** Optional aria-label; when supplied the SVG advertises itself as img. */
    ariaLabel?: string
  }>(),
  {
    size: 16,
    strokeWidth: 2,
    ariaLabel: undefined,
  },
)

const component = computed(() => REGISTRY[props.name])
</script>

<template>
  <component
    :is="component"
    :size="props.size"
    :stroke-width="props.strokeWidth"
    :aria-label="props.ariaLabel"
    :aria-hidden="props.ariaLabel ? undefined : 'true'"
    :role="props.ariaLabel ? 'img' : undefined"
    class="icon"
    data-testid="icon"
    :data-icon-name="props.name"
  />
</template>

<style scoped>
/*
 * `currentColor` makes the stroke follow the inherited text colour, so
 * any CSS that themes the surrounding text themes the icon for free.
 * No hardcoded fill or stroke value — the consumer's color rule wins.
 */
.icon {
  color: currentColor;
  vertical-align: middle;
  flex-shrink: 0;
}
</style>
