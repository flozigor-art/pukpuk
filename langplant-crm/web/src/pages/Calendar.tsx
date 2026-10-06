import { DndContext, PointerSensor, TouchSensor, useDraggable, useDroppable, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import clsx from 'clsx'
import { CalendarDays, ChevronLeft, ChevronRight, List, Plus } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useNewVideo } from '../components/Layout'
import { Button, Field, IconButton, Loading, Modal, PlatformIcon, Seg, StagePill, Thumb, Toggle } from '../components/ui'
import { api } from '../lib/api'
import { addDays, dayAt, diffDays, fmtDay, fmtTime, MONTHS_NOM, pad, today as todayStr, weekday, WEEKDAYS } from '../lib/format'
import { DAY_REASONS, DAY_STATUS, PUB_STATUS } from '../lib/labels'
import { queryClient, useAction, useCalendar, useDicts, useVideos } from '../lib/queries'
import type { CalItem, Day } from '../lib/types'

const statusColor: Record<string, string> = {
  done: 'var(--accent)',
  planned: 'var(--blue)',
  today: 'var(--amber)',
  missed: 'var(--red)',
  excused: 'var(--text-3)',
}

export default function Calendar() {
  const now = todayStr()
  const [month, setMonth] = useState(now.slice(0, 7))
  const mobile = typeof window !== 'undefined' && window.innerWidth < 900
  const [view, setView] = useState<'month' | 'agenda'>(mobile ? 'agenda' : 'month')
  const [openDay, setOpenDay] = useState<string | null>(null)
  // phones: tapping a day in the month selects it and lists it under the grid
  const compact = typeof window !== 'undefined' && window.matchMedia('(max-width: 699px)').matches
  const [sel, setSel] = useState(now)

  const [y, m] = month.split('-').map(Number)
  const first = `${month}-01`
  const gridFrom = addDays(first, -weekday(first))
  const lastDay = `${month}-${pad(new Date(Date.UTC(y, m, 0)).getUTCDate())}`
  const gridTo = addDays(lastDay, 6 - weekday(lastDay))
  const from = view === 'month' ? gridFrom : addDays(now, -7)
  const to = view === 'month' ? gridTo : addDays(now, 45)
  const { data, isLoading } = useCalendar(from, to)
  const days = data?.days ?? []
  const byDate = useMemo(() => new Map(days.map((x) => [x.date, x])), [days])
  const shift = (n: number) => {
    const dt = new Date(Date.UTC(y, m - 1 + n, 1))
    setMonth(`${dt.getUTCFullYear()}-${pad(dt.getUTCMonth() + 1)}`)
  }

  return (
    <div className="page wide" style={{ maxWidth: 1400 }}>
      <div className="page-head">
        <div className="hide-m">
          <h1>Календарь публикаций</h1>
          <div className="sub">План: {data?.quota ?? 1} новый уникальный ролик в день · отсчёт с {data?.start ? fmtDay(data.start, { long: true }) : '—'}</div>
        </div>
        <div className="actions cal-actions">
          {view === 'month' ? (
            <div className="row" style={{ gap: 2 }}>
              <IconButton label="Предыдущий месяц" onClick={() => shift(-1)}>
                <ChevronLeft size={18} />
              </IconButton>
              <b className="cal-month">
                {MONTHS_NOM[m - 1]} {y}
              </b>
              <IconButton label="Следующий месяц" onClick={() => shift(1)}>
                <ChevronRight size={18} />
              </IconButton>
              {month !== now.slice(0, 7) && (
                <Button size="sm" onClick={() => setMonth(now.slice(0, 7))} style={{ marginLeft: 4 }}>
                  Сегодня
                </Button>
              )}
            </div>
          ) : (
            <b className="cal-month hide-d" style={{ textAlign: 'left' }}>
              Ближайшие недели
            </b>
          )}
          <Seg
            value={view}
            onChange={setView}
            options={[
              { value: 'month', label: <CalendarDays size={15} />, title: 'Месяц' },
              { value: 'agenda', label: <List size={15} />, title: 'Лента' },
            ]}
          />
        </div>
      </div>
      <div className="legend cal-legend">
        <span>
          <i style={{ background: 'var(--accent)', borderRadius: 99 }} /> вышел<span className="hide-m"> новый ролик</span>
        </span>
        <span>
          <i style={{ background: 'var(--blue)', borderRadius: 99 }} /> запланирован
        </span>
        <span>
          <i style={{ background: 'var(--red)', borderRadius: 99 }} /> пропуск
        </span>
        <span>
          <i style={{ background: 'var(--text-3)', borderRadius: 99 }} /> <span className="hide-m">уважительная </span>причина
        </span>
        {view === 'month' && <span className="hide-m" style={{ marginLeft: 'auto' }}>Перетащите запланированный ролик на другой день, чтобы перенести</span>}
      </div>
      {isLoading && !data ? (
        <Loading />
      ) : view === 'month' ? (
        <>
          <MonthGrid from={gridFrom} to={gridTo} month={month} byDate={byDate} today={now} selected={compact ? sel : null} onOpen={compact ? setSel : setOpenDay} />
          {compact && byDate.get(sel) && (
            <div className="card agenda" style={{ marginTop: 10 }}>
              <div className="ag-month row" style={{ justifyContent: 'space-between' }}>
                <span>{fmtDay(sel, { weekday: true, long: true })}</span>
                <button className="btn sm ghost" style={{ margin: '-4px -8px', textTransform: 'none', letterSpacing: 0 }} onClick={() => setOpenDay(sel)}>
                  День…
                </button>
              </div>
              <AgendaDay day={byDate.get(sel)!} today={now} onOpen={setOpenDay} />
            </div>
          )}
        </>
      ) : (
        <Agenda days={days} today={now} onOpen={setOpenDay} />
      )}
      {openDay && <DayModal date={openDay} day={byDate.get(openDay)} onClose={() => setOpenDay(null)} />}
    </div>
  )
}

