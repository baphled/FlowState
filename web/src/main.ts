import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { useUIStore, detectCarbonylMode } from './stores/uiStore'
import './assets/themes.css'
import './assets/carbonyl.css'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
app.use(router)

// Carbonyl-mode bootstrap — see internal/carbonyl/run.go buildTargetURL
// for the Go side that appends `?carbonyl=1`. We resolve the flag once
// at boot, before mount, so:
//   - the body class is in place before any component's onMounted runs
//     (MessageInput keys initial focus off it);
//   - the Pinia store has the correct initial state for every later
//     consumer without watchers or race conditions.
// Browser-mode behaviour is unchanged: when the flag is absent both the
// class and the store flag stay at their false defaults.
if (detectCarbonylMode()) {
  document.body.classList.add('carbonyl-mode')
  useUIStore(pinia).setCarbonylMode(true)
}

app.mount('#app')
