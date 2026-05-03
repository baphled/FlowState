<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute, RouterLink } from 'vue-router'
import { fetchAgent } from '@/api'
import type { Agent } from '@/types'

defineOptions({ name: 'AgentInfoView' })

const route = useRoute()
const agent = ref<Agent | null>(null)
const error = ref<string | null>(null)
const loading = ref(false)

async function load(id: string) {
  loading.value = true
  error.value = null
  agent.value = null
  try {
    agent.value = await fetchAgent(id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

onMounted(() => load(String(route.params.id)))
watch(
  () => route.params.id,
  (next) => {
    if (next) load(String(next))
  },
)
</script>

<template>
  <section class="agent-info" data-testid="agent-info-view">
    <header class="agent-info__header">
      <RouterLink to="/chat" class="agent-info__back">← back to chat</RouterLink>
      <h1 v-if="agent" class="agent-info__title">{{ agent.name || agent.id }}</h1>
    </header>

    <p v-if="loading" class="agent-info__status">Loading agent…</p>
    <p v-else-if="error" class="agent-info__status agent-info__status--error">{{ error }}</p>

    <article v-else-if="agent" class="agent-info__body">
      <dl class="agent-info__meta">
        <dt>ID</dt><dd>{{ agent.id }}</dd>
        <dt v-if="agent.version">Version</dt><dd v-if="agent.version">{{ agent.version }}</dd>
        <dt v-if="agent.model">Model</dt><dd v-if="agent.model">{{ agent.model }}</dd>
        <dt v-if="agent.provider">Provider</dt><dd v-if="agent.provider">{{ agent.provider }}</dd>
      </dl>

      <section v-if="agent.description" class="agent-info__section">
        <h2>Description</h2>
        <p>{{ agent.description }}</p>
      </section>

      <section v-if="agent.instructions" class="agent-info__section">
        <h2>Instructions</h2>
        <pre class="agent-info__instructions">{{ agent.instructions }}</pre>
      </section>

      <section v-if="agent.capabilities?.skills?.length" class="agent-info__section">
        <h2>Skills</h2>
        <ul class="agent-info__list">
          <li v-for="skill in agent.capabilities.skills" :key="skill">{{ skill }}</li>
        </ul>
      </section>

      <section v-if="agent.capabilities?.tools?.length" class="agent-info__section">
        <h2>Tools</h2>
        <ul class="agent-info__list">
          <li v-for="tool in agent.capabilities.tools" :key="tool">{{ tool }}</li>
        </ul>
      </section>
    </article>
  </section>
</template>

<style scoped>
.agent-info {
  padding: 1.5rem 2rem;
  max-width: 60rem;
  margin: 0 auto;
  font-family: var(--font-mono);
  color: var(--text-primary);
}

.agent-info__header {
  display: flex;
  align-items: baseline;
  gap: 1rem;
  margin-bottom: 1.5rem;
  border-bottom: 1px solid var(--border);
  padding-bottom: 0.75rem;
}

.agent-info__back {
  color: var(--text-muted);
  text-decoration: none;
  font-size: 0.85rem;
}

.agent-info__back:hover {
  color: var(--accent);
}

.agent-info__title {
  margin: 0;
  font-size: 1.25rem;
  color: var(--accent);
}

.agent-info__status {
  color: var(--text-muted);
  font-style: italic;
}

.agent-info__status--error {
  color: var(--error, #f7768e);
  font-style: normal;
}

.agent-info__meta {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.25rem 1rem;
  margin: 0 0 1.5rem 0;
  font-size: 0.85rem;
}

.agent-info__meta dt {
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  font-size: 0.7rem;
}

.agent-info__meta dd {
  margin: 0;
  color: var(--text-primary);
}

.agent-info__section {
  margin-bottom: 1.5rem;
}

.agent-info__section h2 {
  font-size: 0.85rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-secondary);
  margin: 0 0 0.5rem 0;
}

.agent-info__instructions {
  background: var(--bg-elevated, rgba(0, 0, 0, 0.2));
  padding: 0.75rem 1rem;
  border-radius: var(--radius);
  border: 1px solid var(--border);
  white-space: pre-wrap;
  font-size: 0.8rem;
  line-height: 1.5;
  margin: 0;
}

.agent-info__list {
  margin: 0;
  padding-left: 1.25rem;
  font-size: 0.85rem;
  line-height: 1.6;
}
</style>