async function moveItem(item: CalItem, to: string) {
  for (const p of item.pubs) {
    if (p.status === 'planned' || p.status === 'scheduled') {
      const t = new Date(p.at)
      const hh = Number(fmtTime(t.getTime()).slice(0, 2))
      const mm = Number(fmtTime(t.getTime()).slice(3, 5))
      await api(`/publications/${p.id}`, { method: 'PATCH', body: { plan_at: dayAt(to, hh, mm) } })
    }
  }
  await api(`/videos/${item.video_id}`, { method: 'PATCH', body: { plan_date: to } })
  ;['calendar', 'videos', 'dashboard'].forEach((k) => queryClient.invalidateQueries({ queryKey: [k] }))
  queryClient.invalidateQueries({ queryKey: ['video', item.video_id] })
}

function MonthGrid({ from, to, month, byDate, today, selected, onOpen }: { from: string; to: string; month: string; byDate: Map<string, Day>; today: string; selected: string | null; onOpen: (d: string) => void }) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(TouchSensor, { activationConstraint: { delay: 250, tolerance: 8 } }))
  const dates: string[] = []
  for (let x = from; x <= to; x = addDays(x, 1)) dates.push(x)
  const onDragEnd = (e: DragEndEvent) => {
    const item = e.active.data.current?.item as CalItem | undefined
    const target = e.over?.id as string | undefined
    const source = e.active.data.current?.date as string | undefined
    if (item && target && target !== source) moveItem(item, target)
  }
  return (
    <DndContext sensors={sensors} onDragEnd={onDragEnd}>
      <div className="cal">
        {WEEKDAYS.map((w) => (
          <div key={w} className="cal-wd">
            {w}
          </div>
        ))}
        {dates.map((date) => (
          <DayCell key={date} date={date} day={byDate.get(date)} other={!date.startsWith(month)} today={today} selected={date === selected} onOpen={onOpen} />
        ))}
      </div>
    </DndContext>
  )
}

function DayCell({ date, day, other, today, selected, onOpen }: { date: string; day?: Day; other: boolean; today: string; selected: boolean; onOpen: (d: string) => void }) {
  const { setNodeRef, isOver } = useDroppable({ id: date })
  const items = day?.items ?? []
  const color = day ? statusColor[day.status] : undefined
  return (
    <div ref={setNodeRef} className={clsx('cal-day', other && 'other', date === today && 'is-today', selected && 'sel')} style={isOver ? { background: 'var(--blue-soft)' } : undefined} onClick={() => onOpen(date)}>
      <div className="num">
        <b>{Number(date.slice(8))}</b>
        {day?.status === 'excused' && <span className="tiny muted">{DAY_REASONS[day.reason] ?? 'причина'}</span>}
        {color && <span className="mark" style={{ background: color }} title={DAY_STATUS[day!.status]} />}
      </div>
      {items.slice(0, 3).map((it) => (
        <CalChip key={it.video_id} item={it} date={date} />
      ))}
      {items.length > 3 && <div className="tiny muted" style={{ paddingLeft: 6 }}>ещё {items.length - 3}</div>}
    </div>
  )
}

