import { computed, ref, watch, type ComputedRef, type Ref } from "vue";
import fuzzysort from "fuzzysort";

export interface FuzzySearchItem {
  id: string;
  label: string;
  group?: string;
  meta?: string;
}

export function useFuzzyFilter(items: Ref<FuzzySearchItem[]>) {
  const query = ref("");
  const highlightedIndex = ref(0);

  const filteredItems: ComputedRef<FuzzySearchItem[]> = computed(() => {
    const source = items.value;
    if (query.value.trim() === "") {
      return source;
    }

    const results = fuzzysort.go(query.value, source, { keys: ["label"] });

    return results.map((result) => result.obj);
  });

  watch(filteredItems, (filtered) => {
    if (highlightedIndex.value >= filtered.length) {
      highlightedIndex.value = 0;
    }
  });

  function highlightNext(): void {
    const len = filteredItems.value.length;
    if (len === 0) return;
    highlightedIndex.value = (highlightedIndex.value + 1) % len;
  }

  function highlightPrev(): void {
    const len = filteredItems.value.length;
    if (len === 0) return;
    highlightedIndex.value = (highlightedIndex.value - 1 + len) % len;
  }

  function resetHighlight(): void {
    highlightedIndex.value = 0;
  }

  function setQuery(q: string): void {
    query.value = q;
    resetHighlight();
  }

  return {
    query,
    filteredItems,
    highlightedIndex,
    highlightNext,
    highlightPrev,
    resetHighlight,
    setQuery,
  };
}
