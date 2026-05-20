import { ref, type Ref } from "vue";

export type ToastVariant = "default" | "success" | "error" | "loading";

export interface ToastAction {
  label: string;
  onClick: () => void;
}

export interface ToastOptions {
  message: string;
  title?: string;
  variant?: ToastVariant;
  duration?: number;
  action?: ToastAction;
}

export interface Toast {
  id: number;
  message: string;
  title?: string;
  variant: ToastVariant;
  duration: number;
  action?: ToastAction;
}

const DEFAULT_DURATION = 3000;

const toasts = ref<Toast[]>([]);

let nextId = 0;

function resolveOptions(options: ToastOptions | string): Toast {
  if (typeof options === "string") {
    return {
      id: nextId++,
      message: options,
      variant: "default",
      duration: DEFAULT_DURATION,
    };
  }

  const variant = options.variant ?? "default";
  const duration =
    options.duration !== undefined
      ? options.duration
      : variant === "loading"
        ? 0
        : DEFAULT_DURATION;

  return {
    id: nextId++,
    message: options.message,
    title: options.title,
    variant,
    duration,
    action: options.action,
  };
}

const timers = new Map<number, ReturnType<typeof setTimeout>>();

function scheduleDismiss(toast: Toast): void {
  if (toast.duration <= 0) return;

  const timer = setTimeout(() => {
    removeToast(toast.id);
  }, toast.duration);

  timers.set(toast.id, timer);
}

function clearTimer(id: number): void {
  const timer = timers.get(id);
  if (timer !== undefined) {
    clearTimeout(timer);
    timers.delete(id);
  }
}

function removeToast(id: number): void {
  clearTimer(id);
  const index = toasts.value.findIndex((t) => t.id === id);
  if (index !== -1) {
    toasts.value.splice(index, 1);
  }
}

function dismissAll(): void {
  for (const toast of toasts.value) {
    clearTimer(toast.id);
  }
  toasts.value.splice(0, toasts.value.length);
}

export function showToast(options: ToastOptions | string): number {
  const toast = resolveOptions(options);
  toasts.value.push(toast);
  scheduleDismiss(toast);
  return toast.id;
}

export function dismissToast(id: number): void {
  removeToast(id);
}

/**
 * updateToast — patch a live toast in place.
 *
 * Used by aggregating notifiers (e.g. the tool-activity rolling toast in
 * chatStore.handleToolCallEvent) so a flurry of events updates a single
 * toast rather than spawning a parallel toast per event. The toast keeps
 * its id and DOM position; the auto-dismiss timer is rescheduled if the
 * patch supplies a new `duration`.
 *
 * Returns true if the toast was found and patched, false otherwise — a
 * caller can use the return to detect "the toast already auto-dismissed,
 * spawn a fresh one" without racing the timer module.
 *
 * Patchable fields: message, title, variant, duration, action. The id is
 * intentionally immutable — the whole point of this API is that a stale
 * external reference still resolves to the same toast.
 */
export function updateToast(
  id: number,
  patch: Partial<Omit<Toast, "id">>,
): boolean {
  const toast = toasts.value.find((t) => t.id === id);
  if (!toast) return false;

  if (patch.message !== undefined) toast.message = patch.message;
  if (patch.title !== undefined) toast.title = patch.title;
  if (patch.variant !== undefined) toast.variant = patch.variant;
  if (patch.action !== undefined) toast.action = patch.action;

  if (patch.duration !== undefined) {
    toast.duration = patch.duration;
    clearTimer(id);
    scheduleDismiss(toast);
  }

  return true;
}

export function useToast() {
  return {
    toasts: toasts as Ref<Toast[]>,
    removeToast,
    dismissAll,
  };
}
