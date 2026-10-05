import { useSyncExternalStore } from 'react'
import { api, ApiError } from './api'
import { audioPeaks, imageThumb, videoPoster } from './media'
import { queryClient } from './queries'

export interface UploadTarget {
  type: 'asset' | 'track'
  video_id?: number
  variant_id?: number | null
  kind?: string
  note?: string
  title?: string
  artist?: string
  album?: string
  bpm?: number | null
  musical_key?: string
  duration_ms?: number | null
  tags?: number[]
  source?: string
  license?: string
  license_url?: string
  platforms?: number[]
  notes?: string
}

export interface UploadResult {
  sha256: string
  duplicate: boolean
  asset_id?: number
  track_id?: number
}

export interface UploadItem {
  key: string
  file: File
  target: UploadTarget
  label: string
  status: 'queued' | 'uploading' | 'done' | 'error' | 'canceled'
  sent: number
  speed: number
  error?: string
  uploadId?: string
  result?: UploadResult
  cover?: Blob // ID3 cover art for tracks
}

type Listener = () => void
const listeners = new Set<Listener>()
let items: UploadItem[] = []
const xhrs = new Map<string, XMLHttpRequest>()
const MAX_PARALLEL = 2
let wakeLock: { release: () => Promise<void> } | null = null

function emit() {
  items = [...items]
  listeners.forEach((l) => l())
  updateWakeLock()
}
function update(key: string, patch: Partial<UploadItem>) {
  items = items.map((it) => (it.key === key ? { ...it, ...patch } : it))
  listeners.forEach((l) => l())
}

export function useUploads(): UploadItem[] {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => items,
  )
}

export const activeUploads = () => items.filter((i) => i.status === 'queued' || i.status === 'uploading')

export function enqueue(files: File[], target: UploadTarget | ((f: File) => UploadTarget), label: string | ((f: File) => string), extra?: (f: File) => Partial<UploadItem>) {
  for (const file of files) {
    const it: UploadItem = {
      key: Math.random().toString(36).slice(2) + Date.now().toString(36),
      file,
      target: typeof target === 'function' ? target(file) : target,
      label: typeof label === 'function' ? label(file) : label,
      status: 'queued',
      sent: 0,
      speed: 0,
      ...(extra?.(file) ?? {}),
    }
    items.push(it)
  }
  emit()
  pump()
}

export function cancel(key: string) {
  const it = items.find((i) => i.key === key)
  if (!it) return
  xhrs.get(key)?.abort()
  if (it.uploadId && it.status !== 'done') api(`/uploads/${it.uploadId}`, { method: 'DELETE' }).catch(() => {})
  forgetResume(it)
  update(key, { status: 'canceled' })
  pump()
}

export function retry(key: string) {
  update(key, { status: 'queued', error: undefined })
  pump()
}

export function clearFinished() {
  items = items.filter((i) => i.status === 'queued' || i.status === 'uploading' || i.status === 'error')
  emit()
}

function pump() {
  const running = items.filter((i) => i.status === 'uploading').length
  const next = items.filter((i) => i.status === 'queued').slice(0, Math.max(0, MAX_PARALLEL - running))
  for (const it of next) {
    update(it.key, { status: 'uploading' })
    run(it.key).finally(pump)
  }
  emit()
}

