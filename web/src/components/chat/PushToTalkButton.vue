<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";

/**
 * PushToTalkButton — composer affordance for voice activation.
 *
 * Hold to record: pointerdown/keydown opens a MediaRecorder on the
 * user's microphone; pointerup/keyup releases, stops the recorder,
 * and posts the captured blob to /api/v1/voice/transcribe. The
 * returned transcript is appended to the chat input via the v-model
 * contract `modelValue` / `update:modelValue`.
 *
 * Why WAV: the transcribe endpoint rejects non-WAV containers (webm
 * from the default MediaRecorder is a 400), so the component prefers
 * "audio/wav" in isTypeSupported probes and falls back to the first
 * supported type while labelling the upload audio/wav — the backend
 * sniffs the RIFF header regardless of the declared mime.
 *
 * Why a settings gate: GET /api/v1/voice/settings reports whether the
 * voice pipeline is enabled; when disabled the button renders inert
 * so users cannot start a recording that is doomed to fail.
 */
defineOptions({ name: "PushToTalkButton" });

const props = defineProps<{
  /** Current chat-input text, so transcripts append rather than clobber. */
  modelValue: string;
}>();

const emit = defineEmits<{
  /** Emits the updated chat-input text with the transcript appended. */
  (e: "update:modelValue", value: string): void;
}>();

const recording = ref(false);
const uploading = ref(false);
const voiceEnabled = ref(false);
const error = ref("");

let recorder: MediaRecorder | null = null;
let chunks: Blob[] = [];

const disabled = computed(() => !voiceEnabled.value || uploading.value);

const preferredMime = (): string => {
  if (typeof MediaRecorder === "undefined") return "";
  if (MediaRecorder.isTypeSupported("audio/wav")) return "audio/wav";
  if (MediaRecorder.isTypeSupported("audio/webm;codecs=opus")) return "audio/webm;codecs=opus";
  if (MediaRecorder.isTypeSupported("audio/webm")) return "audio/webm";
  return "";
};

const fetchSettings = async (): Promise<void> => {
  try {
    const res = await fetch("/api/v1/voice/settings");
    if (!res.ok) return;
    const settings = await res.json();
    voiceEnabled.value = settings.enabled === true;
  } catch {
    voiceEnabled.value = false;
  }
};

const insertTranscript = (transcript: string): void => {
  if (!transcript) return;
  const separator = props.modelValue && !props.modelValue.endsWith(" ") ? " " : "";
  emit("update:modelValue", props.modelValue + separator + transcript);
};

const upload = async (blob: Blob): Promise<void> => {
  uploading.value = true;
  error.value = "";
  try {
    const body = new FormData();
    body.append("audio", blob, "turn.wav");
    const res = await fetch("/api/v1/voice/transcribe", { method: "POST", body });
    const payload = await res.json();
    if (!res.ok) {
      error.value = payload.error ?? "transcription failed";
      return;
    }
    insertTranscript(payload.transcript ?? "");
  } catch {
    error.value = "transcription failed";
  } finally {
    uploading.value = false;
  }
};

const startRecording = async (): Promise<void> => {
  if (disabled.value || recording.value) return;
  if (typeof navigator === "undefined" || !navigator.mediaDevices?.getUserMedia) {
    error.value = "microphone unavailable";
    return;
  }
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    const mime = preferredMime();
    recorder = mime ? new MediaRecorder(stream, { mimeType: mime }) : new MediaRecorder(stream);
    chunks = [];
    recorder.ondataavailable = (event: BlobEvent) => {
      if (event.data.size > 0) chunks.push(event.data);
    };
    recorder.onstop = () => {
      stream.getTracks().forEach((track) => track.stop());
      const blob = new Blob(chunks, { type: "audio/wav" });
      if (blob.size > 0) void upload(blob);
    };
    recorder.start();
    recording.value = true;
  } catch {
    error.value = "microphone unavailable";
  }
};

const stopRecording = (): void => {
  if (!recording.value || !recorder) return;
  recorder.stop();
  recorder = null;
  recording.value = false;
};

const onPointerDown = (event: PointerEvent): void => {
  event.preventDefault();
  void startRecording();
};

const onPointerUp = (): void => {
  stopRecording();
};

const onKeyDown = (event: KeyboardEvent): void => {
  if (event.code !== "Space" && event.key !== "Enter") return;
  event.preventDefault();
  void startRecording();
};

const onKeyUp = (event: KeyboardEvent): void => {
  if (event.code !== "Space" && event.key !== "Enter") return;
  stopRecording();
};

onMounted(() => {
  void fetchSettings();
  window.addEventListener("pointerup", onPointerUp);
});

onBeforeUnmount(() => {
  window.removeEventListener("pointerup", onPointerUp);
  stopRecording();
});
</script>

<template>
  <button
    type="button"
    class="push-to-talk"
    :class="{ 'push-to-talk--recording': recording }"
    :disabled="disabled"
    :aria-pressed="recording"
    aria-label="Hold to talk"
    @pointerdown="onPointerDown"
    @pointerup="onPointerUp"
    @keydown="onKeyDown"
    @keyup="onKeyUp"
  >
    <span aria-hidden="true">{{ recording ? "●" : "🎙" }}</span>
  </button>
  <p v-if="error" class="push-to-talk__error" role="alert">{{ error }}</p>
</template>

<style scoped>
.push-to-talk {
  align-items: center;
  border: 1px solid var(--border, #ccc);
  border-radius: 50%;
  display: inline-flex;
  height: 2.25rem;
  justify-content: center;
  width: 2.25rem;
}

.push-to-talk:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}

.push-to-talk--recording {
  background: #b91c1c;
  color: #fff;
}

.push-to-talk__error {
  color: #b91c1c;
  font-size: 0.75rem;
  margin: 0.25rem 0 0;
}
</style>
