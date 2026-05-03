import { ref, type Ref } from 'vue'

export type ToastVariant = 'default' | 'success' | 'error' | 'loading'

export interface ToastAction {
  label: string
  onClick: () => void
}

export interface ToastOptions {
  message: string
  title?: string
  variant?: ToastVariant
  duration?: number
  action?: ToastAction
}

export interface Toast {
  id: number
  message: string
  title?: string
  variant: ToastVariant
  duration: number
  action?: ToastAction
}

const DEFAULT_DURATION = 3000

const toasts = ref<Toast[]>([])

let nextId = 0

function resolveOptions(options: ToastOptions | string): Toast {
  if (typeof options === 'string') {
    return {
      id: nextId++,
      message: options,
      variant: 'default',
      duration: DEFAULT_DURATION,
    }
  }

  const variant = options.variant ?? 'default'
  const duration =
    options.duration !== undefined
      ? options.duration
      : variant === 'loading'
        ? 0
        : DEFAULT_DURATION

  return {
    id: nextId++,
    message: options.message,
    title: options.title,
    variant,
    duration,
    action: options.action,
  }
}

const timers = new Map<number, ReturnType<typeof setTimeout>>()

function scheduleDismiss(toast: Toast): void {
  if (toast.duration <= 0) return

  const timer = setTimeout(() => {
    removeToast(toast.id)
  }, toast.duration)

  timers.set(toast.id, timer)
}

function clearTimer(id: number): void {
  const timer = timers.get(id)
  if (timer !== undefined) {
    clearTimeout(timer)
    timers.delete(id)
  }
}

function removeToast(id: number): void {
  clearTimer(id)
  const index = toasts.value.findIndex((t) => t.id === id)
  if (index !== -1) {
    toasts.value.splice(index, 1)
  }
}

function dismissAll(): void {
  for (const toast of toasts.value) {
    clearTimer(toast.id)
  }
  toasts.value.splice(0, toasts.value.length)
}

export function showToast(options: ToastOptions | string): void {
  const toast = resolveOptions(options)
  toasts.value.push(toast)
  scheduleDismiss(toast)
}

export function dismissToast(id: number): void {
  removeToast(id)
}

export function useToast() {
  return {
    toasts: toasts as Ref<Toast[]>,
    removeToast,
    dismissAll,
  }
}
