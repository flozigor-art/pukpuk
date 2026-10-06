export type ID = number

export interface User {
  id: ID
  login: string
  name: string
  role: 'admin' | 'member'
  color: string
  disabled: boolean
  created_at: number
}

export interface Language {
  code: string
  name: string
  flag: string
  sort: number
  is_primary: boolean
  archived: boolean
}

export interface Platform {
  id: ID
  name: string
  color: string
  icon: string
  counts_for_quota: boolean
  sort: number
  archived: boolean
}

export interface Channel {
  id: ID
  platform_id: ID
  language_code: string | null
  name: string
  url: string
  sort: number
  archived: boolean
}

export type StageKind = 'idea' | 'work' | 'ready' | 'done'

export interface Stage {
  id: ID
  name: string
  color: string
  kind: StageKind
  sort: number
  archived: boolean
}

export interface AssetKind {
  key: string
  name: string
  hint: string
  scope: 'variant' | 'shared'
  required: boolean
  accept: string
  sort: number
  archived: boolean
}

export type TagScope = 'video' | 'music'

export interface TagGroup {
  id: ID
  scope: TagScope
  name: string
  sort: number
}

export interface Tag {
  id: ID
  scope: TagScope
  group_id: ID | null
  name: string
  color: string
  sort: number
}

export interface ChecklistTemplateItem {
  id: ID
  label: string
  sort: number
}

export interface Settings {
  code_prefix: string
  timezone: string
  quota_per_day: string
  quota_start: string
  archive_hours: string
  window_days: string
  auto_done_stage: string
}

export interface Bootstrap {
  me: User
  users: User[]
  languages: Language[]
  platforms: Platform[]
  channels: Channel[]
  stages: Stage[]
  kinds: AssetKind[]
  tag_groups: TagGroup[]
  tags: Tag[]
  checklist: ChecklistTemplateItem[]
  settings: Settings
  config: { version: string; chunk_size: number; max_file: number }
}

export type PubStatus = 'planned' | 'scheduled' | 'published' | 'skipped' | 'removed'

export interface PubSummary {
  id: ID
  channel_id: ID
  status: PubStatus
  plan_at: number | null
  published_at: number | null
  url: string
}

export interface VariantSummary {
  id: ID
  lang: string
  title: string
  caption?: string
  voice: string
  status: 'todo' | 'wip' | 'ready'
  first_published_at: number | null
  missing: string[]
  deadline_at: number | null
  pubs: PubSummary[]
}

export interface ArchiveState {
  published: boolean
  complete: boolean
  missing: number
  deadline_at: number | null
}

export interface Video {
  id: ID
  num: number
  code: string
  title: string
  stage_id: ID | null
  assignee_id: ID | null
  plan_date: string | null
  is_unique: boolean
  original_id: ID | null
  created_by: ID | null
  created_at: number
  updated_at: number
  deleted_at?: number
  script?: string
  notes?: string
  music_note?: string
  tags: ID[]
  thumb: string | null
  variants: VariantSummary[]
  first_published_at: number | null
  next_plan_at: number | null
  archive: ArchiveState
  asset_count: number
  comment_count: number
  check_done: number
  check_total: number
}

export interface BlobInfo {
  state: 'buffered' | 'stored' | 'missing'
  local: boolean
  duration_ms: number | null
  width: number | null
  height: number | null
  derived: string[]
  sync_error?: string
}

export interface Asset {
  id: ID
  video_id: ID
  variant_id: ID | null
  kind: string
  filename: string
  sha256: string
  size: number
  mime: string
  version: number
  note: string
  uploaded_by: ID | null
  created_at: number
  deleted_at?: number
  blob: BlobInfo | null
}

export interface Publication {
  id: ID
  video_id: ID
  variant_id: ID
  channel_id: ID
  status: PubStatus
  plan_at: number | null
  published_at: number | null
  url: string
  note: string
  views: number | null
  created_by: ID | null
  updated_at: number
}

