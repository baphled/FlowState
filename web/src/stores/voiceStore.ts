import { defineStore } from 'pinia'

// Voice wiring for the web chat (chain voice-web-1, May 2026 — actually
// Sep 2026). Two surfaces, both server-ready per internal/api/voice_fe.go:
//
//   1. Agent-speaks-first — after a turn's stream completes, collect the
//      assistant text + tool_call rows, build a speakable narration
//      (mirroring the backend rule: NEVER speak raw tool payloads; tool
//      calls become short verb phrases), POST {"text"} to
//      /api/v1/voice/tts (64KiB cap, audio/wav response) and play it via
//      an Audio element. Gated on the `enabled` flag from
//      GET /api/v1/voice/settings. Every failure is silent/non-blocking —
//      voice is an augmentation, never a chat-breaking path.
//
//   2. User talk-back — MediaRecorder mic capture, converted to WAV
//      client-side (the server REJECTS webm/opus with 400; RIFF header is
//      checked), POSTed as multipart to /api/v1/voice/transcribe (10MiB
//      cap), and the transcript is dispatched through the existing
//      chatStore.sendMessage path via a DYNAMIC import (avoids a static
//      chatStore ⇄ voiceStore import cycle; chatStore statically imports
//      this module for speak-on-turn-complete).
//
// No new dependencies: fetch, MediaRecorder, AudioContext, Audio only.

/** Mirrors voice.Settings JSON from GET /api/v1/voice/settings. */
export interface VoiceSettings {
  enabled: boolean
  tts_model: string
  length_scale: number
  noise_scale: number
  sentence_silence: number
}

/** Server-side caps — keep the client honest before the 413 does. */
export const MAX_TTS_TEXT_BYTES = 64 * 1024
export const MAX_UPLOAD_BYTES = 10 * 1024 * 1024

/** A minimal projection of Message rows we narrate. Keeps voiceStore
 * decoupled from the full chatStore Message type (and its @/types import,
 * which lives in the separate flowstate-web repo). */
export interface NarratableRow {
  id: string
  role: string
  content?: string
  toolName?: string
}

/**
 * describeToolForNarration — client-side mirror of the backend narration
 * rule: describe the tool by NAME only, never its input payload. Verb
 * phrases ("checking files", "running a build command") read naturally
 * when spoken. Falls back to the humanised raw name.
 */
export function describeToolForNarration(rawName: string): string {
  const key = rawName.trim().toLowerCase()
  switch (key) {
    case 'bash':
    case 'shell':
    case 'terminal':
      return 'running a command'
    case 'read':
    case 'view':
      return 'checking a file'
    case 'edit':
    case 'multiedit':
    case 'str_replace_editor':
      return 'editing a file'
    case 'write':
    case 'create_file':
      return 'writing a file'
    case 'grep':
    case 'search':
    case 'glob':
    case 'find':
      return 'checking files'
    case 'webfetch':
    case 'web_fetch':
    case 'fetch':
      return 'fetching a web page'
    case 'websearch':
    case 'web_search':
      return 'searching the web'
    case 'task':
    case 'agent':
    case 'delegate':
      return 'delegating to an agent'
    case 'todowrite':
    case 'todo_write':
    case 'update_todos':
      return 'updating the task list'
    default:
      return `running ${rawName.replace(/_/g, ' ')}`
  }
}

/**
 * buildNarration — assembles the speakable string for a completed turn.
 * Rules:
 *   - assistant text is kept (code fences stripped, inline code kept).
 *   - tool_call rows contribute ONLY their described verb phrase; the
 *     raw `input` payload is never spoken (mirrors PreprocessTTS-side
 *     speech-safety; we enforce it client-side so the POSTed text is
 *     already clean).
 *   - thinking / tool_result / tool_error / delegation rows are skipped.
 * Returns '' when there is nothing worth speaking — the caller treats
 * that as a no-op.
 */
export function buildNarration(rows: NarratableRow[]): string {
  const parts: string[] = []
  for (const row of rows) {
    if (!row) continue
    if (row.role === 'tool_call') {
      if (row.toolName) parts.push(describeToolForNarration(row.toolName))
      continue
    }
    if (row.role !== 'assistant') continue
    const text = stripCodeFences(row.content ?? '')
    if (text.trim()) parts.push(text.trim())
  }
  return parts.join('. ')
}

