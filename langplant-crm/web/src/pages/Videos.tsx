import { DndContext, DragOverlay, PointerSensor, TouchSensor, useDraggable, useDroppable, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { useQueryClient } from '@tanstack/react-query'
import clsx from 'clsx'
import { Archive, CalendarDays, Clapperboard, Columns3, List, MessageSquare, Plus, Search, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { useNewVideo } from '../components/Layout'
import { LangBadges, TagChips } from '../components/pickers'
import { Avatar, Button, Empty, Loading, Seg, StagePill, Thumb } from '../components/ui'
import { patch } from '../lib/api'
import { dayOf, fmtDate, fmtLeft, relDay } from '../lib/format'
import { useDicts, useVideos } from '../lib/queries'
import type { ID, Video } from '../lib/types'

type View = 'list' | 'board'

export default function Videos() {
  const d = useDicts()
  const { data, isLoading } = useVideos()
  const [sp, setSp] = useSearchParams()
  const newVideo = useNewVideo()
  const view: View = (sp.get('view') as View) || (localStorage.getItem('crm.videos.view') as View) || 'list'
  const q = sp.get('q') ?? ''
  const stage = sp.get('stage') ?? ''
  const tag = sp.get('tag') ?? ''
  const pub = sp.get('pub') ?? ''
  const lang = sp.get('lang') ?? ''
  const who = sp.get('who') ?? ''
  const sort = sp.get('sort') ?? 'num'

  const set = (k: string, v: string) => {
    const n = new URLSearchParams(sp)
    if (v) n.set(k, v)
    else n.delete(k)
    setSp(n, { replace: true })
  }
  const setView = (v: View) => {
    try {
      localStorage.setItem('crm.videos.view', v)
    } catch {
      /* ignore */
    }
    set('view', v)
  }

  const list = useMemo(() => {
    let xs = data ?? []
    const query = q.trim().toLowerCase()
    if (query) xs = xs.filter((v) => v.title.toLowerCase().includes(query) || v.code.toLowerCase().includes(query) || String(v.num) === query)
    if (stage && view === 'list') xs = xs.filter((v) => String(v.stage_id) === stage)
    if (tag) xs = xs.filter((v) => v.tags.includes(Number(tag)))
    if (lang) xs = xs.filter((v) => v.variants.some((x) => x.lang === lang))
    if (who) xs = xs.filter((v) => String(v.assignee_id) === who)
    if (pub === 'published') xs = xs.filter((v) => v.first_published_at)
    if (pub === 'unpublished') xs = xs.filter((v) => !v.first_published_at)
    if (pub === 'planned') xs = xs.filter((v) => !v.first_published_at && (v.next_plan_at || v.plan_date))
    if (pub === 'archive') xs = xs.filter((v) => v.archive.published && !v.archive.complete)
    const planKey = (v: Video) => v.plan_date ?? (v.next_plan_at ? dayOf(v.next_plan_at) : '9999')
    if (sort === 'plan') xs = [...xs].sort((a, b) => planKey(a).localeCompare(planKey(b)))
    if (sort === 'updated') xs = [...xs].sort((a, b) => b.updated_at - a.updated_at)
    return xs
  }, [data, q, stage, tag, lang, who, pub, sort, view])

  const filtered = !!(q || (stage && view === 'list') || tag || lang || who || pub)
  const seg = (
    <Seg
      value={view}
      onChange={setView}
      options={[
        { value: 'list', label: <List size={16} />, title: 'Список' },
        { value: 'board', label: <Columns3 size={16} />, title: 'Доска по этапам' },
      ]}
    />
  )

  return (
    <div className={clsx('page', view === 'board' && 'wide')}>
      <div className="page-head">
        <div className="hide-m">
          <h1>Ролики</h1>
          <div className="sub">{data ? `${data.length} всего` : ' '}</div>
        </div>
        <div className="actions hide-m">
          {seg}
          <Button variant="primary" icon={<Plus size={16} />} onClick={() => newVideo()}>
            Новый ролик
          </Button>
        </div>
      </div>

      <div className="toolbar">
        <div className="input-icon search">
          <Search size={15} />
          <input className="input" placeholder="Поиск по названию или коду" value={q} onChange={(e) => set('q', e.target.value)} />
        </div>
        <span className="hide-d">{seg}</span>
        <div className="filters">
          {view === 'list' && (
            <select className={clsx('select sm', stage && 'on')} style={{ width: 'auto' }} value={stage} onChange={(e) => set('stage', e.target.value)}>
              <option value="">Все этапы</option>
              {d.activeStages.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          )}
          <select className={clsx('select sm', pub && 'on')} style={{ width: 'auto' }} value={pub} onChange={(e) => set('pub', e.target.value)}>
            <option value="">Любой статус</option>
            <option value="unpublished">Не опубликованы</option>
            <option value="planned">Запланированы</option>
            <option value="published">Опубликованы</option>
            <option value="archive">Архив не собран</option>
          </select>
          <select className={clsx('select sm', tag && 'on')} style={{ width: 'auto' }} value={tag} onChange={(e) => set('tag', e.target.value)}>
            <option value="">Все теги</option>
            {d.groups('video').map(({ group, tags }) => (
              <optgroup key={group?.id ?? 0} label={group?.name ?? 'Без группы'}>
                {tags.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
          {d.activeLangs.length > 1 && (
            <select className={clsx('select sm', lang && 'on')} style={{ width: 'auto' }} value={lang} onChange={(e) => set('lang', e.target.value)}>
              <option value="">Все языки</option>
              {d.activeLangs.map((l) => (
                <option key={l.code} value={l.code}>
                  {l.flag} {l.name}
                </option>
              ))}
            </select>
          )}
          <select className={clsx('select sm', who && 'on')} style={{ width: 'auto' }} value={who} onChange={(e) => set('who', e.target.value)}>
            <option value="">Все исполнители</option>
            {d.users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.name}
              </option>
            ))}
          </select>
          {view === 'list' && (
            <select className="select sm" style={{ width: 'auto' }} value={sort} onChange={(e) => set('sort', e.target.value)}>
              <option value="num">Сначала новые</option>
              <option value="plan">По дате публикации</option>
              <option value="updated">Недавно изменённые</option>
            </select>
          )}
          {filtered && (
            <Button variant="ghost" size="sm" icon={<X size={14} />} onClick={() => setSp(view !== 'list' ? { view } : {}, { replace: true })}>
              Сбросить
            </Button>
          )}
          {filtered && data && (
            <span className="small muted nums" style={{ marginLeft: 'auto', whiteSpace: 'nowrap' }}>
              {list.length} из {data.length}
            </span>
          )}
        </div>
      </div>

      {isLoading ? (
        <Loading />
      ) : !data?.length ? (
        <Empty icon={<Clapperboard size={22} />} title="Роликов пока нет" action={<Button variant="primary" icon={<Plus size={16} />} onClick={() => newVideo()}>Создать первый</Button>}>
          Создайте ролик — к нему можно будет загрузить финал, голос, музыку, исходники и отметить публикации
        </Empty>
      ) : view === 'board' ? (
        <Board videos={list} />
      ) : list.length === 0 ? (
        <Empty icon={<Search size={22} />} title="Ничего не найдено" />
      ) : (
        <VideoList videos={list} />
      )}
    </div>
  )
}

function PubInfo({ v }: { v: Video }) {
  if (v.first_published_at) return <span className="small text-2">вышел {fmtDate(v.first_published_at)}</span>
  if (v.next_plan_at) return <span className="small" style={{ color: 'var(--blue-text)' }}>{relDay(dayOf(v.next_plan_at))}</span>
  if (v.plan_date) return <span className="small" style={{ color: 'var(--blue-text)' }}>план: {relDay(v.plan_date)}</span>
  return <span className="small muted">—</span>
}

function ArchiveInfo({ v }: { v: Video }) {
  if (!v.archive.published) return null
  if (v.archive.complete) return <span className="chip sm green" title="Обязательные материалы загружены">архив ✓</span>
  const left = v.archive.deadline_at ? fmtLeft(v.archive.deadline_at) : null
  return (
    <span className={clsx('chip sm', left?.overdue ? 'red' : 'amber')} title={left?.text}>
      <Archive size={11} /> −{v.archive.missing}
    </span>
  )
}

function VideoList({ videos }: { videos: Video[] }) {
  const d = useDicts()
  const nav = useNavigate()
  return (
    <>
      <div className="card hide-m" style={{ overflow: 'hidden' }}>
        <table className="vtable">
          <thead>
            <tr>
              <th style={{ width: 56 }} />
              <th>Ролик</th>
              <th>Этап</th>
              <th>Языки</th>
              <th>Публикация</th>
              <th>Архив</th>
              <th style={{ width: 40 }} />
            </tr>
          </thead>
          <tbody>
            {videos.map((v) => (
              <tr key={v.id} onClick={() => nav(`/videos/${v.id}`)}>
                <td>
                  <Thumb sha={v.thumb} size="sm" />
                </td>
                <td style={{ maxWidth: 420 }}>
                  <div className="row" style={{ gap: 6 }}>
                    <span className="code">{v.code}</span>
                    {!v.is_unique && <span className="chip sm">перезалив</span>}
                    {v.comment_count > 0 && (
                      <span className="muted tiny row" style={{ gap: 2 }}>
                        <MessageSquare size={11} />
                        {v.comment_count}
                      </span>
                    )}
                  </div>
                  <div className="ellipsis" style={{ fontWeight: 600 }}>
                    {v.title}
                  </div>
                  <TagChips ids={v.tags} max={3} />
                </td>
                <td>
                  <StagePill stage={v.stage_id ? d.stageById.get(v.stage_id) : null} size="sm" />
                </td>
                <td>
                  <LangBadges video={v} />
                </td>
                <td>
                  <PubInfo v={v} />
                </td>
                <td>
                  <ArchiveInfo v={v} />
                </td>
                <td>{v.assignee_id && <Avatar user={d.userById.get(v.assignee_id)} size="sm" />}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="vcards hide-d">
        {videos.map((v) => (
          <div key={v.id} className="vcard" onClick={() => nav(`/videos/${v.id}`)}>
            <Thumb sha={v.thumb} />
            <div className="meta">
              <div className="row" style={{ gap: 6 }}>
                <span className="code">{v.code}</span>
                <span className="grow" />
                <PubInfo v={v} />
              </div>
              <div className="t">{v.title}</div>
              <div className="row wrap" style={{ gap: 6 }}>
                <StagePill stage={v.stage_id ? d.stageById.get(v.stage_id) : null} size="sm" />
                <LangBadges video={v} />
                <ArchiveInfo v={v} />
              </div>
            </div>
          </div>
        ))}
      </div>
    </>
  )
}

function Board({ videos }: { videos: Video[] }) {
  const d = useDicts()
  const qc = useQueryClient()
  const [active, setActive] = useState<Video | null>(null)
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(TouchSensor, { activationConstraint: { delay: 220, tolerance: 8 } }))
  const cols = d.activeStages.map((s) => ({ stage: s, items: videos.filter((v) => v.stage_id === s.id) }))
  const orphans = videos.filter((v) => !v.stage_id || !d.activeStages.some((s) => s.id === v.stage_id))

  const onDragEnd = async (e: DragEndEvent) => {
    setActive(null)
    const id = Number(e.active.id)
    const to = e.over ? Number(e.over.id) : null
    const v = videos.find((x) => x.id === id)
    if (!v || !to || v.stage_id === to) return
    qc.setQueryData<Video[]>(['videos'], (old) => old?.map((x) => (x.id === id ? { ...x, stage_id: to } : x)))
    try {
      await patch(`/videos/${id}`, { stage_id: to })
    } finally {
      qc.invalidateQueries({ queryKey: ['videos'] })
      qc.invalidateQueries({ queryKey: ['video', id] })
    }
  }

  return (
    <DndContext sensors={sensors} onDragStart={(e) => setActive(videos.find((v) => v.id === Number(e.active.id)) ?? null)} onDragEnd={onDragEnd} onDragCancel={() => setActive(null)}>
      <div className="board">
        {cols.map(({ stage, items }) => (
          <Column key={stage.id} id={stage.id} title={stage.name} color={stage.color} items={items} />
        ))}
        {orphans.length > 0 && <Column id={-1} title="Без этапа" color="#a1a1aa" items={orphans} />}
      </div>
      <DragOverlay dropAnimation={null}>{active && <BoardCard v={active} overlay />}</DragOverlay>
    </DndContext>
  )
}

function Column({ id, title, color, items }: { id: ID; title: string; color: string; items: Video[] }) {
  const { setNodeRef, isOver } = useDroppable({ id })
  const newVideo = useNewVideo()
  return (
    <div ref={setNodeRef} className={clsx('board-col', isOver && 'over')}>
      <div className="board-col-head">
        <span className="dot" style={{ background: color }} />
        {title}
        <span className="n">{items.length}</span>
        {id > 0 && (
          <button className="icon-btn sm" style={{ marginLeft: 'auto' }} onClick={() => newVideo()} aria-label="Новый ролик">
            <Plus size={15} />
          </button>
        )}
      </div>
      {items.map((v) => (
        <Draggable key={v.id} v={v} />
      ))}
    </div>
  )
}
function Draggable({ v }: { v: Video }) {
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({ id: v.id })
  return (
    <div ref={setNodeRef} {...attributes} {...listeners}>
      <BoardCard v={v} dragging={isDragging} />
    </div>
  )
}

function BoardCard({ v, dragging, overlay }: { v: Video; dragging?: boolean; overlay?: boolean }) {
  const d = useDicts()
  const nav = useNavigate()
  return (
    <div className={clsx('bcard', dragging && 'dragging', overlay && 'overlaying')} onClick={() => !overlay && nav(`/videos/${v.id}`)}>
      <Thumb sha={v.thumb} size="sm" />
      <div className="grow" style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        <div className="row" style={{ gap: 6 }}>
          <span className="code">{v.code}</span>
          <span className="grow" />
          {v.assignee_id && <Avatar user={d.userById.get(v.assignee_id)} size="sm" />}
        </div>
        <div className="t">{v.title}</div>
        <div className="row wrap" style={{ gap: 5 }}>
          <LangBadges video={v} />
          <ArchiveInfo v={v} />
          {(v.plan_date || v.next_plan_at) && !v.first_published_at && (
            <span className="chip sm blue">
              <CalendarDays size={11} />
              {v.plan_date ? relDay(v.plan_date) : fmtDate(v.next_plan_at!)}
            </span>
          )}
          {v.check_total > 0 && (
            <span className="chip sm" title="Чек-лист">
              ✓ {v.check_done}/{v.check_total}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}
