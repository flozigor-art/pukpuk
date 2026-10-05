import clsx from 'clsx'
import { ArrowLeft, Copy, Download, Ellipsis, Music, Pause, Play, Plus, Search, Send, Trash, Upload, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { toast } from 'sonner'
import { LangBadges, StagePicker, TagChips, TagPicker, UserPicker } from '../components/pickers'
import { Avatar, Button, Empty, IconButton, Loading, Menu, MenuItem, MenuSep, Popover, Toggle, useConfirm } from '../components/ui'
import { fmtAgo, fmtBytes, fmtDateTime, fmtDuration } from '../lib/format'
import { ACTIONS, VARIANT_STATUS, VOICE } from '../lib/labels'
import { playTrack, usePlayer } from '../lib/player'
import { useAction, useActivity, useComments, useDicts, useTracks, useVideo, useVideos } from '../lib/queries'
import type { Activity, ID, VariantSummary, VideoDetail } from '../lib/types'
import { enqueue } from '../lib/uploads'
import { FeedItem } from './Dashboard'
import { ArchiveStatus, AssignModal, Materials } from './video/Materials'
import { Publications } from './video/Publications'

type Tab = 'files' | 'pubs' | 'text' | 'talk' | 'history'

export default function VideoPage() {
  const id = Number(useParams().id)
  const { data: v, isLoading, error } = useVideo(id)
  const d = useDicts()
  const [variantId, setVariantId] = useState<ID | null>(null)
  const [tab, setTab] = useState<Tab>('files')
  const [drop, setDrop] = useState<File[] | null>(null)
  const [dragging, setDragging] = useState(false)

  const variant = useMemo(() => {
    if (!v) return null
    return v.variants.find((x) => x.id === variantId) ?? v.variants.find((x) => x.lang === d.primaryLang) ?? v.variants[0] ?? null
  }, [v, variantId, d.primaryLang])

  // drop files anywhere on the page
  useEffect(() => {
    let depth = 0
    const hasFiles = (e: DragEvent) => e.dataTransfer?.types.includes('Files')
    const enter = (e: DragEvent) => {
      if (!hasFiles(e)) return
      depth++
      setDragging(true)
    }
    const leave = () => {
      depth = Math.max(0, depth - 1)
      if (!depth) setDragging(false)
    }
    const over = (e: DragEvent) => hasFiles(e) && e.preventDefault()
    const dropH = (e: DragEvent) => {
      depth = 0
      setDragging(false)
      if (!hasFiles(e)) return
      e.preventDefault()
      const files = [...(e.dataTransfer?.files ?? [])]
      if (files.length) setDrop(files)
    }
    window.addEventListener('dragenter', enter)
    window.addEventListener('dragleave', leave)
    window.addEventListener('dragover', over)
    window.addEventListener('drop', dropH)
    return () => {
      window.removeEventListener('dragenter', enter)
      window.removeEventListener('dragleave', leave)
      window.removeEventListener('dragover', over)
      window.removeEventListener('drop', dropH)
    }
  }, [])

  if (isLoading) return <Loading />
  if (error || !v) return <Empty title="Ролик не найден" action={<Link className="btn" to="/videos">К списку</Link>} />

  const uploadTo = (files: File[], kind?: string, toVariant?: VariantSummary | null) => {
    if (!kind) {
      setDrop(files)
      return
    }
    const target = toVariant === undefined ? variant : toVariant
    const lang = target ? target.lang.toUpperCase() : 'общие'
    enqueue(files, { type: 'asset', video_id: v.id, variant_id: target?.id ?? null, kind }, `${v.code} · ${d.kindByKey.get(kind)?.name ?? kind} · ${lang}`)
  }

  return (
    <div className="page">
      <VideoHeader v={v} />
      <div className="vlayout">
        <div style={{ minWidth: 0 }}>
          <div className="hide-d" style={{ marginBottom: 14 }}>
            <Props v={v} />
          </div>
          <div className="lang-tabs" style={{ marginBottom: 12 }}>
            {v.variants.map((x) => {
              const lang = d.langByCode.get(x.lang)
              const published = x.pubs.some((p) => p.status === 'published')
              return (
                <button key={x.id} className={clsx('lang-tab', variant?.id === x.id && 'on')} onClick={() => setVariantId(x.id)}>
                  <span>{lang?.flag}</span>
                  {x.lang.toUpperCase()}
                  <span className="st" style={{ background: published ? 'var(--accent)' : x.missing.length ? 'var(--border-strong)' : 'var(--blue)' }} />
                </button>
              )
            })}
            <AddLanguage video={v} onAdded={setVariantId} />
          </div>

          <div className="card">
            <div className="tabs" style={{ padding: '0 10px' }}>
              {(
                [
                  ['files', 'Материалы', v.asset_count],
                  ['pubs', 'Публикации', variant?.pubs.filter((p) => p.status === 'published').length],
                  ['text', 'Тексты', null],
                  ['talk', 'Обсуждение', v.comment_count],
                  ['history', 'История', null],
                ] as [Tab, string, number | null | undefined][]
              ).map(([key, label, n]) => (
                <button key={key} className={clsx(tab === key && 'on')} onClick={() => setTab(key)}>
                  {label}
                  {n ? <span className="badge">{n}</span> : null}
                </button>
              ))}
            </div>
            <div className="card-body" style={{ paddingTop: 14 }}>
              {tab === 'files' && (
                <>
                  {variant && (
                    <div className="row wrap" style={{ marginBottom: 6 }}>
                      <h3 style={{ fontSize: 14 }} className="grow">
                        {d.langByCode.get(variant.lang)?.flag} {d.langByCode.get(variant.lang)?.name ?? variant.lang} — материалы версии
                      </h3>
                      <ArchiveStatus variant={variant} />
                    </div>
                  )}
                  {variant && <Materials video={v} variant={variant} onFiles={(f, k) => uploadTo(f, k, variant)} />}
                  <div className="row" style={{ margin: '22px 0 6px' }}>
                    <h3 style={{ fontSize: 14 }} className="grow">
                      Общие для всех языков
                    </h3>
                    <span className="small muted hide-m">исходники, проект монтажа, музыка, SFX</span>
                  </div>
                  <Materials video={v} variant={null} onFiles={(f, k) => uploadTo(f, k, null)} />
                  <div className="row" style={{ marginTop: 16 }}>
                    <label className="btn">
                      <Upload size={15} /> Загрузить несколько файлов
                      <input
                        type="file"
                        multiple
                        hidden
                        onChange={(e) => {
                          const fs = [...(e.target.files ?? [])]
                          e.target.value = ''
                          if (fs.length) setDrop(fs)
                        }}
                      />
                    </label>
                    <span className="small muted hide-m">или перетащите файлы в окно — тип определится по имени</span>
                  </div>
                </>
              )}
              {tab === 'pubs' && variant && <Publications video={v} variant={variant} />}
              {tab === 'text' && variant && <Texts video={v} variant={variant} />}
              {tab === 'talk' && <Comments videoId={v.id} />}
              {tab === 'history' && <History videoId={v.id} />}
            </div>
          </div>
        </div>
        <Side v={v} />
      </div>
      {dragging && (
        <div className="dropzone-overlay">
          <div>
            <Upload size={18} /> Отпустите, чтобы загрузить в {v.code}
          </div>
        </div>
      )}
      {drop && <AssignModal video={v} files={drop} defaultVariant={variant?.id ?? null} onClose={() => setDrop(null)} />}
    </div>
  )
}

function VideoHeader({ v }: { v: VideoDetail }) {
  const act = useAction()
  const nav = useNavigate()
  const confirm = useConfirm()
  const [title, setTitle] = useState(v.title)
  useEffect(() => setTitle(v.title), [v.title])
  const saveTitle = () => {
    if (title.trim() && title !== v.title) act('PATCH', `/videos/${v.id}`, { title }, { invalidate: [['video', v.id], ['videos']] })
    else setTitle(v.title)
  }
  return (
    <div className="vhead">
      <Link to="/videos" className="icon-btn hide-m" aria-label="Назад" style={{ marginTop: 4 }}>
        <ArrowLeft size={18} />
      </Link>
      <div className="grow" style={{ minWidth: 0 }}>
        <div className="row" style={{ gap: 8, marginBottom: 2 }}>
          <span className="code">{v.code}</span>
          {!v.is_unique && <span className="chip sm">перезалив, не считается новым</span>}
          <LangBadges video={v} />
        </div>
        <input
          className="title-input"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={saveTitle}
          onKeyDown={(e) => {
            if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
            if (e.key === 'Escape') setTitle(v.title)
          }}
          aria-label="Название ролика"
        />
      </div>
      <a className="btn hide-m" href={`/api/videos/${v.id}/zip`} title="Скачать все материалы одним архивом">
        <Download size={15} /> Всё одним архивом
      </a>
      <Menu
        trigger={
          <IconButton label="Ещё">
            <Ellipsis size={18} />
          </IconButton>
        }
      >
        <MenuItem icon={<Download size={15} />} onSelect={() => (window.location.href = `/api/videos/${v.id}/zip`)}>
          Скачать всё (zip)
        </MenuItem>
        <MenuSep />
        <MenuItem
          icon={<Trash size={15} />}
          danger
          onSelect={async () => {
            if (await confirm({ title: `Удалить ${v.code}?`, text: 'Ролик со всеми файлами попадёт в корзину. Восстановить можно в разделе «Корзина».', confirm: 'В корзину', danger: true })) {
              await act('DELETE', `/videos/${v.id}`, undefined, { invalidate: [['videos'], ['dashboard']], success: 'Ролик перемещён в корзину' })
              nav('/videos')
            }
          }}
        >
          Удалить ролик
        </MenuItem>
      </Menu>
    </div>
  )
}

function AddLanguage({ video, onAdded }: { video: VideoDetail; onAdded: (id: ID) => void }) {
  const d = useDicts()
  const act = useAction()
  const [open, setOpen] = useState(false)
  const free = d.activeLangs.filter((l) => !video.variants.some((v) => v.lang === l.code))
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      trigger={
        <button className="lang-tab" style={{ fontWeight: 550, color: 'var(--text-2)' }}>
          <Plus size={15} /> Язык
        </button>
      }
    >
      <div className="menu-label">Добавить языковую версию</div>
      {free.map((l) => (
        <button
          key={l.code}
          className="menu-item"
          onClick={async () => {
            setOpen(false)
            const r = await act<{ id: ID }>('POST', `/videos/${video.id}/variants`, { lang: l.code }, { invalidate: [['video', video.id], ['videos']] })
            if (r) onAdded(r.id)
          }}
        >
          <span>{l.flag}</span>
          <span className="grow">{l.name}</span>
          <span className="muted">{l.code}</span>
        </button>
      ))}
      {free.length === 0 && <div className="small muted" style={{ padding: 9 }}>Все языки уже добавлены. Новые языки — в настройках.</div>}
      <div className="menu-sep" />
      <Link to="/settings?tab=languages" className="menu-item">
        Настроить языки
      </Link>
    </Popover>
  )
}

