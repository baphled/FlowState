import BashTool from '@/components/tools/BashTool.vue'
import EditTool from '@/components/tools/EditTool.vue'
import GenericTool from '@/components/tools/GenericTool.vue'
import GlobTool from '@/components/tools/GlobTool.vue'
import GrepTool from '@/components/tools/GrepTool.vue'
import ReadTool from '@/components/tools/ReadTool.vue'
import WriteTool from '@/components/tools/WriteTool.vue'
import { getToolComponent, registerTool } from './toolRegistry'

function registerIfNeeded(name: string, component: typeof BashTool): void {
  if (getToolComponent(name) === component) {
    return
  }

  registerTool({ name, component })
}

export function registerTools(): void {
  registerIfNeeded('bash', BashTool)
  registerIfNeeded('read', ReadTool)
  registerIfNeeded('write', WriteTool)
  registerIfNeeded('edit', EditTool)
  registerIfNeeded('multiedit', EditTool)
  registerIfNeeded('apply_patch', EditTool)
  registerIfNeeded('glob', GlobTool)
  registerIfNeeded('list', GlobTool)
  registerIfNeeded('grep', GrepTool)
  registerIfNeeded('skill_load', GenericTool)
  registerIfNeeded('webfetch', GenericTool)
  registerIfNeeded('websearch', GenericTool)
  registerIfNeeded('task', GenericTool)
}