function CalChip({ item, date }: { item: CalItem; date: string }) {
  const d = useDicts()
  const movable = item.kind === 'planned'
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({ id: `${date}:${item.video_id}`, data: { item, date }, disabled: !movable })
  const platforms = [...new Set(item.pubs.map((p) => d.channelById.get(p.channel_id)?.platform_id))]
  return (
    <div
      ref={setNodeRef}
      {...attributes}
      {...listeners}
      className={clsx('cal-item', item.kind)}
      style={{ opacity: isDragging ? 0.4 : 1, cursor: movable ? 'grab' : 'pointer' }}
      title={`${item.code} ${item.title}`}
      onClick={(e) => e.stopPropagation()}
    >
      <Link to={`/videos/${item.video_id}`} className="t" onClick={(e) => isDragging && e.preventDefault()}>
        {item.title}
      </Link>
      <span className="meta">
        {item.pubs.length > 0 ? <span>{fmtTime(Math.min(...item.pubs.map((p) => p.at)))}</span> : <span className="code">{item.code}</span>}
        <span className="flags">
          {platforms.slice(0, 4).map((pid) => (
            <PlatformIcon key={pid} platform={pid ? d.platformById.get(pid) : undefined} size="sm" />
          ))}
        </span>
      </span>
    </div>
  )
}

type AgendaRow = { kind: 'day'; day: Day } | { kind: 'free'; from: string; to: string }

function Agenda({ days, today, onOpen }: { days: Day[]; today: string; onOpen: (d: string) => void }) {
  const newVideo = useNewVideo()
  // past days only when something happened; runs of empty future days fold into one row
  const rows: AgendaRow[] = []
  for (const x of days) {
    const past = x.date < today
    if (past && !x.items.length && x.status !== 'missed' && x.status !== 'excused') continue
    if (x.date > today && !x.items.length && !x.excused && !x.note) {
      const last = rows[rows.length - 1]
      if (last?.kind === 'free' && addDays(last.to, 1) === x.date) last.to = x.date
      else rows.push({ kind: 'free', from: x.date, to: x.date })
      continue
    }
    rows.push({ kind: 'day', day: x })
  }
  let month = ''
  return (
    <div className="card agenda">
      {rows.map((r) => {
        const date = r.kind === 'day' ? r.day.date : r.from
        const head = date.slice(0, 7) !== month ? MONTHS_NOM[Number(date.slice(5, 7)) - 1] : null
        month = date.slice(0, 7)
        return (
          <div key={date}>
            {head && <div className="ag-month">{head}</div>}
            {r.kind === 'free' ? (
              <div className="ag-row ag-free">
                <div className="ag-date">
                  <b>{Number(r.from.slice(8))}</b>
                  <span>{WEEKDAYS[weekday(r.from)]}</span>
                </div>
                <div className="grow muted small">
                  {r.from === r.to ? 'Ничего не запланировано' : `Свободно до ${fmtDay(r.to)} · ${diffDays(r.to, r.from) + 1} дн.`}
                </div>
                <button className="btn sm ghost" onClick={() => newVideo(r.from)}>
                  <Plus size={14} /> Ролик
                </button>
              </div>
            ) : (
              <AgendaDay day={r.day} today={today} onOpen={onOpen} />
            )}
          </div>
        )
      })}
    </div>
  )
}

function AgendaDay({ day: x, today, onOpen }: { day: Day; today: string; onOpen: (d: string) => void }) {
  return (
    <div className={clsx('ag-row', x.date === today && 'is-today')} onClick={() => onOpen(x.date)}>
      <div className="ag-date">
        <b>{Number(x.date.slice(8))}</b>
        <span>{WEEKDAYS[weekday(x.date)]}</span>
      </div>
      <div className="grow" style={{ minWidth: 0, display: 'flex', flexDirection: 'column', gap: 6 }}>
        <div className="ag-status" style={{ color: statusColor[x.status] ?? 'var(--text-3)' }}>
          {x.date === today && x.status !== 'today' ? `Сегодня · ${DAY_STATUS[x.status].toLowerCase()}` : DAY_STATUS[x.status]}
          {x.status === 'excused' && x.reason && ` · ${DAY_REASONS[x.reason]}`}
          {x.note && <span className="muted" style={{ fontWeight: 500 }}> · {x.note}</span>}
        </div>
        {x.items.map((it) => (
          <AgendaItem key={it.video_id} item={it} />
        ))}
      </div>
    </div>
  )
}

function AgendaItem({ item }: { item: CalItem }) {
  const d = useDicts()
  return (
    <Link to={`/videos/${item.video_id}`} className="ag-item" onClick={(e) => e.stopPropagation()}>
      <Thumb sha={item.thumb} size="sm" />
      <div className="grow" style={{ minWidth: 0 }}>
        <div className="clamp2" style={{ fontWeight: 600, lineHeight: 1.3 }}>
          {item.title}
        </div>
        <div className="row tiny muted" style={{ gap: 6, marginTop: 2, whiteSpace: 'nowrap', overflow: 'hidden' }}>
          <span className="code">{item.code}</span>
          {item.pubs.length > 0 && (
            <>
              <span>{fmtTime(Math.min(...item.pubs.map((p) => p.at)))}</span>
              <span className="flags">
                {[...new Set(item.pubs.map((p) => d.channelById.get(p.channel_id)?.platform_id ?? 0))].map((pid) => (
                  <PlatformIcon key={pid} platform={d.platformById.get(pid)} size="sm" />
                ))}
              </span>
              <span>{[...new Set(item.pubs.map((p) => p.lang.toUpperCase()))].join(' · ')}</span>
            </>
          )}
          {item.kind === 'pub' && <span>повторная публикация</span>}
        </div>
      </div>
    </Link>
  )
}

