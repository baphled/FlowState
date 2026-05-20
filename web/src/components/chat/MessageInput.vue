<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import FuzzySearchModal from '@/components/common/FuzzySearchModal.vue'
import Icon from '@/components/common/Icon.vue'
import { detectTrigger, insertToken, type TriggerDescriptor } from '@/composables/useInputTriggers'
import { SLASH_COMMANDS } from '@/commands/slashCommands'
import type { FuzzySearchItem } from '@/composables/useFuzzyFilter'
import { showToast } from '@/composables/useToast'
import { uploadAttachments as apiUploadAttachments } from '@/api'

defineOptions({ name: 'MessageInput' })

const store = useChatStore()
const inputText = ref('')
const textareaRef = ref<HTMLTextAreaElement | null>(null)

// activeTrigger drives picker visibility. The caret-tracking handlers
// recompute it on every input/keyup/click, so it always reflects the
// latest textarea state.
const activeTrigger = ref<TriggerDescriptor | null>(null)

// ---- UI Parity PR2 B4 (May 2026) — prompt-history walk state ----
//
// historyCursor is the offset from the END of store.promptHistory. -1
// means "live buffer" (the user's current draft); 0 means "newest
// recorded prompt"; 1 means "second-newest"; etc. ArrowUp increments,
// ArrowDown decrements. liveDraft preserves whatever the user had typed
// before they started walking history so ArrowDown can restore it once
// they walk past the newest entry.
const historyCursor = ref<number>(-1)
const liveDraft = ref<string>('')

// ---- UI Parity PR2 B3 (May 2026) — attachment composer state ----
//
// pendingAttachments holds files the user has chosen via the picker,
// dragged onto the composer, or pasted from the clipboard. They are
// staged here until the user hits Send. The backend chat-attachment
// endpoint does not yet exist on `feature/vue-ui-rebase`; the upload
// path below stubs the POST and flags the gap.
// Chat Attachments Backend (May 2026) PR4 task-15 — extend the inline
// PendingAttachment shape with a kind discriminant. The frontend
// branches on this for the staged-attachment chip render (image
// thumbnail vs document file-icon) and for the picker `accept`
// attribute. The backend's per-file Kind discrimination (image vs
// document) is the authoritative source — this client-side flag is
// only for chip rendering. Default 'image' for backwards-compat with
// any historical caller staging an image without setting kind.
interface PendingAttachment {
  id: string
  file: File
  kind: 'image' | 'document'
  previewUrl: string | null
}
const pendingAttachments = ref<PendingAttachment[]>([])
const isDragging = ref(false)
let dragCounter = 0
const fileInputRef = ref<HTMLInputElement | null>(null)

// ---- UI Parity PR2 B5 (May 2026) — stop-generating button state ----
//
// The composer swaps Send → Stop when the active session is streaming
// (per-session, not the legacy flat flag). Clicking Stop calls the
// store's handleEscapeKey twice in quick succession so users do not
// have to remember the undiscoverable Esc-twice keybinding.
//
// Stays on FE-only streamingFor: current-session optimistic UI between
// chat-send resolve and long-poll attach. Child-session list surfaces
// (ChildSessionsPanel, SessionBrowser, SessionSwitcher) use backend-
// authoritative child.activeTurnId per Child Session Turn Registry plan
// (May 2026) §Item 3 + §R8. The composer's Send/Stop swap is a
// current-session affordance and must flip the instant the user clicks
// Send — waiting for the backend round-trip would feel laggy.
const streamingState = computed(() => store.streamingFor(store.currentSessionId))
const isStreamingNow = computed(
  () => streamingState.value.isStreaming || streamingState.value.isLoading,
)

// Static slash-command catalogue mirrored from the TUI registry. See
// web/src/commands/slashCommands.ts for the canonical source-of-truth
// notes. Mapped to FuzzySearchItem for the modal.
const commandItems = computed<FuzzySearchItem[]>(() =>
  SLASH_COMMANDS.map((cmd) => ({
    id: cmd.name,
    label: `/${cmd.name}`,
    meta: cmd.description,
  })),
)

