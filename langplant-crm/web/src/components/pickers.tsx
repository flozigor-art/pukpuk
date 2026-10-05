import clsx from 'clsx'
import { Check, Plus, Search } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { post } from '../lib/api'
import { queryClient, useDicts } from '../lib/queries'
import type { ID, TagScope, Video } from '../lib/types'
import { Avatar, Popover, PopoverClose, StagePill } from './ui'

export function StagePicker({ value, onChange, trigger }: { value: ID | null; onChange: (id: ID) => void; trigger?: ReactNode }) {
  const d = useDicts()
  const [open, setOpen] = useState(false)
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      trigger={
        trigger ?? (
          <button className="prop-btn">
            <StagePill stage={value ? d.stageById.get(value) : null} />
          </button>
        )
      }
    >
      {d.activeStages.map((s) => (
        <button
          key={s.id}
          className="menu-item"
          onClick={() => {
            onChange(s.id)
            setOpen(false)
          }}
        >
          <span className="dot" style={{ background: s.color }} />
          <span className="grow">{s.name}</span>
          {s.id === value && <Check size={15} />}
        </button>
      ))}
    </Popover>
  )
}

export function UserPicker({ value, onChange, allowNone = true }: { value: ID | null; onChange: (id: ID | null) => void; allowNone?: boolean }) {
  const d = useDicts()
  const [open, setOpen] = useState(false)
  const u = value ? d.userById.get(value) : null
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      trigger={
        <button className="prop-btn">
          {u ? (
            <>
              <Avatar user={u} size="sm" />
              {u.name}
            </>
          ) : (
            <span className="muted">Не назначен</span>
          )}
        </button>
      }
    >
      {allowNone && (
        <button
          className="menu-item"
          onClick={() => {
            onChange(null)
            setOpen(false)
          }}
        >
          <span className="avatar sm" style={{ background: 'var(--surface-3)' }} />
          <span className="grow muted">Не назначен</span>
          {!value && <Check size={15} />}
        </button>
      )}
      {d.users
        .filter((x) => !x.disabled)
        .map((x) => (
          <button
            key={x.id}
            className="menu-item"
            onClick={() => {
              onChange(x.id)
              setOpen(false)
            }}
          >
            <Avatar user={x} size="sm" />
            <span className="grow">{x.name}</span>
            {x.id === value && <Check size={15} />}
          </button>
        ))}
    </Popover>
  )
}

export function TagChips({ ids, scope, max = 99 }: { ids: ID[]; scope?: TagScope; max?: number }) {
  const d = useDicts()
  const tags = ids.map((id) => d.tagById.get(id)).filter((t) => t && (!scope || t.scope === scope))
  if (!tags.length) return null
  return (
    <span className="chips">
      {tags.slice(0, max).map((t) => (
        <span key={t!.id} className="chip sm" style={t!.color ? { background: `color-mix(in srgb, ${t!.color} 18%, transparent)`, color: `color-mix(in srgb, ${t!.color} 70%, var(--text))` } : undefined}>
          {t!.name}
        </span>
      ))}
      {tags.length > max && <span className="chip sm">+{tags.length - max}</span>}
    </span>
  )
}

export function TagPicker({ scope, value, onChange, trigger }: { scope: TagScope; value: ID[]; onChange: (ids: ID[]) => void; trigger: ReactNode }) {
  const d = useDicts()
  const [q, setQ] = useState('')
  const groups = d.groups(scope)
  const sel = new Set(value)
  const query = q.trim().toLowerCase()
  const exact = d.tags.some((t) => t.scope === scope && t.name.toLowerCase() === query)
  const toggle = (id: ID) => onChange(sel.has(id) ? value.filter((x) => x !== id) : [...value, id])
  const create = async () => {
    const res = await post<{ id: ID }>(`/dict/tags`, { scope, name: q.trim() })
    await queryClient.invalidateQueries({ queryKey: ['bootstrap'] })
    onChange([...value, res.id])
    setQ('')
  }
  return (
    <Popover trigger={trigger}>
      <div className="input-icon" style={{ margin: 2, marginBottom: 6 }}>
        <Search size={15} />
        <input
          className="input sm"
          placeholder="Найти или создать тег"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && query && !exact) create()
          }}
          autoFocus
        />
      </div>
      {groups.map(({ group, tags }) => {
        const shown = tags.filter((t) => !query || t.name.toLowerCase().includes(query))
        if (!shown.length) return null
        return (
          <div key={group?.id ?? 'none'}>
            <div className="menu-label">{group?.name ?? 'Без группы'}</div>
            {shown.map((t) => (
              <button key={t.id} className="menu-item" onClick={() => toggle(t.id)}>
                <input type="checkbox" className="check" checked={sel.has(t.id)} readOnly tabIndex={-1} />
                <span className="grow">{t.name}</span>
              </button>
            ))}
          </div>
        )
      })}
      {query && !exact && (
        <button className="menu-item" onClick={create}>
          <Plus size={15} />
          <span className="grow">
            Создать «<b>{q.trim()}</b>»
          </span>
        </button>
      )}
      <div className="menu-sep" />
      <PopoverClose asChild>
        <button className="menu-item" style={{ justifyContent: 'center', fontWeight: 600 }}>
          Готово
        </button>
      </PopoverClose>
    </Popover>
  )
}

/** Language badges for a video: green = published, blue = planned, grey = in work. */
export function LangBadges({ video }: { video: Video }) {
  return (
    <span className="flags">
      {video.variants.map((v) => {
        const published = v.pubs.some((p) => p.status === 'published')
        const planned = v.pubs.some((p) => p.status === 'planned' || p.status === 'scheduled')
        return (
          <span key={v.id} className={clsx('lang', published ? 'pub' : planned && 'plan')} title={published ? 'Опубликовано' : planned ? 'Запланировано' : 'Не опубликовано'}>
            {v.lang}
          </span>
        )
      })}
    </span>
  )
}

export function useStageCounts(videos: Video[] | undefined) {
  return useMemo(() => {
    const m = new Map<ID | null, number>()
    for (const v of videos ?? []) m.set(v.stage_id, (m.get(v.stage_id) ?? 0) + 1)
    return m
  }, [videos])
}
