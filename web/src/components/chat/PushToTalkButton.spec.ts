import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import PushToTalkButton from "./PushToTalkButton.vue";

/**
 * PushToTalkButton specs — pin the push-to-talk contract:
 *
 *   - Disabled while /api/v1/voice/settings reports voice disabled.
 *   - Enabled when settings report voice enabled.
 *   - Hold (pointerdown) starts a MediaRecorder; release stops it and
 *     POSTs the captured WAV blob to /api/v1/voice/transcribe.
 *   - The returned transcript is emitted into the chat input via
 *     update:modelValue, appended after any existing text.
 */

interface FetchLog {
  url: string;
  init?: RequestInit;
  body?: FormData | null;
}

const fetchLog: FetchLog[] = [];

function stubFetch(responses: Record<string, unknown>): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      let body: FormData | null = null;
      if (init?.body instanceof FormData) body = init.body;
      fetchLog.push({ url, init, body });
      const payload = responses[url];
      if (payload === undefined) {
        return new Response(JSON.stringify({ error: "not found" }), { status: 404 });
      }
      return new Response(JSON.stringify(payload), { status: 200 });
    })
  );
}

class FakeMediaRecorder {
  static instances: FakeMediaRecorder[] = [];
  static supported: string[] = ["audio/wav"];
  mimeType: string;
  ondataavailable: ((event: { data: Blob }) => void) | null = null;
  onstop: (() => void) | null = null;
  private stopped = false;
  constructor(_stream: MediaStream, options?: { mimeType?: string }) {
    this.mimeType = options?.mimeType ?? "";
    FakeMediaRecorder.instances.push(this);
  }
  start(): void {}
  stop(): void {
    if (this.stopped) return;
    this.stopped = true;
    this.ondataavailable?.({ data: new Blob(["RIFFfakeWAV"], { type: "audio/wav" }) });
    this.onstop?.();
  }
  static isTypeSupported(type: string): boolean {
    return FakeMediaRecorder.supported.includes(type);
  }
}

class FakeTrack {
  stop = vi.fn();
}

class FakeStream {
  tracks = [new FakeTrack()];
  getTracks(): FakeTrack[] {
    return this.tracks;
  }
}

describe("PushToTalkButton", () => {
  beforeEach(() => {
    fetchLog.length = 0;
    FakeMediaRecorder.instances = [];
    vi.stubGlobal("MediaRecorder", FakeMediaRecorder);
    vi.stubGlobal("navigator", {
      mediaDevices: {
        getUserMedia: vi.fn(async () => new FakeStream()),
      },
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("is disabled when voice settings report disabled", async () => {
    stubFetch({ "/api/v1/voice/settings": { enabled: false } });
    const wrapper = mount(PushToTalkButton, { props: { modelValue: "" } });
    await flushPromises();
    expect(wrapper.get("button").attributes("disabled")).toBeDefined();
  });

  it("is enabled when voice settings report enabled", async () => {
    stubFetch({ "/api/v1/voice/settings": { enabled: true } });
    const wrapper = mount(PushToTalkButton, { props: { modelValue: "" } });
    await flushPromises();
    expect(wrapper.get("button").attributes("disabled")).toBeUndefined();
  });

  it("records on hold and posts the WAV blob on release", async () => {
    stubFetch({
      "/api/v1/voice/settings": { enabled: true },
      "/api/v1/voice/transcribe": { transcript: "hello from the spa" },
    });
    const wrapper = mount(PushToTalkButton, { props: { modelValue: "" } });
    await flushPromises();

    await wrapper.get("button").trigger("pointerdown", { pointerId: 1 });
    await flushPromises();
    expect(FakeMediaRecorder.instances).toHaveLength(1);
    expect(FakeMediaRecorder.instances[0].mimeType).toBe("audio/wav");

    wrapper.get("button").trigger("pointerup", { pointerId: 1 });
    await flushPromises();

    const upload = fetchLog.find((entry) => entry.url === "/api/v1/voice/transcribe");
    expect(upload).toBeDefined();
    expect(upload!.init?.method).toBe("POST");
    expect(upload!.body).toBeInstanceOf(FormData);

    const emitted = wrapper.emitted("update:modelValue");
    expect(emitted).toEqual([["hello from the spa"]]);
  });

  it("appends the transcript after existing chat input text", async () => {
    stubFetch({
      "/api/v1/voice/settings": { enabled: true },
      "/api/v1/voice/transcribe": { transcript: "more words" },
    });
    const wrapper = mount(PushToTalkButton, { props: { modelValue: "existing text" } });
    await flushPromises();

    await wrapper.get("button").trigger("pointerdown", { pointerId: 1 });
    await flushPromises();
    wrapper.get("button").trigger("pointerup", { pointerId: 1 });
    await flushPromises();

    const emitted = wrapper.emitted("update:modelValue");
    expect(emitted).toEqual([["existing text more words"]]);
  });
});