// Combined agent + swarm surface for "@" mentions. Both slices flow
// from the chat store; swarms come from GET /api/swarms via
// chatStore.loadSwarms (Web Swarm Mention Parity, May 2026), agents
// from chatStore.loadAgents. The orchestrator-side ScanMentions path
// is unconditional for the API surface, so any @<swarm-id> typed here
// dispatches identically to the TUI's @-mention flow when the user
// presses Send.
const mentionItems = computed<FuzzySearchItem[]>(() => {
  const agents = store.availableAgentDetails.map<FuzzySearchItem>((agent) => ({
    id: agent.id,
    label: `@${agent.id}`,
    group: 'Agents',
    meta: agent.name,
  }))
  const swarms = store.swarms.map<FuzzySearchItem>((swarm) => ({
    id: swarm.id,
    label: `@${swarm.id}`,
    group: 'Swarms',
    meta: swarm.lead,
  }))
  return [...agents, ...swarms]
})

// Group label varies between the two pickers — slash commands have no
// group so they render as a flat list.
const slashOpen = computed(() => activeTrigger.value?.kind === 'slash')
const mentionOpen = computed(() => activeTrigger.value?.kind === 'mention')

// Initial-fragment seed pushed into the modal's search input so the
// user's typing-as-they-trigger filters the list immediately rather
// than only after they type inside the modal.
const initialQuery = computed(() => activeTrigger.value?.fragment ?? '')

function handleInput(): void {
  autoResize()
  recomputeTrigger()
  // Editing the textarea breaks the history walk — drop back to live mode.
  // The user has decided to compose something new; further ArrowUp
  // presses should start from the newest entry again.
  if (historyCursor.value !== -1) {
    historyCursor.value = -1
    liveDraft.value = ''
  }
}

function recomputeTrigger(): void {
  const el = textareaRef.value
  if (!el) {
    activeTrigger.value = null
    return
  }
  // Read straight from the DOM element instead of `inputText.value` —
  // when this fires inside the same input event as v-model's auto-
  // generated listener, the reactive ref may not have been assigned
  // yet. The element's `.value` is always authoritative.
  activeTrigger.value = detectTrigger(el.value, el.selectionStart ?? 0)
}

// UI Parity PR2 B4 — ArrowUp recall.
//
// Step the cursor toward older entries. At the top of the history we
// stop (the user has reached the oldest entry; no wraparound). The
// FIRST ArrowUp stashes the live draft so ArrowDown can restore it.
function walkHistoryUp(): boolean {
  const history = store.promptHistory
  if (history.length === 0) return false
  if (historyCursor.value === -1) {
    liveDraft.value = inputText.value
    historyCursor.value = 0
  } else if (historyCursor.value < history.length - 1) {
    historyCursor.value += 1
  } else {
    return true // already at oldest; consumed but no movement
  }
  applyHistorySnapshot()
  return true
}

// UI Parity PR2 B4 — ArrowDown forward-walk.
//
// Step toward newer entries. Walking past the newest entry restores the
// live draft and re-enters "live" mode.
function walkHistoryDown(): boolean {
  if (historyCursor.value === -1) return false
  if (historyCursor.value === 0) {
    historyCursor.value = -1
    inputText.value = liveDraft.value
    liveDraft.value = ''
  } else {
    historyCursor.value -= 1
    applyHistorySnapshot()
  }
  void nextTick(() => {
    autoResize()
    const el = textareaRef.value
    if (el) {
      const end = el.value.length
      el.selectionStart = end
      el.selectionEnd = end
    }
  })
  return true
}

function applyHistorySnapshot(): void {
  const history = store.promptHistory
  if (history.length === 0) return
  const idx = history.length - 1 - historyCursor.value
  inputText.value = history[idx] ?? ''
  void nextTick(() => {
    autoResize()
    const el = textareaRef.value
    if (el) {
      const end = el.value.length
      el.selectionStart = end
      el.selectionEnd = end
    }
  })
}

// Gate the ArrowUp recall: only consume the key when the caret is at
// the very start of the textarea (or the buffer is empty). Mid-text
// editing of multi-line prompts must still allow native ArrowUp to
// move the caret between lines.
function isAtBufferStart(el: HTMLTextAreaElement | null): boolean {
  if (!el) return false
  if (el.value.length === 0) return true
  return (el.selectionStart ?? 0) === 0 && (el.selectionEnd ?? 0) === 0
}

function isAtBufferEnd(el: HTMLTextAreaElement | null): boolean {
  if (!el) return false
  const end = el.value.length
  return (el.selectionStart ?? 0) === end && (el.selectionEnd ?? 0) === end
}