// --- resume across page reloads: the same file re-added continues where it stopped
const RESUME_KEY = 'crm.uploads'
const resumeId = (it: UploadItem) => `${it.file.name}|${it.file.size}|${it.file.lastModified}|${JSON.stringify(it.target)}`
function resumeMap(): Record<string, string> {
  try {
    return JSON.parse(localStorage.getItem(RESUME_KEY) || '{}')
  } catch {
    return {}
  }
}
function remember(it: UploadItem, id: string) {
  try {
    const m = resumeMap()
    m[resumeId(it)] = id
    localStorage.setItem(RESUME_KEY, JSON.stringify(m))
  } catch {
    /* storage unavailable */
  }
}
function forgetResume(it: UploadItem) {
  try {
    const m = resumeMap()
    delete m[resumeId(it)]
    localStorage.setItem(RESUME_KEY, JSON.stringify(m))
  } catch {
    /* storage unavailable */
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

async function run(key: string) {
  const get = () => items.find((i) => i.key === key)!
  let it = get()
  try {
    let id = resumeMap()[resumeId(it)]
    let offset = 0
    let chunk = 8 * 1024 * 1024
    if (id) {
      try {
        const st = await api<{ received: number; chunk_size: number }>(`/uploads/${id}`)
        offset = st.received
        chunk = st.chunk_size
      } catch {
        id = ''
      }
    }
    if (!id) {
      const res = await api<{ id: string; chunk_size: number }>('/uploads', {
        method: 'POST',
        body: { filename: it.file.name, size: it.file.size, mime: it.file.type, target: it.target },
      })
      id = res.id
      chunk = res.chunk_size
      remember(it, id)
    }
    update(key, { uploadId: id, sent: offset })

    let failures = 0
    let lastT = performance.now()
    let lastB = offset
    while (true) {
      it = get()
      if (it.status === 'canceled') return
      const end = Math.min(offset + chunk, it.file.size)
      try {
        const res = await putChunk(key, id, offset, it.file.slice(offset, end), (loaded) => {
          const now = performance.now()
          const sent = offset + loaded
          if (now - lastT > 700) {
            const speed = ((sent - lastB) / (now - lastT)) * 1000
            lastT = now
            lastB = sent
            update(key, { sent, speed: get().speed ? get().speed * 0.6 + speed * 0.4 : speed })
          } else {
            update(key, { sent })
          }
        })
        failures = 0
        offset = res.received
        update(key, { sent: offset })
        if (res.done) {
          forgetResume(it)
          update(key, { status: 'done', result: res.result, sent: it.file.size })
          afterUpload(get()).catch(() => {})
          return
        }
      } catch (e) {
        if (get().status === 'canceled') return
        const err = e as ApiError
        if (err.code === 'offset_mismatch' && typeof err.data.received === 'number') {
          offset = err.data.received as number
          continue
        }
        if (err.status === 404 && err.code === 'upload_not_found') {
          forgetResume(it)
          throw err
        }
        if (err.status >= 400 && err.status < 500 && err.code !== 'chunk_interrupted' && err.status !== 409 && err.status !== 429) throw err
        failures++
        if (failures > 12) throw new ApiError(0, 'network', 'Связь пропала. Нажмите «Повторить» — загрузка продолжится с места остановки')
        update(key, { error: 'Связь прервалась, повтор…' })
        await sleep(Math.min(30000, 1000 * 2 ** Math.min(failures, 5)))
        update(key, { error: undefined })
      }
    }
  } catch (e) {
    if (get()?.status !== 'canceled') update(key, { status: 'error', error: (e as Error).message })
  }
}

function putChunk(key: string, id: string, offset: number, blob: Blob, onProgress: (loaded: number) => void) {
  return new Promise<{ received: number; done: boolean; result?: UploadResult }>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhrs.set(key, xhr)
    xhr.open('PUT', `/api/uploads/${id}`)
    xhr.setRequestHeader('X-CRM', '1')
    xhr.setRequestHeader('Upload-Offset', String(offset))
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.upload.onprogress = (e) => onProgress(e.loaded)
    xhr.onload = () => {
      xhrs.delete(key)
      let data: Record<string, unknown> = {}
      try {
        data = JSON.parse(xhr.responseText || '{}')
      } catch {
        /* not json */
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as never)
      else reject(new ApiError(xhr.status, String(data.error ?? 'error'), String(data.message ?? `Ошибка ${xhr.status}`), data))
    }
    xhr.onerror = () => {
      xhrs.delete(key)
      reject(new ApiError(0, 'network', 'Нет связи'))
    }
    xhr.onabort = () => {
      xhrs.delete(key)
      reject(new ApiError(0, 'aborted', 'Отменено'))
    }
    xhr.send(blob)
  })
}

/** Posters, thumbnails and waveforms generated in the browser right after upload. */
async function afterUpload(it: UploadItem) {
  const sha = it.result?.sha256
  if (!sha) return
  const send = (name: string, blob: Blob) =>
    fetch(`/api/files/${sha}/client/${name}`, { method: 'PUT', headers: { 'X-CRM': '1', 'Content-Type': 'image/jpeg' }, body: blob })
  const type = it.file.type
  if (!it.result?.duplicate) {
    if (type.startsWith('video/')) {
      const p = await videoPoster(it.file)
      if (p) {
        await send('poster.jpg', p.poster)
        await send('thumb.jpg', p.thumb)
      }
    } else if (type.startsWith('image/')) {
      const t = await imageThumb(it.file)
      if (t) await send('thumb.jpg', t)
    } else if (type.startsWith('audio/') && it.target.type === 'track') {
      if (it.cover) {
        const t = await imageThumb(it.cover)
        if (t) await send('thumb.jpg', t)
      }
      const p = await audioPeaks(it.file)
      if (p) await api(`/files/${sha}/peaks`, { method: 'PUT', body: { peaks: p.peaks, duration_ms: Math.round(p.duration) } })
    }
  }
  queryClient.invalidateQueries({ queryKey: it.target.type === 'track' ? ['music'] : ['video', it.target.video_id] })
  queryClient.invalidateQueries({ queryKey: ['videos'] })
}

async function updateWakeLock() {
  const active = activeUploads().length > 0
  const nav = navigator as Navigator & { wakeLock?: { request: (t: 'screen') => Promise<{ release: () => Promise<void> }> } }
  try {
    if (active && !wakeLock && nav.wakeLock && document.visibilityState === 'visible') wakeLock = await nav.wakeLock.request('screen')
    if (!active && wakeLock) {
      await wakeLock.release()
      wakeLock = null
    }
  } catch {
    wakeLock = null
  }
}

window.addEventListener('beforeunload', (e) => {
  if (activeUploads().length > 0) {
    e.preventDefault()
    e.returnValue = ''
  }
})