function Side({ v }: { v: VideoDetail }) {
  return (
    <div className="form" style={{ gap: 16 }}>
      <div className="hide-m">
        <Props v={v} />
      </div>
      <Checklist v={v} />
      <MusicCard v={v} />
    </div>
  )
}

function Props({ v }: { v: VideoDetail }) {
  const d = useDicts()
  const act = useAction()
  const videos = useVideos()
  const inv = [['video', v.id], ['videos'], ['calendar'], ['dashboard']]
  const set = (body: Record<string, unknown>) => act('PATCH', `/videos/${v.id}`, body, { invalidate: inv })
  const creator = v.created_by ? d.userById.get(v.created_by) : undefined
  return (
      <div className="card card-pad props">
        <div className="prop">
          <span className="k">Этап</span>
          <StagePicker value={v.stage_id} onChange={(id) => set({ stage_id: id })} />
        </div>
        <div className="prop">
          <span className="k">Ответственный</span>
          <UserPicker value={v.assignee_id} onChange={(id) => set({ assignee_id: id })} />
        </div>
        <div className="prop">
          <span className="k">Дата публикации</span>
          <div className="row">
            <input className="input sm" type="date" value={v.plan_date ?? ''} onChange={(e) => set({ plan_date: e.target.value || null })} style={{ maxWidth: 170 }} />
            {v.plan_date && (
              <IconButton label="Убрать дату" size="sm" onClick={() => set({ plan_date: null })}>
                <X size={14} />
              </IconButton>
            )}
          </div>
        </div>
        <div className="prop" style={{ alignItems: 'start' }}>
          <span className="k" style={{ paddingTop: 6 }}>
            Теги
          </span>
          <div className="row wrap" style={{ paddingTop: 3 }}>
            <TagChips ids={v.tags} />
            <TagPicker
              scope="video"
              value={v.tags}
              onChange={(tags) => act('PUT', `/videos/${v.id}/tags`, { tags }, { invalidate: [['video', v.id], ['videos']] })}
              trigger={
                <button className="chip outline" aria-label="Теги">
                  <Plus size={12} />
                </button>
              }
            />
          </div>
        </div>
        <div className="prop">
          <span className="k">Новый ролик</span>
          <div className="row">
            <Toggle on={v.is_unique} onChange={(on) => set({ is_unique: on, ...(on ? { original_id: null } : {}) })} label="Уникальный ролик" />
            <span className="small muted">{v.is_unique ? 'считается в план' : 'перезалив / вариация'}</span>
          </div>
        </div>
        {!v.is_unique && (
          <div className="prop">
            <span className="k">Оригинал</span>
            <select className="select sm" value={v.original_id ?? ''} onChange={(e) => set({ original_id: e.target.value ? Number(e.target.value) : null })}>
              <option value="">—</option>
              {videos.data
                ?.filter((x) => x.id !== v.id)
                .map((x) => (
                  <option key={x.id} value={x.id}>
                    {x.code} {x.title}
                  </option>
                ))}
            </select>
          </div>
        )}
        <div className="tiny muted" style={{ marginTop: 8 }}>
          Создал(а) {creator?.name ?? '—'} · {fmtDateTime(v.created_at)}
          {v.first_published_at && <> · вышел {fmtDateTime(v.first_published_at)}</>}
        </div>
      </div>
  )
}