function handleKeydown(event: KeyboardEvent): void {
  // Esc closes any open picker without losing the buffer.
  if (event.key === 'Escape' && activeTrigger.value !== null) {
    event.preventDefault()
    activeTrigger.value = null
    return
  }

  // Arrow keys / Enter / Esc inside an open picker are owned by the
  // FuzzySearchModal's own document-level handler, so the textarea
  // stays out of the way. Submitting on Enter only fires when no
  // picker is open.
  if (activeTrigger.value !== null) {
    return
  }

  // UI Parity PR2 B4 — ArrowUp / ArrowDown history walk. Gated on
  // caret-at-edge so users typing in the middle of a multi-line
  // prompt keep native caret motion.
  if (event.key === 'ArrowUp' && isAtBufferStart(textareaRef.value)) {
    if (walkHistoryUp()) {
      event.preventDefault()
      return
    }
  }
  if (event.key === 'ArrowDown' && isAtBufferEnd(textareaRef.value) && historyCursor.value !== -1) {
    if (walkHistoryDown()) {
      event.preventDefault()
      return
    }
  }

  if (event.key === 'Enter' && !event.shiftKey && !event.altKey) {
    event.preventDefault()
    submit()
  }
}

function autoResize(): void {
  const el = textareaRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(el.scrollHeight, 200)}px`
}

async function submit(): Promise<void> {
  const text = inputText.value.trim()
  if (!text && pendingAttachments.value.length === 0) return
  // UI Parity bug-fix bundle (May 2026). P0-1: pre-fix a submit with
  // staged attachments and no text fell through `if (!text && no
  // attachments) return` (mixed predicate), invoked
  // uploadPendingAttachments which cleared the staged array, then
  // called store.sendMessage('') which silently early-returned (its
  // own !text gate) — attachments lost, no error, no toast. Block the
  // submit here with a user-visible toast so the user knows to add a
  // message; the attachments stay staged so the next send carries
  // them through.
  if (!text && pendingAttachments.value.length > 0) {
    showToast({
      title: 'Message required',
      message: 'Add a message to send your attachment.',
      variant: 'error',
      duration: 4000,
    })
    return
  }
  // Streaming Coherence Slice E (May 2026) — queued prompts. The
  // composer no longer bounces submit-while-streaming with a toast;
  // it forwards the prompt to sendMessage which routes it to the
  // session's queue. The QueuedPromptStrip renders the queued
  // entries between the thread and the composer; clicking X reverts
  // a prompt into the composer for edit-then-resend.
  //
  // Chat Attachments Backend PR1 (May 2026) — upload BEFORE sending so
  // the prompt arrives with attachment references already resolved.
  // Plan §6 task-05. On failure we keep the staged attachments and
  // refuse the send so the user can retry without re-staging.
  let uploadedIds: string[] = []
  if (pendingAttachments.value.length > 0) {
    try {
      uploadedIds = await uploadPendingAttachments()
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Upload failed.'
      showToast({
        title: 'Attachment upload failed',
        message,
        variant: 'error',
        duration: 5000,
      })
      return
    }
  }
  inputText.value = ''
  activeTrigger.value = null
  historyCursor.value = -1
  liveDraft.value = ''
  // UI Parity bug-fix bundle (May 2026). P0-2: revoke the blob: URLs
  // we created in stageFiles so the browser frees the underlying
  // resources. Pre-fix the array was cleared without revoking,
  // leaking ~one object per staged image until page unload.
  revokePreviewUrls(pendingAttachments.value)
  pendingAttachments.value = []
  if (uploadedIds.length > 0) {
    await store.sendMessage(text, { attachmentIds: uploadedIds })
  } else {
    await store.sendMessage(text)
  }
}

// UI Parity PR2 B5 — Stop-generating button.
//
// Clicking Stop fires handleEscapeKey twice in quick succession so the
// user does not have to remember the Esc-Esc chord. The store's
// handleEscapeKey is itself idempotent on the first press (just arms a
// 600ms window), so two synchronous calls translate to "arm then
// confirm" in one click.
async function handleStop(): Promise<void> {
  await store.handleEscapeKey()
  await store.handleEscapeKey()
}

// ---- UI Parity PR2 B3 — attachment handlers ----

