import { createApp } from "vue";
import { createPinia } from "pinia";
import App from "./App.vue";
import router from "./router";
import { registerTools } from "@/tools/registerTools";
import "./assets/themes.css";

// Register tool renderers at module init — BEFORE the Vue app mounts.
//
// Why module-init (not ChatView.onMounted):
//
//   Vue's onMounted fires AFTER the component's initial template render.
//   MessageBubble children inside the chat thread mount as part of that
//   initial render. Each bubble's
//
//     const toolComponent = computed(() => getToolComponent(...) ?? GenericTool)
//
//   evaluates during the first patch, against the toolComponents Map. If
//   registration happens later in ChatView.onMounted the Map is still empty
//   on that first evaluation, and because the Map is a plain non-reactive
//   `new Map()` the computed never re-fires — every todowrite / todo_update
//   tool_result silently falls through to GenericTool, surfacing raw JSON
//   to the user. Driving registration before app.mount() guarantees every
//   MessageBubble's first computed reads the populated registry, without
//   needing to retrofit reactivity onto the registry itself.
//
//   registerTools() has zero Vue-app dependencies (no Pinia, no router, no
//   reactive state) — see web/src/tools/registerTools.ts — so it is safe
//   to call before createPinia / app.use(router).
registerTools();

const app = createApp(App);
app.use(createPinia());
app.use(router);
app.mount("#app");