function Checklist({ v }: { v: VideoDetail }) {
  const act = useAction()
  const d = useDicts()
  const [text, setText] = useState('')
  // optimistic state so the checkbox reacts instantly
  const [pending, setPending] = useState<Record<number, boolean>>({})
  const inv = [['video', v.id], ['videos']]
  const isDone = (c: { id: number; done_at: number | null }) => pending[c.id] ?? !!c.done_at
  const done = v.checklist.filter(isDone).length
  const toggle = async (id: number, value: boolean) => {
    setPending((p) => ({ ...p, [id]: value }))
    try {
      await act('PATCH', `/checklist/${id}`, { done: value }, { invalidate: inv })
    } finally {
      setPending(({ [id]: _, ...rest }) => rest)
    }
  }
  return (
    <div className="card">
      <div className="card-head">
        <h3>Чек-лист</h3>
        <span className="muted small">
          {done}/{v.checklist.length}
        </span>
      </div>
      <div className="card-body">
        <div className="checklist">
          {v.checklist.map((c) => (
            <label key={c.id} className={clsx('check-row', isDone(c) && 'done')} title={c.done_by ? `${d.userById.get(c.done_by)?.name}, ${fmtAgo(c.done_at!)}` : undefined}>
              <input type="checkbox" className="check" checked={isDone(c)} onChange={(e) => toggle(c.id, e.target.checked)} />
              <span className="lbl">{c.label}</span>
              <button
                className="icon-btn sm x"
                aria-label="Удалить пункт"
                onClick={(e) => {
                  e.preventDefault()
                  act('DELETE', `/checklist/${c.id}`, undefined, { invalidate: inv })
                }}
              >
                <X size={13} />
              </button>
            </label>
          ))}
        </div>
        <form
          className="row"
          style={{ marginTop: 8 }}
          onSubmit={async (e) => {
            e.preventDefault()
            if (!text.trim()) return
            await act('POST', `/videos/${v.id}/checklist`, { label: text }, { invalidate: inv })
            setText('')
          }}
        >
          <input className="input sm" placeholder="Новый пункт" value={text} onChange={(e) => setText(e.target.value)} />
          <IconButton label="Добавить" type="submit">
            <Plus size={16} />
          </IconButton>
        </form>
      </div>
    </div>
  )
}

