export interface ToolRendererProps {
  toolName: string
  heading: string
  body: string
  status?: 'pending' | 'running' | 'completed' | 'error'
  toolInput?: string
}
