import { QueryClient, useQuery, useQueryClient, keepPreviousData } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo } from 'react'
import { toast } from 'sonner'
import { api, ApiError, get } from './api'
import { setTimeZone } from './format'
import type {
  Activity,
  AssetKind,
  Bootstrap,
  Channel,
  Comment,
  Dashboard,
  Day,
  Language,
  Platform,
  Stage,
  StorageStatus,
  Tag,
  TagGroup,
  TagScope,
  Track,
  TrashData,
  User,
  Video,
  VideoDetail,
} from './types'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 20_000,
      retry: (n, e) => !(e instanceof ApiError && e.status >= 400 && e.status < 500) && n < 2,
      refetchOnWindowFocus: true,
    },
  },
})

export const useBootstrapQuery = () =>
  useQuery({ queryKey: ['bootstrap'], queryFn: () => get<Bootstrap>('/bootstrap'), staleTime: 5 * 60_000 })

export interface Dicts extends Bootstrap {
  userById: Map<number, User>
  stageById: Map<number, Stage>
  langByCode: Map<string, Language>
  platformById: Map<number, Platform>
  channelById: Map<number, Channel>
  kindByKey: Map<string, AssetKind>
  tagById: Map<number, Tag>
  activeStages: Stage[]
  activeLangs: Language[]
  activeChannels: Channel[]
  activeKinds: AssetKind[]
  primaryLang: string
  groups: (scope: TagScope) => { group: TagGroup | null; tags: Tag[] }[]
  isAdmin: boolean
}

/** Dictionaries loaded once at start; the app renders only after they are available. */
export function useDicts(): Dicts {
  const { data } = useBootstrapQuery()
  return useMemo(() => {
    const b = data as Bootstrap
    setTimeZone(b.settings.timezone)
    const byId = <T extends { id: number }>(xs: T[]) => new Map(xs.map((x) => [x.id, x]))
    const primary = b.languages.find((l) => l.is_primary)?.code ?? b.languages[0]?.code ?? 'ru'
    return {
      ...b,
      userById: byId(b.users),
      stageById: byId(b.stages),
      langByCode: new Map(b.languages.map((l) => [l.code, l])),
      platformById: byId(b.platforms),
      channelById: byId(b.channels),
      kindByKey: new Map(b.kinds.map((k) => [k.key, k])),
      tagById: byId(b.tags),
      activeStages: b.stages.filter((s) => !s.archived),
      activeLangs: b.languages.filter((l) => !l.archived),
      activeChannels: b.channels.filter((c) => !c.archived),
      activeKinds: b.kinds.filter((k) => !k.archived),
      primaryLang: primary,
      isAdmin: b.me.role === 'admin',
      groups: (scope: TagScope) => {
        const gs = b.tag_groups.filter((g) => g.scope === scope)
        const tags = b.tags.filter((t) => t.scope === scope)
        const out: { group: TagGroup | null; tags: Tag[] }[] = gs.map((g) => ({ group: g, tags: tags.filter((t) => t.group_id === g.id) }))
        const loose = tags.filter((t) => !t.group_id || !gs.some((g) => g.id === t.group_id))
        if (loose.length) out.push({ group: null, tags: loose })
        return out
      },
    }
  }, [data])
}

export const useVideos = () => useQuery({ queryKey: ['videos'], queryFn: () => get<Video[]>('/videos') })
export const useVideo = (id: number) =>
  useQuery({ queryKey: ['video', id], queryFn: () => get<VideoDetail>(`/videos/${id}`), enabled: id > 0 })
export const useComments = (id: number) => useQuery({ queryKey: ['comments', id], queryFn: () => get<Comment[]>(`/videos/${id}/comments`) })
export const useActivity = (videoId?: number) =>
  useQuery({ queryKey: ['activity', videoId ?? 0], queryFn: () => get<Activity[]>(`/activity?limit=60${videoId ? `&video_id=${videoId}` : ''}`) })
export const useDashboard = () => useQuery({ queryKey: ['dashboard'], queryFn: () => get<Dashboard>('/dashboard'), refetchInterval: 5 * 60_000 })
export const useCalendar = (from: string, to: string) =>
  useQuery({
    queryKey: ['calendar', from, to],
    queryFn: () => get<{ today: string; start: string; quota: number; days: Day[] }>(`/calendar?from=${from}&to=${to}`),
    placeholderData: keepPreviousData,
  })
export const useTracks = () => useQuery({ queryKey: ['music'], queryFn: () => get<Track[]>('/music') })
export const useTrack = (id: number) =>
  useQuery({
    queryKey: ['track', id],
    queryFn: () => get<{ track: Track; videos: { video_id: number; code: string; title: string }[] }>(`/music/${id}`),
    enabled: id > 0,
  })
export const useStorage = () => useQuery({ queryKey: ['storage'], queryFn: () => get<StorageStatus>('/storage'), refetchInterval: 30_000 })
export const useTrash = () => useQuery({ queryKey: ['trash'], queryFn: () => get<TrashData>('/trash') })

/** Calls the API, shows errors as toasts and refreshes the given queries. */
export function useAction() {
  const qc = useQueryClient()
  return useCallback(
    async <T = unknown>(method: string, path: string, body?: unknown, opts: { invalidate?: unknown[][]; success?: string; silent?: boolean } = {}): Promise<T | undefined> => {
      try {
        const res = await api<T>(path, { method, body })
        for (const key of opts.invalidate ?? []) qc.invalidateQueries({ queryKey: key })
        if (opts.success) toast.success(opts.success)
        return res
      } catch (e) {
        if (!opts.silent) toast.error((e as Error).message)
        throw e
      }
    },
    [qc],
  )
}

const topicKeys: Record<string, unknown[][]> = {
  videos: [['videos'], ['dashboard'], ['calendar']],
  calendar: [['calendar'], ['dashboard']],
  music: [['music'], ['track']],
  storage: [['storage'], ['dashboard'], ['video']],
  dicts: [['bootstrap']],
  users: [['bootstrap']],
  trash: [['trash']],
  activity: [['activity'], ['dashboard']],
}

/** Subscribes to server-sent change notifications and refreshes affected data. */
export function useServerEvents(enabled: boolean) {
  const qc = useQueryClient()
  useEffect(() => {
    if (!enabled) return
    let pending = new Set<string>()
    let timer: number | undefined
    const flush = () => {
      const keys = new Map<string, unknown[]>()
      for (const t of pending) {
        if (t.startsWith('video:')) {
          const id = Number(t.slice(6))
          keys.set(`v${id}`, ['video', id])
          keys.set(`c${id}`, ['comments', id])
          keys.set('activity', ['activity'])
        } else {
          for (const k of topicKeys[t] ?? []) keys.set(JSON.stringify(k), k)
        }
      }
      pending = new Set()
      for (const k of keys.values()) qc.invalidateQueries({ queryKey: k })
    }
    const es = new EventSource('/api/events')
    es.onmessage = (ev) => {
      try {
        const { topics } = JSON.parse(ev.data) as { topics: string[] }
        topics.forEach((t) => pending.add(t))
        window.clearTimeout(timer)
        timer = window.setTimeout(flush, 350)
      } catch {
        /* ignore */
      }
    }
    return () => {
      es.close()
      window.clearTimeout(timer)
    }
  }, [enabled, qc])
}