function newAttachmentId(): string {
  return `att-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

// PR4 task-15: document chip displays a human-readable size next to
// the filename. Small files report KB, larger files MB; matches the
// shorthand format the rest of the app uses for file-byte reports.
function humanReadableSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function stageFiles(files: FileList | File[] | null | undefined): void {
  if (!files) return
  const list = Array.from(files)
  if (list.length === 0) return
  for (const file of list) {
    // Chat Attachments Backend (May 2026) PR4 task-15 — staged file
    // types: image/* (jpeg/png/gif/webp) and application/pdf only.
    // Anything else is silently skipped. The backend allow-list is
    // the authoritative gate — this client-side filter is a
    // friendly-UI nicety so the user never sees a 415 toast for an
    // obviously-wrong file (e.g. dropping a .docx onto the
    // composer).
    const isImage = file.type.startsWith('image/')
    const isPDF = file.type === 'application/pdf'
    if (!isImage && !isPDF) continue
    let previewUrl: string | null = null
    if (isImage) {
      // PDFs do not render a thumbnail — the chip uses a file-icon
      // badge instead. createObjectURL is reserved for image previews
      // so we never leak a blob: URL for PDFs (which would carry the
      // full byte payload in memory until revoke).
      try {
        previewUrl = URL.createObjectURL(file)
      } catch {
        previewUrl = null
      }
    }
    pendingAttachments.value.push({
      id: newAttachmentId(),
      file,
      kind: isPDF ? 'document' : 'image',
      previewUrl,
    })
  }
}

function handleFilePicker(event: Event): void {
  const target = event.target as HTMLInputElement
  stageFiles(target.files)
  // Reset so the user can re-pick the same file next time.
  target.value = ''
}

function removeAttachment(id: string): void {
  const found = pendingAttachments.value.find((a) => a.id === id)
  if (found?.previewUrl) {
    try {
      URL.revokeObjectURL(found.previewUrl)
    } catch {
      // Some test environments (jsdom) lack revokeObjectURL — non-fatal.
    }
  }
  pendingAttachments.value = pendingAttachments.value.filter((a) => a.id !== id)
}

// UI Parity bug-fix bundle (May 2026). P0-2: helper that revokes every
// blob: URL on a list of pending attachments. Used by both submit()
// (post-send cleanup) and onBeforeUnmount (component teardown cleanup);
// removeAttachment also revokes inline since it can short-circuit
// without iterating the whole list.
function revokePreviewUrls(list: PendingAttachment[]): void {
  for (const att of list) {
    if (!att.previewUrl) continue
    try {
      URL.revokeObjectURL(att.previewUrl)
    } catch {
      // jsdom and some test environments lack revokeObjectURL — non-fatal.
    }
  }
}

function handlePaste(event: ClipboardEvent): void {
  const items = event.clipboardData?.items
  if (!items) return
  const files: File[] = []
  for (let i = 0; i < items.length; i++) {
    const item = items[i]
    if (item.kind === 'file') {
      const file = item.getAsFile()
      // PR4 task-15: paste accepts the same shape as the file picker —
      // image/* and application/pdf. stageFiles enforces the same
      // filter so the gate stays single-source-of-truth.
      if (file && (file.type.startsWith('image/') || file.type === 'application/pdf')) {
        files.push(file)
      }
    }
  }
  if (files.length > 0) {
    event.preventDefault()
    stageFiles(files)
  }
}

function handleDragEnter(event: DragEvent): void {
  // Only react when the drag actually carries files — keeps the
  // overlay from flashing for ordinary text-selection drags.
  const types = event.dataTransfer?.types
  if (!types) return
  if (!Array.from(types).includes('Files')) return
  event.preventDefault()
  dragCounter += 1
  isDragging.value = true
}

function handleDragLeave(event: DragEvent): void {
  event.preventDefault()
  dragCounter = Math.max(0, dragCounter - 1)
  if (dragCounter === 0) isDragging.value = false
}

function handleDragOver(event: DragEvent): void {
  if (!event.dataTransfer) return
  if (Array.from(event.dataTransfer.types).includes('Files')) {
    event.preventDefault()
    event.dataTransfer.dropEffect = 'copy'
  }
}

function handleDrop(event: DragEvent): void {
  event.preventDefault()
  isDragging.value = false
  dragCounter = 0
  const files = event.dataTransfer?.files
  if (files && files.length > 0) {
    stageFiles(files)
  }
}

async function uploadPendingAttachments(): Promise<string[]> {
  // Chat Attachments Backend PR1 (May 2026) — closes the B3
  // silent-file-loss bug. Plan §6 task-05.
  //
  // The staged pendingAttachments slice carries the user-picked files;
  // we POST them to /api/v1/sessions/{id}/attachments which returns
  // metadata including the stable content-hash id. The caller threads
  // these ids onto the subsequent /messages call so the backend can
  // resolve them against the session's attachment store and the
  // provider can lift each into a native image content block.
  //
  // Error semantics:
  //  - Throws on any non-2xx response. The caller (submit) catches and
  //    surfaces a toast, leaving the pendingAttachments slice intact so
  //    the user can retry without re-staging.
  //  - No staged-attachment side-effects on failure.
  if (pendingAttachments.value.length === 0) {
    return []
  }
  const sessionId = store.currentSessionId
  if (!sessionId) {
    throw new Error('Cannot upload attachments without an active session.')
  }
  const files = pendingAttachments.value.map((a) => a.file)
  const uploaded = await apiUploadAttachments(sessionId, files)
  return uploaded.map((u) => u.id)
}

async function applySelection(item: FuzzySearchItem): Promise<void> {
  const trigger = activeTrigger.value
  if (!trigger) return
  // Reject cross-picker contamination: a slash-command item must not be
  // applied when the mention trigger is active, and vice versa. The hidden
  // picker (v-show, not v-if) can fire @select with its top item before the
  // visible picker fires, inserting the wrong token at the wrong position.
  if (trigger.kind === 'slash' && !item.label.startsWith('/')) return
  if (trigger.kind === 'mention' && !item.label.startsWith('@')) return
  const result = insertToken(inputText.value, trigger, item.label)
  inputText.value = result.text
  activeTrigger.value = null
  await nextTick()
  const el = textareaRef.value
  if (el) {
    el.focus()
    el.selectionStart = result.caret
    el.selectionEnd = result.caret
  }
}

function closePicker(): void {
  activeTrigger.value = null
}

// UI Parity bug-fix bundle (May 2026). P1-6: defensive drag-counter
// reset. When the user drags out of the browser window and drops the
// drag outside the viewport, Chrome on Linux/Win misses the final
// dragleave on the composer — dragCounter stays at 1 and the overlay
// becomes permanent. Window-level dragend / drop listeners reset the
// state defensively. They run even if the drag never actually entered
// the composer (cheap no-op when counter is already 0).
function handleWindowDragEnd(): void {
  dragCounter = 0
  isDragging.value = false
}

onMounted(() => {
  // Agents may not be loaded yet when the input mounts inside a fresh
  // chat session — kick it off so the @-picker has something to show.
  if (store.availableAgentDetails.length === 0) {
    void store.loadAgents()
  }
  window.addEventListener('dragend', handleWindowDragEnd)
  // Drop on the window (anywhere outside the composer) also unsticks
  // the overlay. Use capture so a drop on a child that stops
  // propagation still resets the composer's state defensively.
  window.addEventListener('drop', handleWindowDragEnd, { capture: true })
})

onBeforeUnmount(() => {
  // UI Parity bug-fix bundle (May 2026). P0-2: revoke any outstanding
  // preview URLs so navigating away does not leak the blobs.
  revokePreviewUrls(pendingAttachments.value)
  window.removeEventListener('dragend', handleWindowDragEnd)
  window.removeEventListener('drop', handleWindowDragEnd, { capture: true } as EventListenerOptions)
})

// Watch composerText so a revert-to-message action pre-fills the textarea.
// We consume the value immediately and reset it to '' so subsequent renders
// do not re-apply the same text.
watch(
  () => store.composerText,
  (text) => {
    if (text) {
      inputText.value = text
      store.composerText = ''
      void nextTick(() => {
        autoResize()
        textareaRef.value?.focus()
      })
    }
  },
)
</script>

<template>
  <div
    class="message-input-wrap"
    :class="{ 'is-dragging': isDragging }"
    data-testid="message-input-wrap"
    @dragenter="handleDragEnter"
    @dragleave="handleDragLeave"
    @dragover="handleDragOver"
    @drop="handleDrop"
  >
    <!--
      UI Parity PR2 B3 — drag-and-drop overlay. Mounted always but only
      visible when isDragging is true; keeps layout stable and avoids
      a mount-time flicker during the first drag.
    -->
    <div
      v-if="isDragging"
      class="message-input-drag-overlay"
      data-testid="message-input-drag-overlay"
      aria-hidden="true"
    >
      <Icon name="attach" :size="32" />
      <span>Drop image or PDF to attach</span>
    </div>

    <!--
      UI Parity PR2 B3 — staged attachments. Renders thumbnails for any
      image the user has chosen, dragged, or pasted. Each thumbnail has
      a remove button so the user can unstage before sending.
    -->
    <div
      v-if="pendingAttachments.length > 0"
      class="message-input-attachments"
      data-testid="message-input-attachments"
    >
      <div
        v-for="att in pendingAttachments"
        :key="att.id"
        class="message-input-attachment"
        :class="{ 'is-document': att.kind === 'document' }"
        :data-testid="`message-input-attachment-${att.id}`"
      >
        <!--
          PR4 task-15: branch on kind. Images keep the existing
          thumbnail; PDFs render a file-icon badge instead of an
          <img> (no thumbnail in v1 — preview rendering of PDF first
          page is a future PR). Filename + human-readable size flank
          the icon for documents so the user can confirm they staged
          the right file before sending.
        -->
        <img
          v-if="att.kind === 'image' && att.previewUrl"
          :src="att.previewUrl"
          :alt="att.file.name"
          class="message-input-attachment-thumb"
        />
        <span
          v-else-if="att.kind === 'document'"
          class="message-input-attachment-doc-icon"
          data-testid="message-input-attachment-doc-icon"
          aria-hidden="true"
        >
          <Icon name="attach" :size="24" />
        </span>
        <span class="message-input-attachment-name">{{ att.file.name }}</span>
        <span
          v-if="att.kind === 'document'"
          class="message-input-attachment-size"
          data-testid="message-input-attachment-size"
        >{{ humanReadableSize(att.file.size) }}</span>
        <button
          type="button"
          class="message-input-attachment-remove"
          :data-testid="`message-input-attachment-remove-${att.id}`"
          :aria-label="`Remove attachment ${att.file.name}`"
          @click="removeAttachment(att.id)"
        >
          <Icon name="close" :size="14" />
        </button>
      </div>
    </div>

    <div class="input-row">
      <!--
        UI Parity PR2 B3 — file picker button. Hidden file input + a
        visible attach-icon button that proxies clicks to it; cleaner
        than the native picker chrome and consistent with the rest of
        the composer's icon vocabulary.
      -->
      <button
        type="button"
        class="attach-button"
        data-testid="attach-button"
        aria-label="Attach image"
        title="Attach image"
        @click="fileInputRef?.click()"
      >
        <Icon name="attach" :size="18" />
      </button>
      <input
        ref="fileInputRef"
        type="file"
        accept="image/*,application/pdf"
        multiple
        class="file-input"
        data-testid="file-input"
        @change="handleFilePicker"
      />

      <textarea
        v-model="inputText"
        ref="textareaRef"
        class="message-input"
        data-testid="message-input"
        placeholder="Type a message… (Enter to send, Shift+Enter or Alt+Enter for newline)"
        rows="1"
        @input="handleInput"
        @keyup="recomputeTrigger"
        @click="recomputeTrigger"
        @keydown="handleKeydown"
        @paste="handlePaste"
      />

      <!--
        UI Parity PR2 B5 — Send/Stop swap. While the current session is
        actively streaming the composer renders a red Stop button instead
        of the dimmed Send button. Clicking Stop fires the cancel path
        directly; tooltip surfaces the discoverable Esc-Esc keybinding so
        keyboard users learn the chord.
      -->
      <button
        v-if="isStreamingNow"
        type="button"
        class="stop-button"
        data-testid="stop-button"
        title="Stop generating (Esc Esc to confirm)"
        aria-label="Stop generating"
        @click="handleStop"
      >
        <Icon name="stop" :size="14" />
        <span class="stop-button-label">Stop</span>
      </button>
      <button
        v-else
        class="send-button"
        data-testid="send-button"
        :disabled="!inputText.trim() && pendingAttachments.length === 0"
        @click="submit"
      >
        Send
      </button>
    </div>

    <p v-if="store.error" class="input-error" data-testid="chat-error">
      {{ store.error }}
    </p>

    <p class="input-hint">Enter to send · Shift+Enter / Alt+Enter for newline · ↑ history · "/" commands · "@" agents</p>

    <!--
      Slash and mention pickers reuse FuzzySearchModal — the same scaffolding
      that backs the toolbar AgentPicker / ModelPicker — so there is exactly
      one fuzzy-search overlay pattern in the app. Mutually exclusive: only
      one of slashOpen / mentionOpen is true at a time, driven by detectTrigger.
    -->
    <FuzzySearchModal
      :items="commandItems"
      :open="slashOpen"
      :initial-query="initialQuery"
      :focus-on-open="false"
      placeholder="Search commands..."
      empty-message="No matching commands"
      @select="applySelection"
      @close="closePicker"
    />

    <FuzzySearchModal
      :items="mentionItems"
      :open="mentionOpen"
      :initial-query="initialQuery"
      :focus-on-open="false"
      placeholder="Search agents and swarms..."
      empty-message="No matching agents or swarms"
      @select="applySelection"
      @close="closePicker"
    />
  </div>
</template>

<style scoped>
.message-input-wrap {
  padding: 0.75rem 1rem;
  border-top: 1px solid var(--border);
  background: var(--bg-secondary);
  flex-shrink: 0;
  position: relative;
}

.message-input-wrap.is-dragging {
  background: var(--accent-bg, rgba(74, 222, 128, 0.06));
}

.message-input-drag-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  background: rgba(0, 0, 0, 0.45);
  color: var(--text-primary);
  font-size: 0.95rem;
  font-weight: 600;
  border: 2px dashed var(--accent);
  border-radius: var(--radius);
  z-index: 10;
  pointer-events: none;
}

.input-row {
  display: flex;
  gap: 0.5rem;
  align-items: flex-end;
}

.attach-button {
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text-muted);
  border-radius: var(--radius);
  padding: 0.4rem 0.55rem;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
  align-self: flex-end;
  height: 38px;
}

.attach-button:hover {
  background: var(--bg-elevated);
  color: var(--text-primary);
  border-color: var(--accent);
}

.file-input {
  display: none;
}

.message-input {
  flex: 1;
  background: var(--bg-elevated);
  color: var(--text-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 0.5rem 0.75rem;
  font-family: var(--font-sans);
  font-size: 0.95rem;
  resize: none;
  line-height: 1.5;
  transition: border-color 0.15s;
  max-height: 200px;
  overflow-y: auto;
}

.message-input:focus {
  outline: none;
  border-color: var(--accent);
}

.send-button {
  background: var(--accent);
  color: var(--bg-primary);
  border: none;
  border-radius: var(--radius);
  padding: 0.5rem 1.25rem;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
  flex-shrink: 0;
  align-self: flex-end;
  height: 38px;
}

.send-button:hover:not(:disabled) { background: var(--accent-hover); }
.send-button:disabled { opacity: 0.4; cursor: not-allowed; }

/*
 * UI Parity PR2 B5 — Stop button styling. Red destructive accent + the
 * square stop icon makes the affordance unambiguous; the label remains
 * for clarity at the standard text colour. Animation matches the
 * streaming-dot pulse cadence elsewhere so the "something is happening"
 * vocabulary stays consistent.
 */
.stop-button {
  background: var(--accent-danger, #f87171);
  color: white;
  border: none;
  border-radius: var(--radius);
  padding: 0.5rem 1rem;
  font-weight: 600;
  cursor: pointer;
  transition: filter 0.15s;
  flex-shrink: 0;
  align-self: flex-end;
  height: 38px;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}

.stop-button:hover { filter: brightness(1.1); }
.stop-button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }

.stop-button-label {
  font-size: 0.9rem;
}

.input-error {
  color: var(--error);
  font-size: 0.8rem;
  margin-top: 0.25rem;
}

.input-hint {
  font-size: 0.72rem;
  color: var(--text-muted);
  margin-top: 0.25rem;
}

/*
 * UI Parity PR2 B3 — staged attachments strip. Renders above the input
 * row; thumbnails for image attachments with a remove button per item.
 */
.message-input-attachments {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
}

.message-input-attachment {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.4rem 0.25rem 0.25rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  max-width: 200px;
}

.message-input-attachment-thumb {
  width: 40px;
  height: 40px;
  object-fit: cover;
  border-radius: var(--radius);
}

.message-input-attachment-name {
  font-size: 0.78rem;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100px;
}

.message-input-attachment-remove {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius);
  padding: 0.15rem;
}

.message-input-attachment-remove:hover {
  background: rgba(248, 113, 113, 0.15);
  color: var(--accent-danger, #f87171);
}
</style>