function DayModal({ date, day, onClose }: { date: string; day?: Day; onClose: () => void }) {
  const d = useDicts()
  const act = useAction()
  const nav = useNavigate()
  const newVideo = useNewVideo()
  const videos = useVideos()
  const [excused, setExcused] = useState(day?.excused ?? false)
  const [reason, setReason] = useState(day?.reason || 'illness')
  const [note, setNote] = useState(day?.note ?? '')
  const inv = [['calendar'], ['dashboard']]
  const unplanned = (videos.data ?? []).filter((v) => !v.first_published_at && !v.plan_date && !v.next_plan_at)
  const save = async () => {
    if (!excused && !note.trim()) await act('DELETE', `/calendar/days/${date}`, undefined, { invalidate: inv })
    else await act('PUT', `/calendar/days/${date}`, { excused, reason: excused ? reason : '', note }, { invalidate: inv })
    onClose()
  }
  return (
    <Modal
      open
      onClose={onClose}
      title={fmtDay(date, { weekday: true, long: true })}
      footer={
        <>
          <Button onClick={onClose}>Закрыть</Button>
          <Button variant="primary" onClick={save}>
            Сохранить
          </Button>
        </>
      }
    >
      <div className="form">
        {day && (
          <div className="row">
            <span className="dot" style={{ background: statusColor[day.status] ?? 'var(--border-strong)' }} />
            <b>{DAY_STATUS[day.status]}</b>
          </div>
        )}
        {(day?.items ?? []).map((it) => (
          <Link key={it.video_id} to={`/videos/${it.video_id}`} className="row" style={{ gap: 10 }} onClick={onClose}>
            <Thumb sha={it.thumb} size="sm" />
            <div className="grow" style={{ minWidth: 0 }}>
              <div className="ellipsis" style={{ fontWeight: 600 }}>
                <span className="code">{it.code}</span> {it.title}
              </div>
              <div className="tiny muted">
                {it.pubs.length
                  ? it.pubs.map((p) => `${d.platformById.get(d.channelById.get(p.channel_id)?.platform_id ?? 0)?.name} ${p.lang.toUpperCase()} — ${PUB_STATUS[p.status].label.toLowerCase()} ${fmtTime(p.at)}`).join(' · ')
                  : 'дата публикации без конкретных площадок'}
              </div>
            </div>
            <StagePill stage={it.stage_id ? d.stageById.get(it.stage_id) : null} size="sm" />
          </Link>
        ))}
        <div className="row wrap">
          <Button size="sm" icon={<Plus size={14} />} onClick={() => {
            onClose()
            newVideo(date)
          }}>
            Новый ролик на этот день
          </Button>
          {unplanned.length > 0 && (
            <select
              className="select sm"
              style={{ width: 'auto', maxWidth: '100%' }}
              value=""
              onChange={async (e) => {
                const id = Number(e.target.value)
                if (!id) return
                await act('PATCH', `/videos/${id}`, { plan_date: date }, { invalidate: [['calendar'], ['videos'], ['dashboard']] })
              }}
            >
              <option value="">Запланировать существующий…</option>
              {unplanned.map((v) => (
                <option key={v.id} value={v.id}>
                  {v.code} {v.title} ({d.stageById.get(v.stage_id ?? 0)?.name ?? '—'})
                </option>
              ))}
            </select>
          )}
        </div>
        <div className="card card-pad" style={{ background: 'var(--surface-2)', boxShadow: 'none' }}>
          <div className="row" style={{ marginBottom: excused ? 12 : 0 }}>
            <Toggle on={excused} onChange={setExcused} label="Уважительная причина" />
            <div className="grow">
              <b>Уважительная причина</b>
              <div className="tiny muted">День без публикации не считается пропуском (п.6.6–6.8, 31)</div>
            </div>
          </div>
          {excused && (
            <div className="chips">
              {Object.entries(DAY_REASONS).map(([k, l]) => (
                <button key={k} type="button" className={clsx('chip', reason === k && 'on')} onClick={() => setReason(k)}>
                  {l}
                </button>
              ))}
            </div>
          )}
        </div>
        <Field label="Заметка на день">
          <input className="input" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Например: съёмка в студии, TikTok лежал до обеда" />
        </Field>
        <button className="btn ghost sm" style={{ alignSelf: 'flex-start' }} onClick={() => nav(`/videos?pub=planned`)}>
          Все запланированные ролики →
        </button>
      </div>
    </Modal>
  )
}

