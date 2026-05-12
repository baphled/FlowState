declare module '*.vue' {
  import type { DefineComponent } from 'vue'

  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>

  export default component
}

// N7 (Vue UI Parity vs OpenCode, May 2026) — SVG asset URLs. Vite
// resolves `.svg` imports to a hashed asset URL at build time (data:
// URL inside vitest's transform). Declare the module so TypeScript
// recognises the import as a string.
declare module '*.svg' {
  const src: string
  export default src
}

// Vite `?raw` query — import a module's source as a string. Used by
// SFC tests that need to assert against the file's CSS without
// computed-style resolution (jsdom does not resolve CSS variables).
declare module '*?raw' {
  const content: string
  export default content
}