export interface CheckItem {
  id: ID
  label: string
  done_by: ID | null
  done_at: number | null
  sort: number
}

export interface VideoDetail extends Video {
  assets: Asset[]
  publications: Publication[]
  checklist: CheckItem[]
  tracks: ID[]
}

export interface Comment {
  id: ID
  video_id: ID
  user_id: ID
  body: string
  created_at: number
  edited_at: number | null
}

export interface UndoItem {
  id: ID
  kind: 'action' | 'undo' | 'redo'
  label: string
  user_id: ID | null
  video_id: ID | null
  admin_only: boolean
  target_id: ID | null
  undone: boolean
  undone_by_user: ID | null
  undone_at: number | null
  created_at: number
  updated_at: number
  video_code?: string
  blocked: boolean
  blocked_by: ID | null
}

export interface Activity {
  id: ID
  user_id: ID | null
  video_id: ID | null
  action: string
  data: Record<string, unknown>
  created_at: number
  video_num?: number
  video_title?: string
}

export type DayStatus = 'off' | 'done' | 'planned' | 'today' | 'empty' | 'missed' | 'excused'

export interface CalPub {
  id: ID
  channel_id: ID
  lang: string
  status: PubStatus
  at: number
}

export interface CalItem {
  video_id: ID
  code: string
  title: string
  stage_id: ID | null
  thumb: string | null
  unique: boolean
  kind: 'first' | 'planned' | 'pub'
  pubs: CalPub[]
}

export interface Day {
  date: string
  status: DayStatus
  done: number
  planned: number
  excused: boolean
  reason: string
  note: string
  items: CalItem[]
}

export interface PlanStats {
  today: string
  today_status: DayStatus
  quota: number
  start: string
  streak: number
  missed_row: number
  missed_window: number
  window: number
  reserve: number
  covered_until: string
  days: Day[]
}

export interface StorageSummary {
  online: boolean
  last_seen: number
  pending_count: number
  pending_bytes: number
  missing_count: number
  buffer_used: number
  buffer_max: number
  last_backup: number
}

export interface Dashboard {
  plan: PlanStats
  archive: Video[]
  stages: Record<string, number>
  in_work: Video[]
  activity: Activity[]
  storage: StorageSummary
  total: number
}

export interface Track {
  id: ID
  title: string
  artist: string
  album: string
  bpm: number | null
  musical_key: string
  duration_ms: number | null
  sha256: string
  filename: string
  size: number
  source: string
  license: string
  license_url: string
  platforms: ID[]
  notes: string
  uploaded_by: ID | null
  created_at: number
  updated_at: number
  deleted_at?: number
  tags: ID[]
  favorite: boolean
  used_in: number
  peaks: number[] | null
  has_cover: boolean
  state: BlobInfo['state']
  local: boolean
}

export interface NodeInfo {
  online: boolean
  connected_at: number
  last_seen: number
  version: string
  node_id: string
  addr: string
  disk_free: number
  disk_total: number
  blobs: number
  bytes: number
  queue: number
  derive: number
  last_backup: number
}

export interface PendingBlob {
  sha256: string
  size: number
  mime: string
  state: string
  created_at: number
  sync_error: string
  name: string
  video_id: ID | null
}

export interface StorageStatus {
  summary: StorageSummary
  node: NodeInfo
  usage: { pinned: number; uploading: number; cached: number; previews: number; disk_free: number; disk_total: number }
  limits: { buffer: number; cache: number; previews: number; reserve: number; max_file: number }
  pending: PendingBlob[]
  missing: PendingBlob[]
  uploads: number
  totals: { count: number; bytes: number; stored_count: number; stored_bytes: number }
  version: string
}

export interface TrashData {
  videos: Video[]
  assets: (Asset & { video_code: string; video_title: string })[]
  tracks: Track[]
}
