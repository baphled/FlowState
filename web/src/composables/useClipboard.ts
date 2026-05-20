import { ref } from "vue";

export function useClipboard() {
  const copied = ref(false);
  const error = ref<string | null>(null);
  let timeoutId: ReturnType<typeof setTimeout> | null = null;

  async function copy(text: string): Promise<void> {
    error.value = null;

    try {
      await navigator.clipboard.writeText(text);
      copied.value = true;

      if (timeoutId) {
        clearTimeout(timeoutId);
      }

      timeoutId = setTimeout(() => {
        copied.value = false;
      }, 2000);
    } catch (e) {
      error.value = e instanceof Error ? e.message : "Failed to copy";
    }
  }

  function cleanup(): void {
    if (timeoutId) {
      clearTimeout(timeoutId);
      timeoutId = null;
    }
  }

  return { copy, copied, error, cleanup };
}
