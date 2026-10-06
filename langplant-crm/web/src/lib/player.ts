import { useSyncExternalStore } from 'react'
import { derivedUrl, fileUrl } from './api'
import type { Track } from './types'

// One global <audio> element: playback continues while navigating, and the
// Media Session API gives lock-screen controls on phones.

interface PlayerState {
  queue: Track[]
  index: number
  playing: boolean
  time: number
  duration: number
  loading: boolean
  error: string | null
}

let state: PlayerState = { queue: [], index: -1, playing: false, time: 0, duration: 0, loading: false, error: null }
const listeners = new Set<() => void>()
const set = (p: Partial<PlayerState>) => {
  state = { ...state, ...p }
  listeners.forEach((l) => l())
}

const audio = new Audio()
audio.preload = 'auto'
audio.addEventListener('play', () => set({ playing: true }))
audio.addEventListener('pause', () => set({ playing: false }))
audio.addEventListener('waiting', () => set({ loading: true }))
audio.addEventListener('playing', () => set({ loading: false, error: null }))
audio.addEventListener('canplay', () => set({ loading: false }))
// a seek requested before the track has loaded (e.g. a click on its waveform)
let pendingSeek: number | null = null
audio.addEventListener('loadedmetadata', () => {
  set({ duration: audio.duration * 1000 })
  if (pendingSeek != null) {
    audio.currentTime = pendingSeek * audio.duration
    set({ time: audio.currentTime * 1000 })
    pendingSeek = null
  }
})
let lastTick = 0
audio.addEventListener('timeupdate', () => {
  const now = performance.now()
  if (now - lastTick > 250) {
    lastTick = now
    set({ time: audio.currentTime * 1000 })
  }
})
audio.addEventListener('ended', () => next())
audio.addEventListener('error', () => {
  set({ loading: false, playing: false, error: 'Не удалось воспроизвести: хранилище офлайн или файл недоступен' })
})

export const current = () => (state.index >= 0 ? state.queue[state.index] : undefined)

function load(i: number, at: number | null = null) {
  const t = state.queue[i]
  if (!t) return
  pendingSeek = at
  set({ index: i, time: 0, duration: t.duration_ms ?? 0, loading: true, error: null })
  audio.src = fileUrl(t.sha256)
  audio.play().catch(() => set({ playing: false, loading: false }))
  if ('mediaSession' in navigator) {
    navigator.mediaSession.metadata = new MediaMetadata({
      title: t.title,
      artist: t.artist || 'LangPlant',
      album: t.album || 'Библиотека музыки',
      artwork: t.has_cover ? [{ src: derivedUrl(t.sha256, 'thumb.jpg'), sizes: '480x480', type: 'image/jpeg' }] : [],
    })
  }
}

/** Plays a track (or toggles it, if it is the current one). `at` starts it from that fraction. */
export function playTrack(track: Track, queue?: Track[], at?: number) {
  const q = queue ?? [track]
  const i = q.findIndex((t) => t.id === track.id)
  const cur = current()
  if (cur && cur.id === track.id) {
    if (at != null) {
      seek(at)
      if (audio.paused) audio.play().catch(() => {})
    } else toggle()
    return
  }
  state = { ...state, queue: q }
  load(i < 0 ? 0 : i, at ?? null)
}

export function toggle() {
  if (!current()) return
  if (audio.paused) audio.play().catch(() => {})
  else audio.pause()
}

export function next() {
  if (state.index < state.queue.length - 1) load(state.index + 1)
  else {
    audio.pause()
    set({ playing: false })
  }
}

export function prev() {
  if (audio.currentTime > 3 || state.index <= 0) {
    audio.currentTime = 0
  } else load(state.index - 1)
}

export function seek(fraction: number) {
  const f = Math.max(0, Math.min(1, fraction))
  if (!isFinite(audio.duration)) {
    pendingSeek = f
    return
  }
  audio.currentTime = f * audio.duration
  set({ time: audio.currentTime * 1000 })
}

let lastScrub = 0
/** Live seeking while the waveform is being dragged: throttled, so the audio follows without stuttering. */
export function scrub(fraction: number) {
  const now = performance.now()
  if (now - lastScrub < 120) return
  lastScrub = now
  seek(fraction)
}

export function stop() {
  audio.pause()
  audio.removeAttribute('src')
  audio.load()
  set({ queue: [], index: -1, playing: false, time: 0, duration: 0 })
}

export function usePlayer(): PlayerState & { track: Track | undefined } {
  const s = useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => state,
  )
  return { ...s, track: s.index >= 0 ? s.queue[s.index] : undefined }
}

if ('mediaSession' in navigator) {
  navigator.mediaSession.setActionHandler('play', () => audio.play())
  navigator.mediaSession.setActionHandler('pause', () => audio.pause())
  navigator.mediaSession.setActionHandler('nexttrack', () => next())
  navigator.mediaSession.setActionHandler('previoustrack', () => prev())
}
