import { createRouter, createWebHistory } from 'vue-router'
import ChatView from '@/views/ChatView.vue'
import SwarmView from '@/views/SwarmView.vue'
import SettingsView from '@/views/SettingsView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/chat' },
    { path: '/chat', component: ChatView, name: 'chat' },
    { path: '/swarm', component: SwarmView, name: 'swarm' },
    { path: '/settings', component: SettingsView, name: 'settings' },
  ],
})

export default router