function MusicCard({ v }: { v: VideoDetail }) {
  const tracks = useTracks()
  const act = useAction()
  const player = usePlayer()
  const [q, setQ] = useState('')
  const linked = (tracks.data ?? []).filter((t) => v.tracks.includes(t.id))
  const setTracks = (ids: ID[]) => act('PUT', `/videos/${v.id}/tracks`, { tracks: ids }, { invalidate: [['video', v.id], ['music']] })
  const query = q.toLowerCase()
  return (
    <div className="card">
      <div className="card-head">
        <Music size={15} />
        <h3>Музыка</h3>
        <span className="more">
          <Popover
            align="end"
            trigger={
              <Button size="sm" variant="ghost" icon={<Plus size={14} />}>
                Из библиотеки
              </Button>
            }
          >
            <div className="input-icon" style={{ margin: 2, marginBottom: 6 }}>
              <Search size={15} />
              <input className="input sm" placeholder="Поиск трека" value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
            </div>
            {(tracks.data ?? [])
              .filter((t) => !query || `${t.title} ${t.artist}`.toLowerCase().includes(query))
              .slice(0, 60)
              .map((t) => {
                const on = v.tracks.includes(t.id)
                return (
                  <button key={t.id} className="menu-item" onClick={() => setTracks(on ? v.tracks.filter((x) => x !== t.id) : [...v.tracks, t.id])}>
                    <input type="checkbox" className="check" checked={on} readOnly tabIndex={-1} />
                    <span className="grow ellipsis">
                      {t.title}
                      <span className="muted"> · {t.artist || '—'}</span>
                    </span>
                  </button>
                )
              })}
            {tracks.data?.length === 0 && <div className="small muted" style={{ padding: 9 }}>Библиотека пуста</div>}
          </Popover>
        </span>
      </div>
      <div className="card-body form" style={{ gap: 10 }}>
        {linked.map((t) => {
          const playing = player.track?.id === t.id && player.playing
          return (
            <div key={t.id} className="row">
              <button className="icon-btn" onClick={() => playTrack(t, linked)} aria-label="Играть">
                {playing ? <Pause size={15} /> : <Play size={15} />}
              </button>
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="ellipsis" style={{ fontWeight: 550 }}>
                  {t.title}
                </div>
                <div className="tiny muted ellipsis">
                  {t.artist || '—'} · {fmtDuration(t.duration_ms)}
                  {t.license ? ` · ${t.license}` : ' · лицензия не указана'}
                </div>
              </div>
              <IconButton label="Отвязать" size="sm" onClick={() => setTracks(v.tracks.filter((x) => x !== t.id))}>
                <X size={14} />
              </IconButton>
            </div>
          )
        })}
        <AutoText
          value={v.music_note ?? ''}
          placeholder="Музыка не из библиотеки: источник и условия лицензии (п.14.3)"
          onSave={(music_note) => act('PATCH', `/videos/${v.id}`, { music_note }, { invalidate: [['video', v.id]], silent: true })}
          rows={2}
        />
      </div>
    </div>
  )
}

