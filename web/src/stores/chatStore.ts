import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { postChat, fetchModels } from '@/api'
import type { Message } from '@/types'

export const useChatStore = defineStore('chat', () => {
  const messages = ref<Message[]>([])
  const isLoading = ref(false)
  const error = ref<string | null>(null)
  const model = ref('claude-sonnet-4.6')
  const availableModels = ref<string[]>([])

  const messageCount = computed(() => messages.value.length)

  async function loadModels(): Promise<void> {
    try {
      availableModels.value = await fetchModels()
      if (availableModels.value.length > 0 && !availableModels.value.includes(model.value)) {
        model.value = availableModels.value[0] ?? model.value
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load models'
    }
  }

  async function sendMessage(content: string): Promise<void> {
    if (!content.trim()) return

    const userMessage: Message = { role: 'user', content: content.trim() }
    messages.value.push(userMessage)
    isLoading.value = true
    error.value = null

    try {
      const response = await postChat({ messages: messages.value, model: model.value })
      messages.value.push({ role: 'assistant', content: response.content })
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Chat request failed'
      messages.value.pop()
    } finally {
      isLoading.value = false
    }
  }

  function clearMessages(): void {
    messages.value = []
    error.value = null
  }

  return { messages, isLoading, error, model, availableModels, messageCount, loadModels, sendMessage, clearMessages }
})
