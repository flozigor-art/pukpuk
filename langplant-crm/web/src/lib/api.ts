export class ApiError extends Error {
  status: number
  code: string
  data: Record<string, unknown>
  constructor(status: number, code: string, message: string, data: Record<string, unknown> = {}) {
    super(message)
    this.status = status
    this.code = code
    this.data = data
  }
}

let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

/** A change recorded in the undo history, announced by the X-Undo-* headers. */
export interface UndoStepRef {
  id: number
  label: string
  merged: boolean
}

let onUndoStep: ((step: UndoStepRef, label?: string) => void) | null = null
export function setUndoStepHandler(fn: (step: UndoStepRef, label?: string) => void) {
  onUndoStep = fn
}

export interface ApiOpts {
  method?: string
  body?: unknown
  signal?: AbortSignal
  /** Text for the "undo" toast instead of the server's label. */
  undoLabel?: string
  /** Called when the request created (or extended) an undo step. */
  onUndoStep?: (step: UndoStepRef) => void
}

export async function api<T = unknown>(path: string, opts: ApiOpts = {}): Promise<T> {
  const method = opts.method ?? 'GET'
  const headers: Record<string, string> = {}
  if (method !== 'GET') headers['X-CRM'] = '1'
  let body: BodyInit | undefined
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }
  let res: Response
  try {
    res = await fetch('/api' + path, { method, headers, body, credentials: 'same-origin', signal: opts.signal })
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
    throw new ApiError(0, 'network', 'Нет связи с сервером')
  }
  const text = await res.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = { message: text }
    }
  }
  if (!res.ok) {
    const d = (data ?? {}) as Record<string, unknown>
    if (res.status === 401 && path !== '/auth/login') onUnauthorized?.()
    throw new ApiError(res.status, String(d.error ?? 'error'), String(d.message ?? `Ошибка ${res.status}`), d)
  }
  const stepId = Number(res.headers.get('X-Undo-Step'))
  if (stepId > 0) {
    let label = res.headers.get('X-Undo-Label') ?? ''
    try {
      label = decodeURIComponent(label)
    } catch {
      /* keep as is */
    }
    const step = { id: stepId, label, merged: res.headers.get('X-Undo-Merged') === '1' }
    opts.onUndoStep?.(step)
    onUndoStep?.(step, opts.undoLabel)
  }
  return data as T
}

export const get = <T>(path: string, signal?: AbortSignal) => api<T>(path, { signal })
export const post = <T>(path: string, body?: unknown) => api<T>(path, { method: 'POST', body })
export const patch = <T>(path: string, body: unknown) => api<T>(path, { method: 'PATCH', body })
export const put = <T>(path: string, body?: unknown) => api<T>(path, { method: 'PUT', body })
export const del = <T>(path: string) => api<T>(path, { method: 'DELETE' })

export const fileUrl = (sha: string, opts: { name?: string; download?: boolean } = {}) => {
  const q = new URLSearchParams()
  if (opts.download) q.set('dl', '1')
  if (opts.name) q.set('name', opts.name)
  const qs = q.toString()
  return `/api/files/${sha}${qs ? '?' + qs : ''}`
}
export const derivedUrl = (sha: string, name: 'poster.jpg' | 'thumb.jpg' | 'preview.mp4') => `/api/files/${sha}/${name}`
