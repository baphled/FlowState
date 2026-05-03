<script setup lang="ts">
import { useRouter, useRoute } from 'vue-router'
import AgentSwitcher from '@/components/agent-switcher/AgentSwitcher.vue'
import ModelSwitcher from '@/components/model-switcher/ModelSwitcher.vue'
import SessionSwitcher from '@/components/session-switcher/SessionSwitcher.vue'

defineOptions({ name: 'NavBar' })

const router = useRouter()
const route = useRoute()

const navItems = [
  { label: 'Chat', path: '/chat', testId: 'nav-chat' },
  { label: 'Swarm', path: '/swarm', testId: 'nav-swarm' },
  { label: 'Settings', path: '/settings', testId: 'nav-settings' },
]
</script>

<template>
  <nav class="nav-bar" data-testid="nav-bar">
    <span class="nav-logo">FlowState</span>
    <div class="nav-switchers">
      <AgentSwitcher />
      <SessionSwitcher />
    </div>
    <ul class="nav-items">
      <li
        v-for="item in navItems"
        :key="item.path"
        class="nav-item"
        :class="{ active: route.path.startsWith(item.path) }"
        :data-testid="item.testId"
        @click="router.push(item.path)"
      >
        {{ item.label }}
      </li>
    </ul>
    <div class="nav-model-switcher">
      <ModelSwitcher />
    </div>
  </nav>
</template>

<style scoped>
.nav-bar {
  display: flex;
  align-items: center;
  gap: 1.5rem;
  padding: 0.5rem 1rem;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

.nav-logo {
  font-family: var(--font-mono);
  font-weight: 700;
  color: var(--accent);
  font-size: 1.1rem;
  letter-spacing: 0.05em;
}

.nav-switchers {
  display: flex;
  gap: 0.5rem;
}

.nav-items {
  display: flex;
  gap: 0.5rem;
  list-style: none;
}

.nav-item {
  padding: 0.25rem 0.75rem;
  border-radius: var(--radius);
  cursor: pointer;
  color: var(--text-muted);
  transition: color 0.15s, background 0.15s;
  font-size: 0.9rem;
}

.nav-item:hover,
.nav-item.active {
  color: var(--text-primary);
  background: var(--bg-elevated);
}

.nav-model-switcher {
  margin-left: auto;
}

.nav-item.active {
  color: var(--accent);
}
</style>