/** Textarea that saves itself on blur and after a pause in typing. */
export function AutoText({ value, onSave, placeholder, rows = 4, label }: { value: string; onSave: (v: string) => Promise<unknown>; placeholder?: string; rows?: number; label?: ReactNode }) {
  const [text, setText] = useState(value)
  const [state, setState] = useState<'idle' | 'saving' | 'saved'>('idle')
  const timer = useRef<number | undefined>(undefined)
  const last = useRef(value)
  const focused = useRef(false)
  useEffect(() => {
    if (!focused.current) {
      setText(value)
      last.current = value
    }
  }, [value])
  const save = async (t: string) => {
    if (t === last.current) return
    setState('saving')
    try {
      await onSave(t)
      last.current = t
      setState('saved')
      window.setTimeout(() => setState('idle'), 1500)
    } catch {
      setState('idle')
    }
  }
  return (
    <div className="field">
      {(label || state !== 'idle') && (
        <div className="row">
          {label && <label className="label grow">{label}</label>}
          {state !== 'idle' && <span className="tiny muted">{state === 'saving' ? 'Сохранение…' : 'Сохранено'}</span>}
        </div>
      )}
      <textarea
        className="textarea"
        rows={rows}
        value={text}
        placeholder={placeholder}
        onFocus={() => (focused.current = true)}
        onChange={(e) => {
          setText(e.target.value)
          window.clearTimeout(timer.current)
          const t = e.target.value
          timer.current = window.setTimeout(() => save(t), 1200)
        }}
        onBlur={() => {
          focused.current = false
          window.clearTimeout(timer.current)
          save(text)
        }}
      />
    </div>
  )
}

