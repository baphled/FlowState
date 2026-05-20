<script setup lang="ts">
import { computed } from "vue";
import { useRouter, useRoute } from "vue-router";
import SessionSwitcher from "@/components/session-switcher/SessionSwitcher.vue";
import { useChatStore } from "@/stores/chatStore";
// N7 (Vue UI Parity vs OpenCode, May 2026): the marketing mark in
// `src/assets/logo.svg` carries the FlowState wordmark + ribbon glyph.
// Vite resolves the import to a hashed asset URL at build time so the
// `<img>` src is cache-friendly and the SVG is HTTP-cached.
import logoUrl from "@/assets/logo.svg";

defineOptions({ name: "NavBar" });

const router = useRouter();
const route = useRoute();
const chatStore = useChatStore();

// Hide the entire NavBar in child sessions. The user's mental model: a
// child session is a focused, read-only view of delegated work — the
// chat/swarm tabs and the SessionSwitcher are global-navigation chrome
// that we explicitly don't want to surface there. Hierarchy navigation
// (ArrowUp/Left/Right) is wired at the document level inside ChatView and
// is unaffected by hiding this bar.
const isChildSession = computed(() => {
  const id = chatStore.currentSessionId;
  if (!id) return false;
  const current = chatStore.sessions.find((session) => session.id === id);
  return Boolean(current?.parentId);
});

const navItems = [
  { label: "Chat", path: "/chat", testId: "nav-chat" },
  { label: "Swarm", path: "/swarm", testId: "nav-swarm" },
  { label: "Settings", path: "/settings", testId: "nav-settings" },
];
</script>

<template>
  <nav v-if="!isChildSession" class="nav-bar" data-testid="nav-bar">
    <img
      class="nav-logo"
      :src="logoUrl"
      alt="FlowState"
      data-testid="nav-logo"
    />
    <div class="nav-switchers">
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
  /*
   * N7 — the SVG mark renders ~1.2rem high so it sits on the same
   * baseline as the nav items without dominating the bar. `currentColor`
   * inside the SVG inherits this element's `color`, so the glyph reads
   * the active theme's accent.
   */
  display: block;
  height: 1.2rem;
  width: auto;
  color: var(--accent);
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
  transition:
    color 0.15s,
    background 0.15s;
  font-size: 0.9rem;
}

.nav-item:hover,
.nav-item.active {
  color: var(--text-primary);
  background: var(--bg-elevated);
}

.nav-item.active {
  color: var(--accent);
}
</style>
