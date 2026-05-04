import { markRaw, type Component } from 'vue'

const toolComponents = new Map<string, Component>()

export const CONTEXT_TOOLS = Object.freeze(['read', 'glob', 'grep', 'list'] as const)

export function registerTool({ name, component }: { name: string; component: Component }): void {
  toolComponents.set(name, markRaw(component))
}

export function getToolComponent(name: string): Component | undefined {
  return toolComponents.get(name)
}

export function isContextTool(name: string): boolean {
  return CONTEXT_TOOLS.includes(name as (typeof CONTEXT_TOOLS)[number])
}