function Texts({ video, variant }: { video: VideoDetail; variant: VariantSummary }) {
  const d = useDicts()
  const act = useAction()
  const confirm = useConfirm()
  const inv = [['video', video.id], ['videos']]
  const setVariant = (body: Record<string, unknown>) => act('PATCH', `/variants/${variant.id}`, body, { invalidate: inv, silent: true })
  const lang = d.langByCode.get(variant.lang)
  const isPrimary = variant.lang === d.primaryLang
  return (
    <div className="form" style={{ gap: 18 }}>
      <div className="grid-2">
        <div className="field">
          <label>Озвучка ({lang?.name ?? variant.lang})</label>
          <select className="select" value={variant.voice} onChange={(e) => setVariant({ voice: e.target.value })}>
            {Object.entries(VOICE).map(([k, l]) => (
              <option key={k} value={k}>
                {l}
              </option>
            ))}
          </select>
        </div>
        {!isPrimary && (
          <div className="field">
            <label>Локализация</label>
            <select className="select" value={variant.status} onChange={(e) => setVariant({ status: e.target.value })}>
              {Object.entries(VARIANT_STATUS).map(([k, s]) => (
                <option key={k} value={k}>
                  {s.label}
                </option>
              ))}
            </select>
          </div>
        )}
      </div>
      {variant.voice === 'ai' && <div className="banner red">Синтез голоса Изабеллы требует отдельного письменного согласия (п.16.4 договора).</div>}
      <div className="field">
        <label>Название для публикации ({variant.lang.toUpperCase()})</label>
        <AutoInput value={variant.title} placeholder={video.title} onSave={(title) => setVariant({ title })} />
      </div>
      <div className="copy-area">
        <AutoText label={`Описание публикации (${variant.lang.toUpperCase()}) — п.7.5`} value={variant.caption ?? ''} placeholder="Текст под роликом, хэштеги" onSave={(caption) => setVariant({ caption })} rows={5} />
        <IconButton
          label="Скопировать"
          size="sm"
          style={{ top: 26 }}
          onClick={() => {
            navigator.clipboard?.writeText(variant.caption ?? '').then(() => toast.success('Описание скопировано'))
          }}
        >
          <Copy size={14} />
        </IconButton>
      </div>
      <AutoText label="Сценарий" value={video.script ?? ''} placeholder="Текст сценария (общий для всех языков)" onSave={(script) => act('PATCH', `/videos/${video.id}`, { script }, { invalidate: [['video', video.id]], silent: true })} rows={8} />
      <AutoText label="Заметки" value={video.notes ?? ''} placeholder="Идеи, ссылки на референсы, что снять" onSave={(notes) => act('PATCH', `/videos/${video.id}`, { notes }, { invalidate: [['video', video.id]], silent: true })} rows={3} />
      {!isPrimary && (
        <div>
          <Button
            variant="danger"
            size="sm"
            icon={<Trash size={14} />}
            onClick={async () => {
              if (await confirm({ title: `Удалить версию ${variant.lang.toUpperCase()}?`, text: 'Можно удалить только пустую версию — без файлов и публикаций.', confirm: 'Удалить', danger: true }))
                act('DELETE', `/variants/${variant.id}`, undefined, { invalidate: inv })
            }}
          >
            Удалить языковую версию
          </Button>
        </div>
      )}
    </div>
  )
}

function AutoInput({ value, onSave, placeholder }: { value: string; onSave: (v: string) => Promise<unknown>; placeholder?: string }) {
  const [text, setText] = useState(value)
  useEffect(() => setText(value), [value])
  return (
    <input
      className="input"
      value={text}
      placeholder={placeholder}
      onChange={(e) => setText(e.target.value)}
      onBlur={() => text !== value && onSave(text)}
      onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
    />
  )
}