/** Removes fenced blocks entirely, keeps inline-code inner text — the
 * same shape as the backend's codeFenceRe/inlineCodeRe pair, applied
 * before the wire so the narration reads clean even before server-side
 * PreprocessTTS runs. */
export function stripCodeFences(text: string): string {
  return text
    .replace(/```[\s\S]*?```/g, '')
    .replace(/~~~[\s\S]*?~~~/g, '')
    .replace(/`([^`]*)`/g, '$1')
}

/** encodeWav — PCM16 WAV (RIFF) encoder over decoded AudioBuffer data.
 * The transcribe endpoint rejects anything without a RIFF+WAVE header
 * (webm/opus from MediaRecorder → 400 invalid_request), so capture is
 * always routed through AudioContext.decodeAudioData then this encoder.
 * Mixes down to mono and caps at 16 kHz to keep uploads well under the
 * 10MiB limit for minutes-long dictation. */
export function encodeWav(buffer: AudioBuffer): Blob {
  const targetRate = 16000
  const channels = buffer.numberOfChannels
  const srcLen = buffer.length
  const srcRate = buffer.sampleRate
  // Linear resample to 16 kHz mono.
  const ratio = srcRate / targetRate
  const outLen = Math.max(1, Math.floor(srcLen / ratio))
  const mono = new Float32Array(outLen)
  for (let ch = 0; ch < channels; ch++) {
    const data = buffer.getChannelData(ch)
    for (let i = 0; i < outLen; i++) {
      const src = Math.min(srcLen - 1, Math.floor(i * ratio))
      mono[i] += data[src] / channels
    }
  }
  const bytesPerSample = 2
  const dataSize = outLen * bytesPerSample
  const view = new DataView(new ArrayBuffer(44 + dataSize))
  const writeStr = (offset: number, s: string): void => {
    for (let i = 0; i < s.length; i++) view.setUint8(offset + i, s.charCodeAt(i))
  }
  writeStr(0, 'RIFF')
  view.setUint32(4, 36 + dataSize, true)
  writeStr(8, 'WAVE')
  writeStr(12, 'fmt ')
  view.setUint32(16, 16, true) // PCM chunk size
  view.setUint16(20, 1, true) // PCM format
  view.setUint16(22, 1, true) // mono
  view.setUint32(24, targetRate, true)
  view.setUint32(28, targetRate * bytesPerSample, true)
  view.setUint16(32, bytesPerSample, true)
  view.setUint16(34, 16, true) // bits per sample
  writeStr(36, 'data')
  view.setUint32(40, dataSize, true)
  let offset = 44
  for (let i = 0; i < outLen; i++) {
    const s = Math.max(-1, Math.min(1, mono[i]))
    view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7fff, true)
    offset += bytesPerSample
  }
  return new Blob([view.buffer], { type: 'audio/wav' })
}

export const useVoiceStore = defineStore('voice', {
  state: () => ({
    /** null = not yet fetched; false = disabled or settings endpoint
     * unavailable (501 voice_not_wired) — either way, no speech. */
    ttsEnabled: null as boolean | null,
    isSpeaking: false,
    isRecording: false,
    isTranscribing: false,
    /** Last user-facing talk-back failure, if any (UI may toast; the
     * store itself never throws out of these actions). */
    voiceError: '' as string,
    _settingsLoaded: false,
    _mediaStream: null as MediaStream | null,
    _recorder: null as MediaRecorder | null,
    _chunks: [] as Blob[],
    _audioEl: null as HTMLAudioElement | null,
  }),

  actions: {
    /** Fetch GET /api/v1/voice/settings once and cache the enabled flag.
     * Any failure (404/501/network) silently disables TTS. */
    async loadVoiceSettings(): Promise<void> {
      if (this._settingsLoaded) return
      try {
        const res = await fetch('/api/v1/voice/settings')
        if (!res.ok) {
          this.ttsEnabled = false
        } else {
          const settings = (await res.json()) as VoiceSettings
          this.ttsEnabled = settings.enabled === true
        }
      } catch {
        this.ttsEnabled = false
      } finally {
        this._settingsLoaded = true
      }
    },

    /**
     * speakAgentTurn — agent-speaks-first entry point. chatStore calls
     * this fire-and-forget once a turn reaches terminal state and the
     * rows are reconciled. Silent on every failure.
     */
    async speakAgentTurn(rows: NarratableRow[]): Promise<void> {
      try {
        await this.loadVoiceSettings()
        if (!this.ttsEnabled) return
        const text = buildNarration(rows)
        if (!text.trim()) return
        // 64KiB cap — slice by characters (defence-in-depth; server
        // enforces bytes with 413 too_large).
        const bounded = Array.from(text).slice(0, MAX_TTS_TEXT_BYTES).join('')
        const res = await fetch('/api/v1/voice/tts', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ text: bounded }),
        })
        if (!res.ok) return
        const blob = await res.blob()
        if (blob.size === 0) return
        await this.playWav(blob)
      } catch {
        // Non-blocking by contract: voice failures never surface into
        // the chat error channel.
      }
    },

    /** Play a WAV blob via an Audio element (simplest no-dep path;
     * object URL revoked after playback ends or errors). */
    async playWav(blob: Blob): Promise<void> {
      this.stopSpeaking()
      const url = URL.createObjectURL(blob)
      const audio = new Audio(url)
      this._audioEl = audio
      this.isSpeaking = true
      return new Promise<void>((resolve) => {
        const done = (): void => {
          URL.revokeObjectURL(url)
          this.isSpeaking = false
          if (this._audioEl === audio) this._audioEl = null
          resolve()
        }
        audio.onended = done
        audio.onerror = done
        audio.play().catch(done)
      })
    },

    stopSpeaking(): void {
      if (this._audioEl) {
        this._audioEl.pause()
        this._audioEl = null
      }
      this.isSpeaking = false
    },

    /**
     * startRecording — request the mic and begin MediaRecorder capture.
     * Sets voiceError (never throws) on denial/failure.
     */
    async startRecording(): Promise<void> {
      if (this.isRecording) return
      this.voiceError = ''
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
        const recorder = new MediaRecorder(stream)
        this._chunks = []
        recorder.ondataavailable = (e: BlobEvent): void => {
          if (e.data && e.data.size > 0) this._chunks.push(e.data)
        }
        recorder.start()
        this._mediaStream = stream
        this._recorder = recorder
        this.isRecording = true
      } catch {
        this.voiceError = 'Microphone unavailable'
      }
    },

    /**
     * stopRecordingAndTranscribe — stop capture, convert the recorded
     * webm/opus chunks to WAV via AudioContext (the server rejects
     * non-RIFF bodies with 400), upload to /api/v1/voice/transcribe,
     * and dispatch the transcript through chatStore.sendMessage via a
     * dynamic import (breaks the static import cycle).
     */
    async stopRecordingAndTranscribe(): Promise<void> {
      if (!this.isRecording || !this._recorder) {
        this.isRecording = false
        return
      }
      const recorder = this._recorder
      const stream = this._mediaStream
      this._recorder = null
      this._mediaStream = null
      this.isRecording = false

      const chunks = await new Promise<Blob[]>((resolve) => {
        recorder.onstop = (): void => resolve([...this._chunks])
        recorder.stop()
      })
      stream?.getTracks().forEach((t) => t.stop())
      if (chunks.length === 0) return

      this.isTranscribing = true
      try {
        const ctx = new AudioContext()
        try {
          const raw = new Blob(chunks, { type: chunks[0]?.type || 'audio/webm' })
          const decoded = await ctx.decodeAudioData(await raw.arrayBuffer())
          const wav = encodeWav(decoded)
          if (wav.size > MAX_UPLOAD_BYTES) {
            this.voiceError = 'Recording too long'
            return
          }
          const form = new FormData()
          form.append('audio', wav, 'speech.wav')
          const res = await fetch('/api/v1/voice/transcribe', {
            method: 'POST',
            body: form,
          })
          if (!res.ok) {
            this.voiceError = 'Transcription failed'
            return
          }
          const { transcript } = (await res.json()) as { transcript?: string }
          const text = (transcript ?? '').trim()
          if (!text) return
          // Dispatch as a NORMAL user message through the existing send
          // path — voice input is indistinguishable from typed input.
          const { useChatStore } = await import('./chatStore')
          await useChatStore().sendMessage(text)
        } finally {
          void ctx.close()
        }
      } catch {
        this.voiceError = 'Voice input failed'
      } finally {
        this.isTranscribing = false
      }
    },

    cancelRecording(): void {
      if (this._recorder && this._recorder.state !== 'inactive') {
        try {
          this._recorder.stop()
        } catch {
          // already stopped
        }
      }
      this._mediaStream?.getTracks().forEach((t) => t.stop())
      this._recorder = null
      this._mediaStream = null
      this._chunks = []
      this.isRecording = false
    },
  },
})
