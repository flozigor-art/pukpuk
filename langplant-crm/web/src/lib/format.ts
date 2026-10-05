import { TZDate } from '@date-fns/tz'

// Project time zone (from settings). All calendar days are in this zone.
let TZ = 'Europe/Moscow'
export function setTimeZone(tz: string) {
  if (tz) TZ = tz
}
export const timeZone = () => TZ

const cache = new Map<string, Intl.DateTimeFormat>()
function fmt(opts: Intl.DateTimeFormatOptions) {
  const key = TZ + JSON.stringify(opts)
  let f = cache.get(key)
  if (!f) {
    f = new Intl.DateTimeFormat('ru-RU', { timeZone: TZ, ...opts })
    cache.set(key, f)
  }
  return f
}

export const fmtDate = (ms: number) => fmt({ day: 'numeric', month: 'short' }).format(ms).replace('.', '')
export const fmtDateFull = (ms: number) => fmt({ day: 'numeric', month: 'long', year: 'numeric' }).format(ms)
export const fmtTime = (ms: number) => fmt({ hour: '2-digit', minute: '2-digit' }).format(ms)
export const fmtDateTime = (ms: number) => `${fmtDate(ms)}, ${fmtTime(ms)}`

/** YYYY-MM-DD of a timestamp in the project time zone. */
export function dayOf(ms: number): string {
  const d = new TZDate(ms, TZ)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}
export const today = () => dayOf(Date.now())

export const pad = (n: number) => String(n).padStart(2, '0')

export function addDays(day: string, n: number): string {
  const [y, m, d] = day.split('-').map(Number)
  const dt = new Date(Date.UTC(y, m - 1, d + n))
  return `${dt.getUTCFullYear()}-${pad(dt.getUTCMonth() + 1)}-${pad(dt.getUTCDate())}`
}
export function weekday(day: string): number {
  const [y, m, d] = day.split('-').map(Number)
  return (new Date(Date.UTC(y, m - 1, d)).getUTCDay() + 6) % 7 // 0 = Monday
}
export function diffDays(a: string, b: string): number {
  const pa = a.split('-').map(Number)
  const pb = b.split('-').map(Number)
  return Math.round((Date.UTC(pa[0], pa[1] - 1, pa[2]) - Date.UTC(pb[0], pb[1] - 1, pb[2])) / 86400000)
}

const MONTHS = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря']
const MONTHS_SHORT = ['янв', 'фев', 'мар', 'апр', 'мая', 'июн', 'июл', 'авг', 'сен', 'окт', 'ноя', 'дек']
export const MONTHS_NOM = ['Январь', 'Февраль', 'Март', 'Апрель', 'Май', 'Июнь', 'Июль', 'Август', 'Сентябрь', 'Октябрь', 'Ноябрь', 'Декабрь']
export const WEEKDAYS = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс']

export function fmtDay(day: string, opts: { weekday?: boolean; long?: boolean } = {}): string {
  const [, m, d] = day.split('-').map(Number)
  let s = `${d} ${opts.long ? MONTHS[m - 1] : MONTHS_SHORT[m - 1]}`
  if (opts.weekday) s += `, ${WEEKDAYS[weekday(day)].toLowerCase()}`
  return s
}

export function relDay(day: string): string {
  const diff = diffDays(day, today())
  if (diff === 0) return 'сегодня'
  if (diff === 1) return 'завтра'
  if (diff === -1) return 'вчера'
  return fmtDay(day)
}

export function fmtAgo(ms: number): string {
  const s = Math.round((Date.now() - ms) / 1000)
  if (s < 45) return 'только что'
  const m = Math.round(s / 60)
  if (m < 60) return `${m} мин назад`
  const h = Math.round(m / 60)
  if (h < 24) return `${h} ч назад`
  const d = dayOf(ms)
  const diff = diffDays(today(), d)
  if (diff === 1) return `вчера, ${fmtTime(ms)}`
  if (diff < 7) return `${diff} ${plural(diff, 'день', 'дня', 'дней')} назад`
  return fmtDate(ms)
}

/** Remaining time until ms, like "31 ч" or "просрочено на 5 ч". */
export function fmtLeft(ms: number): { text: string; overdue: boolean; urgent: boolean } {
  const diff = ms - Date.now()
  const h = Math.abs(diff) / 3600000
  const v = h >= 48 ? `${Math.round(h / 24)} дн` : h >= 1 ? `${Math.round(h)} ч` : `${Math.max(1, Math.round(h * 60))} мин`
  if (diff < 0) return { text: `просрочено на ${v}`, overdue: true, urgent: true }
  return { text: `осталось ${v}`, overdue: false, urgent: h < 12 }
}

export function plural(n: number, one: string, few: string, many: string) {
  const a = Math.abs(n) % 100
  const b = a % 10
  if (a > 10 && a < 20) return many
  if (b > 1 && b < 5) return few
  if (b === 1) return one
  return many
}

export function fmtBytes(n: number | null | undefined): string {
  if (n == null || n < 0) return '—'
  if (n < 1024) return `${n} Б`
  const units = ['КБ', 'МБ', 'ГБ', 'ТБ']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1).replace('.0', '')} ${units[i]}`
}

export function fmtDuration(ms: number | null | undefined): string {
  if (ms == null || !isFinite(ms)) return '—'
  const s = Math.round(ms / 1000)
  const m = Math.floor(s / 60)
  return `${m}:${pad(s % 60)}`
}

/** Value for <input type="datetime-local"> in the project time zone. */
export function toLocalInput(ms: number): string {
  const d = new TZDate(ms, TZ)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}
export function fromLocalInput(v: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(v)
  if (!m) return null
  return new TZDate(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], 0, TZ).getTime()
}
export function dayAt(day: string, hh = 12, mm = 0): number {
  const [y, m, d] = day.split('-').map(Number)
  return new TZDate(y, m - 1, d, hh, mm, 0, TZ).getTime()
}

export function initials(name: string) {
  return name
    .split(/\s+/)
    .map((p) => p[0])
    .join('')
    .slice(0, 2)
    .toUpperCase()
}