function Comments({ videoId }: { videoId: ID }) {
  const d = useDicts()
  const act = useAction()
  const { data } = useComments(videoId)
  const [text, setText] = useState('')
  const send = async () => {
    if (!text.trim()) return
    await act('POST', `/videos/${videoId}/comments`, { body: text }, { invalidate: [['comments', videoId], ['video', videoId]] })
    setText('')
  }
  return (
    <div>
      {data?.length === 0 && <div className="muted small" style={{ padding: '6px 0 14px' }}>Пока нет комментариев. Обсудите правки, монтаж, идеи.</div>}
      {data?.map((c) => {
        const u = d.userById.get(c.user_id)
        return (
          <div key={c.id} className="comment">
            <Avatar user={u} />
            <div className="grow" style={{ minWidth: 0 }}>
              <div className="row">
                <b>{u?.name}</b>
                <span className="tiny muted">{fmtAgo(c.created_at)}</span>
                {(c.user_id === d.me.id || d.isAdmin) && (
                  <button className="icon-btn sm" style={{ marginLeft: 'auto' }} aria-label="Удалить" onClick={() => act('DELETE', `/comments/${c.id}`, undefined, { invalidate: [['comments', videoId], ['video', videoId]] })}>
                    <X size={13} />
                  </button>
                )}
              </div>
              <div className="body">{c.body}</div>
            </div>
          </div>
        )
      })}
      <div className="row" style={{ alignItems: 'flex-end', marginTop: 10 }}>
        <textarea
          className="textarea"
          rows={2}
          style={{ minHeight: 44 }}
          placeholder="Написать комментарий… (Ctrl+Enter — отправить)"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send()
          }}
        />
        <Button variant="primary" onClick={send} disabled={!text.trim()} icon={<Send size={15} />} style={{ height: 44 }}>
          <span className="hide-m">Отправить</span>
        </Button>
      </div>
    </div>
  )
}

function History({ videoId }: { videoId: ID }) {
  const { data } = useActivity(videoId)
  if (!data) return <Loading />
  if (!data.length) return <div className="muted small">Пусто</div>
  return (
    <div>
      {data.map((a) => (
        <FeedItem key={a.id} a={a} showVideo={false} />
      ))}
    </div>
  )
}

export function ActivityText({ a }: { a: Activity }) {
  const d = useDicts()
  const data = a.data as Record<string, string | number | null>
  const base = ACTIONS[a.action] ?? a.action
  switch (a.action) {
    case 'asset.uploaded':
      return (
        <span>
          {base} {d.kindByKey.get(String(data.kind))?.name?.toLowerCase() ?? data.kind}
          {data.lang ? ` (${String(data.lang).toUpperCase()})` : ''} «{data.filename}», {fmtBytes(Number(data.size))}
        </span>
      )
    case 'video.stage': {
      const to = d.stageById.get(Number(data.to))
      return (
        <span>
          {base} → <b>{to?.name ?? '?'}</b>
        </span>
      )
    }
    case 'video.plan':
      return (
        <span>
          {base}: {data.to ?? 'без даты'}
        </span>
      )
    case 'publication.published':
    case 'publication.scheduled':
      return (
        <span>
          {base} в {data.platform} ({String(data.lang).toUpperCase()})
        </span>
      )
    case 'variant.created':
    case 'variant.deleted':
      return (
        <span>
          {base} {String(data.lang).toUpperCase()}
        </span>
      )
    case 'comment.added':
      return (
        <span>
          {base}: «{data.text}»
        </span>
      )
    case 'asset.deleted':
      return (
        <span>
          {base} «{data.filename}»
        </span>
      )
    case 'video.renamed':
      return (
        <span>
          {base} в «{data.to}»
        </span>
      )
    case 'video.purged':
      return (
        <span>
          {base} {data.code} «{data.title}»
        </span>
      )
    case 'day.excused':
      return (
        <span>
          {base} на {data.date}
        </span>
      )
    default:
      return <span>{base}</span>
  }
}

