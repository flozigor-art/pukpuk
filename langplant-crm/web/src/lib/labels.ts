import type { AssetKind, PubStatus } from './types'

export const PUB_STATUS: Record<PubStatus, { label: string; tone: string }> = {
  planned: { label: 'В плане', tone: 'blue' },
  scheduled: { label: 'Отложена в соцсети', tone: 'violet' },
  published: { label: 'Опубликовано', tone: 'green' },
  skipped: { label: 'Не публикуем', tone: '' },
  removed: { label: 'Снято / удалено', tone: 'red' },
}

// п.16: translation and dubbing by Isabella are allowed; synthetic voice needs separate consent.
export const VOICE: Record<string, string> = {
  original: 'Оригинал',
  dub: 'Дубляж (Изабелла)',
  dub_other: 'Дубляж (другой голос)',
  subs_only: 'Только субтитры',
  ai: 'ИИ-голос — нужно отдельное согласие (п.16.4)',
  none: 'Без голоса',
}

export const VARIANT_STATUS: Record<string, { label: string; tone: string }> = {
  todo: { label: 'Не начато', tone: '' },
  wip: { label: 'В работе', tone: 'amber' },
  ready: { label: 'Готово', tone: 'green' },
}

export const DAY_REASONS: Record<string, string> = {
  illness: 'Болезнь',
  platform: 'Площадка недоступна',
  blocked: 'Аккаунт заблокирован',
  vacation: 'Отпуск / поездка',
  force: 'Форс-мажор',
  other: 'Другое',
}

export const DAY_STATUS: Record<string, string> = {
  done: 'Опубликовано',
  planned: 'Запланировано',
  today: 'Сегодня — ещё нет публикации',
  empty: 'Ничего не запланировано',
  missed: 'Пропуск',
  excused: 'Уважительная причина',
  off: 'До начала плана',
}

export const STAGE_KIND: Record<string, string> = {
  idea: 'Идея',
  work: 'В работе',
  ready: 'Готов к публикации',
  done: 'Опубликован',
}

export const ACTIONS: Record<string, string> = {
  'video.created': 'создал(а) ролик',
  'video.stage': 'сменил(а) этап',
  'video.plan': 'изменил(а) дату плана',
  'video.renamed': 'переименовал(а) ролик',
  'video.deleted': 'удалил(а) ролик в корзину',
  'video.restored': 'восстановил(а) ролик',
  'video.purged': 'удалил(а) ролик навсегда',
  'variant.created': 'добавил(а) языковую версию',
  'variant.deleted': 'удалил(а) языковую версию',
  'asset.uploaded': 'загрузил(а)',
  'asset.deleted': 'удалил(а) файл',
  'publication.published': 'опубликовал(а)',
  'publication.scheduled': 'поставил(а) отложенную публикацию',
  'comment.added': 'прокомментировал(а)',
  'day.excused': 'отметил(а) уважительную причину',
}

const has = (s: string, re: RegExp) => re.test(s.toLowerCase())

/** Guesses the file kind from its name and type. */
export function guessKind(file: File, kinds: AssetKind[], existing: Set<string> = new Set()): string {
  const name = file.name.toLowerCase()
  const ext = name.split('.').pop() ?? ''
  const type = file.type
  const avail = new Set(kinds.map((k) => k.key))
  const pick = (...keys: string[]) => keys.find((k) => avail.has(k)) ?? 'other'
  if (['srt', 'vtt', 'ass', 'ssa', 'sbv'].includes(ext)) return pick('subs')
  if (['prproj', 'drp', 'dra', 'aep', 'veg', 'fcpxml', 'kdenlive', 'mlt', 'ccproj', 'drt', 'aup3', 'als', 'flp'].includes(ext)) return pick('project')
  if (['psd', 'ai', 'svg', 'fig', 'sketch', 'xd'].includes(ext)) return pick('graphics')
  if (['txt', 'md', 'doc', 'docx', 'pdf', 'rtf', 'odt'].includes(ext)) return pick('script')
  if (type.startsWith('image/') || ['jpg', 'jpeg', 'png', 'webp', 'heic'].includes(ext))
    return has(name, /cover|обложк|thumb|превью|preview|poster/) ? pick('cover') : existing.has('cover') ? pick('graphics') : pick('cover')
  if (type.startsWith('audio/') || ['mp3', 'wav', 'm4a', 'aac', 'flac', 'ogg', 'aif', 'aiff'].includes(ext)) {
    if (has(name, /no.?voice|без.?голос|bed|минус|instrumental|backing|music.?sfx|фон/)) return pick('bed')
    if (has(name, /voice|vocal|голос|дикт|озвуч|\bvo\b/)) return pick('voice')
    if (has(name, /sfx|fx|звук|effect|whoosh/)) return pick('sfx')
    if (has(name, /music|музык|song|beat|трек/)) return pick('music')
    return existing.has('voice') ? pick('bed', 'music') : pick('voice')
  }
  if (type.startsWith('video/') || ['mp4', 'mov', 'm4v', 'webm', 'mkv', 'avi', 'mts'].includes(ext)) {
    if (has(name, /clean|без.?суб|no.?sub|nosub|textless|чист/)) return pick('clean')
    if (has(name, /final|финал|итог|export|master|ready/)) return pick('final')
    if (has(name, /raw|исход|source|camera|cam|dji|^img_|^mvi|gopr|dsc|clip|take|дубль/)) return pick('raw')
    return existing.has('final') ? pick('raw') : pick('final')
  }
  if (['zip', 'rar', '7z'].includes(ext)) return pick('project', 'other')
  return 'other'
}

export function matchesAccept(file: File, accept: string): boolean {
  if (!accept) return true
  const name = file.name.toLowerCase()
  return accept.split(',').some((a) => {
    a = a.trim().toLowerCase()
    if (!a) return false
    if (a.startsWith('.')) return name.endsWith(a)
    if (a.endsWith('/*')) return file.type.startsWith(a.slice(0, -1))
    return file.type === a
  })
}
