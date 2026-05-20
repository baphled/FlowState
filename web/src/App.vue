<script setup lang="ts">
import { ref, onMounted } from "vue";
import NavBar from "@/components/layout/NavBar.vue";
import ToastContainer from "@/components/common/ToastContainer.vue";
import LoadingOverlay from "@/components/common/LoadingOverlay.vue";
import { useChatStore } from "@/stores/chatStore";

// appReady: drives the loading-overlay gate. Stays false until both
// async bootstrap promises (the /api/health probe and chatStore.bootstrap)
// have settled — success OR failure. The overlay covers the full
// viewport while false and the <RouterView /> is withheld from the DOM,
// so the user never sees a half-built page.
//
// Failure intentionally still flips appReady=true: a network blip during
// hydration must not strand the user behind a permanent splash. ChatView's
// onMounted catches the bootstrap rejection and surfaces an error toast
// (see ChatView.vue Principal F7), and the api-offline-banner below
// surfaces the health-check result.
const appReady = ref(false);
const apiOnline = ref(true);
const chatStore = useChatStore();

// UI Parity PR6 N10 (May 2026) — min-duration gate.
//
// A fast local backend (50–100ms typical) resolved bootstrap before the
// overlay had paid for itself, flashing visibly on every page load. The
// gate keeps the overlay hidden until a 200ms timer fires; if bootstrap
// settles first the overlay never shows at all. If the timer fires while
// bootstrap is still in flight the overlay reveals and stays up until
// appReady flips true. `overlayVisible` is the actual render gate; the
// existing `appReady` continues to gate the router view and downstream
// chrome (so the page underneath stays hidden during real slow boots).
const overlayVisible = ref(false);
const OVERLAY_MIN_DELAY_MS = 200;

onMounted(async () => {
  // Tear down the index.html splash so the Vue overlay can take over
  // without two opaque covers stacking. Removing it before flipping
  // appReady (which would then unmount LoadingOverlay too) keeps the
  // visual handover seamless: HTML splash → Vue overlay → app.
  const htmlSplash = document.getElementById("app-loading-splash");
  if (htmlSplash) htmlSplash.remove();

  // Schedule the overlay to appear only if bootstrap has not settled by
  // the time the gate fires. If bootstrap resolves first, the timer fires
  // into an already-ready app and the if-guard keeps the overlay hidden.
  const overlayTimer = setTimeout(() => {
    if (!appReady.value) {
      overlayVisible.value = true;
    }
  }, OVERLAY_MIN_DELAY_MS);

  // Kick off both bootstrap probes in parallel. Promise.allSettled so
  // either rejection still flips appReady — see the appReady comment.
  const healthPromise = (async () => {
    try {
      const res = await fetch("/api/health");
      apiOnline.value = res.ok;
    } catch {
      apiOnline.value = false;
    }
  })();
  await Promise.allSettled([healthPromise, chatStore.bootstrap()]);

  clearTimeout(overlayTimer);
  appReady.value = true;
  overlayVisible.value = false;
});
</script>

<template>
  <div class="app-shell">
    <LoadingOverlay v-if="overlayVisible" />
    <template v-if="appReady">
      <NavBar />
      <div
        v-if="!apiOnline"
        class="api-offline-banner"
        data-testid="api-offline-banner"
      >
        ⚠ FlowState API server is offline. Run <code>make web-server</code> in
        another terminal.
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
    </template>
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
