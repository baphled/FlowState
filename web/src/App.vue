<script setup lang="ts">
import { ref, onMounted } from 'vue'
import NavBar from '@/components/layout/NavBar.vue'
import ToastContainer from '@/components/common/ToastContainer.vue'

const apiOnline = ref(true)

onMounted(async () => {
  try {
    const res = await fetch('/api/health')
    apiOnline.value = res.ok
  } catch {
    apiOnline.value = false
  }
})
</script>

<template>
  <div class="app-shell">
    <NavBar />
    <div v-if="!apiOnline" class="api-offline-banner" data-testid="api-offline-banner">
      ⚠ FlowState API server is offline. Run <code>make web-server</code> in another terminal.
    </div>
    <main class="app-main">
      <RouterView />
    </main>
    <!--
      Global toast surface — used by chat for surfacing silent-drop
      rejections (the input-gate locked because a prior send is still
      in flight) and any future cross-cutting notifications. Mounted
      once at the app shell so every view shares the same overlay.
    -->
    <ToastContainer />
  </div>
</template>

<style scoped>
.app-shell {
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
  background: var(--bg-primary);
}

.api-offline-banner {
  background: #7c2d12;
  color: #fde8d0;
  font-size: 0.82rem;
  padding: 0.35rem 1rem;
  text-align: center;
  flex-shrink: 0;
}

.api-offline-banner code {
  font-family: var(--font-mono);
  background: rgba(0, 0, 0, 0.25);
  padding: 0.1rem 0.35rem;
  border-radius: 3px;
}

.app-main {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
</style>

