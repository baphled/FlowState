import { ref, type Ref } from "vue";
import { beforeEach, describe, expect, it } from "vitest";
import { useFuzzyFilter } from "./useFuzzyFilter";

interface FuzzySearchItem {
  id: string;
  label: string;
  group?: string;
  meta?: string;
}

const ITEMS: FuzzySearchItem[] = [
  { id: "1", label: "Claude Opus 4", group: "Anthropic", meta: "Most capable" },
  { id: "2", label: "Claude Sonnet 4", group: "Anthropic", meta: "Balanced" },
  { id: "3", label: "GPT-4o", group: "OpenAI", meta: "Fast" },
  { id: "4", label: "Ollama Llama 3", group: "Local", meta: "Local model" },
];

describe("useFuzzyFilter", () => {
  let items: Ref<FuzzySearchItem[]>;

  beforeEach(() => {
    items = ref<FuzzySearchItem[]>(structuredClone(ITEMS));
  });

  it("returns all items when query is empty", () => {
    const { filteredItems } = useFuzzyFilter(items);

    expect(filteredItems.value.length).toBe(ITEMS.length);
  });

  it("filters items by fuzzy matching on label", () => {
    const { filteredItems, setQuery } = useFuzzyFilter(items);
    setQuery("claude");

    const labels = filteredItems.value.map((item) => item.label);
    expect(labels).toContain("Claude Opus 4");
    expect(labels).toContain("Claude Sonnet 4");
    expect(labels).not.toContain("GPT-4o");
    expect(labels).not.toContain("Ollama Llama 3");
  });

  it("filters items by partial label match", () => {
    const { filteredItems, setQuery } = useFuzzyFilter(items);
    setQuery("gpt");

    const labels = filteredItems.value.map((item) => item.label);
    expect(labels).toContain("GPT-4o");
    expect(labels).not.toContain("Claude Opus 4");
  });

  it("returns empty results for query matching nothing", () => {
    const { filteredItems, setQuery } = useFuzzyFilter(items);
    setQuery("xyznonexistent");

    expect(filteredItems.value.length).toBe(0);
  });

  it("starts with highlightedIndex at 0", () => {
    const { highlightedIndex } = useFuzzyFilter(items);

    expect(highlightedIndex.value).toBe(0);
  });

  it("highlightNext moves the highlight down and wraps", () => {
    const { highlightedIndex, highlightNext } = useFuzzyFilter(items);

    highlightNext();
    expect(highlightedIndex.value).toBe(1);

    highlightNext();
    expect(highlightedIndex.value).toBe(2);

    highlightNext();
    expect(highlightedIndex.value).toBe(3);

    highlightNext();
    expect(highlightedIndex.value).toBe(0);
  });

  it("highlightPrev moves the highlight up and wraps", () => {
    const { highlightedIndex, highlightPrev } = useFuzzyFilter(items);

    highlightPrev();
    expect(highlightedIndex.value).toBe(3);

    highlightPrev();
    expect(highlightedIndex.value).toBe(2);
  });

  it("resetHighlight sets index to 0", () => {
    const { highlightedIndex, highlightNext, resetHighlight } =
      useFuzzyFilter(items);

    highlightNext();
    highlightNext();
    expect(highlightedIndex.value).toBe(2);

    resetHighlight();
    expect(highlightedIndex.value).toBe(0);
  });

  it("setQuery updates query and resets highlight", () => {
    const { query, highlightedIndex, setQuery, highlightNext } =
      useFuzzyFilter(items);

    highlightNext();
    highlightNext();
    expect(highlightedIndex.value).toBe(2);

    setQuery("test");
    expect(query.value).toBe("test");
    expect(highlightedIndex.value).toBe(0);
  });

  it("returns grouped items preserving original order when items have groups", () => {
    const { filteredItems } = useFuzzyFilter(items);

    const ids = filteredItems.value.map((item) => item.id);
    expect(ids).toEqual(["1", "2", "3", "4"]);
  });

  it("resets highlight when filtered results shrink below current index", () => {
    const { highlightedIndex, setQuery, highlightNext } = useFuzzyFilter(items);

    highlightNext();
    highlightNext();
    expect(highlightedIndex.value).toBe(2);

    setQuery("gpt");
    expect(highlightedIndex.value).toBe(0);
  });

  it("reactively updates filteredItems when source items change", () => {
    const { filteredItems } = useFuzzyFilter(items);

    expect(filteredItems.value.length).toBe(4);

    items.value = [{ id: "5", label: "New Model" }];
    expect(filteredItems.value.length).toBe(1);
    expect(filteredItems.value[0].label).toBe("New Model");
  });

  it("does not mutate the original items array", () => {
    const original = structuredClone(ITEMS);
    const { setQuery } = useFuzzyFilter(items);
    setQuery("claude");

    expect(items.value).toEqual(original);
  });
});
