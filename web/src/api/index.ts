import type { ChatRequest, ChatResponse, SwarmEvent } from '@/types'

const BASE = '/api'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, init)
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText })) as { error: string }
    throw new Error(err.error ?? `HTTP ${res.status}`)
  }
  return res.json() as Promise<T>
}

export async function fetchModels(): Promise<string[]> {
  return request<string[]>('/models')
}

export async function postChat(req: ChatRequest): Promise<ChatResponse> {
  return request<ChatResponse>('/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
}

export async function fetchSwarmEvents(): Promise<SwarmEvent[]> {
  return request<SwarmEvent[]>('/swarm/events')
}
