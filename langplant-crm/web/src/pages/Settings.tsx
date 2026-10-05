import clsx from 'clsx'
import { ArrowDown, ArrowUp, Archive, KeyRound, Plus, Trash, UserPlus } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { PageHead } from '../components/Layout'
import { Avatar, Button, Field, IconButton, Modal, PLATFORM_ICONS, PlatformIcon, Toggle, useConfirm } from '../components/ui'
import { STAGE_KIND } from '../lib/labels'
import { useAction, useDicts } from '../lib/queries'
import type { ID, TagScope, User } from '../lib/types'

type TabKey = 'profile' | 'users' | 'platforms' | 'languages' | 'stages' | 'kinds' | 'checklist' | 'tags' | 'plan'

export default function SettingsPage() {
  const d = useDicts()
  const [sp, setSp] = useSearchParams()
  const tab = (sp.get('tab') as TabKey) || 'profile'
  const tabs: [TabKey, string, boolean?][] = [
    ['profile', 'Профиль'],
    ['users', 'Пользователи', true],
    ['platforms', 'Площадки и аккаунты'],
    ['languages', 'Языки'],
    ['stages', 'Этапы'],
    ['kinds', 'Типы файлов'],
    ['checklist', 'Чек-лист'],
    ['tags', 'Теги'],
    ['plan', 'План публикаций', true],
  ]
  return (
    <div className="page narrow">
      <PageHead title="Настройки" sub={`LangPlant CRM ${d.config.version}`} />
      <div className="tabs" style={{ marginBottom: 18, flexWrap: 'wrap' }}>
        {tabs
          .filter(([, , admin]) => !admin || d.isAdmin || true)
          .map(([k, l]) => (
            <button key={k} className={clsx(tab === k && 'on')} onClick={() => setSp({ tab: k }, { replace: true })}>
              {l}
            </button>
          ))}
      </div>
      {tab === 'profile' && <Profile />}
      {tab === 'users' && <Users />}
      {tab === 'platforms' && <Platforms />}
      {tab === 'languages' && <Languages />}
      {tab === 'stages' && <Stages />}
      {tab === 'kinds' && <Kinds />}
      {tab === 'checklist' && <ChecklistTpl />}
      {tab === 'tags' && <Tags />}
      {tab === 'plan' && <Plan />}
    </div>
  )
}

function useDict() {
  const act = useAction()
  const inv = [['bootstrap'], ['videos']]
  return {
    create: (type: string, body: object) => act('POST', `/dict/${type}`, body, { invalidate: inv }),
    patch: (type: string, id: ID | string, body: object) => act('PATCH', `/dict/${type}/${id}`, body, { invalidate: inv }),
    remove: (type: string, id: ID | string) => act('DELETE', `/dict/${type}/${id}`, undefined, { invalidate: inv }),
    reorder: (type: string, ids: (ID | string)[]) => act('POST', `/dict/${type}/reorder`, { ids }, { invalidate: inv }),
  }
}

function Inline({ value, onSave, placeholder, className, style }: { value: string; onSave: (v: string) => void; placeholder?: string; className?: string; style?: React.CSSProperties }) {
  const [v, setV] = useState(value)
  useEffect(() => setV(value), [value])
  return (
    <input
      className={clsx('input sm', className)}
      style={style}
      value={v}
      placeholder={placeholder}
      onChange={(e) => setV(e.target.value)}
      onBlur={() => v !== value && onSave(v)}
      onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
    />
  )
}

function Card({ title, hint, children, actions }: { title?: string; hint?: ReactNode; children: ReactNode; actions?: ReactNode }) {
  return (
    <div className="card" style={{ marginBottom: 16 }}>
      {title && (
        <div className="card-head">
          <h3 className="grow">{title}</h3>
          {actions}
        </div>
      )}
      <div className="card-body">
        {hint && <div className="small muted" style={{ marginBottom: 10 }}>{hint}</div>}
        {children}
      </div>
    </div>
  )
}

function Move<T>({ list, index, onMove }: { list: T[]; index: number; onMove: (l: T[]) => void }) {
  const swap = (a: number, b: number) => {
    const n = [...list]
    ;[n[a], n[b]] = [n[b], n[a]]
    onMove(n)
  }
  return (
    <span className="row" style={{ gap: 0 }}>
      <IconButton label="Выше" size="sm" disabled={index === 0} onClick={() => swap(index, index - 1)}>
        <ArrowUp size={14} />
      </IconButton>
      <IconButton label="Ниже" size="sm" disabled={index === list.length - 1} onClick={() => swap(index, index + 1)}>
        <ArrowDown size={14} />
      </IconButton>
    </span>
  )
}

function DeleteBtn({ onDelete, what }: { onDelete: () => void; what: string }) {
  const confirm = useConfirm()
  return (
    <IconButton
      label="Удалить"
      size="sm"
      onClick={async () => {
        if (await confirm({ title: `Удалить ${what}?`, text: 'Если запись уже используется, удалить не получится — её можно архивировать.', confirm: 'Удалить', danger: true })) onDelete()
      }}
    >
      <Trash size={14} />
    </IconButton>
  )
}

function ArchiveBtn({ archived, onChange }: { archived: boolean; onChange: (v: boolean) => void }) {
  return (
    <IconButton label={archived ? 'Вернуть из архива' : 'В архив (скрыть из списков)'} size="sm" active={archived} onClick={() => onChange(!archived)}>
      <Archive size={14} />
    </IconButton>
  )
}

// ---- profile ---------------------------------------------------------------

function Profile() {
  const d = useDicts()
  const act = useAction()
  const [old, setOld] = useState('')
  const [pw, setPw] = useState('')
  const me = d.me
  return (
    <>
      <Card title="Профиль">
        <div className="form">
          <div className="row">
            <Avatar user={me} size="lg" />
            <div className="grow">
              <b>{me.name}</b>
              <div className="small muted">
                {me.login} · {me.role === 'admin' ? 'администратор' : 'участник'}
              </div>
            </div>
          </div>
          <div className="grid-2">
            <Field label="Имя">
              <Inline value={me.name} onSave={(name) => act('PATCH', `/users/${me.id}`, { name }, { invalidate: [['bootstrap']] })} />
            </Field>
            <Field label="Цвет">
              <input type="color" className="color-dot" style={{ width: 44, height: 34 }} value={me.color} onChange={(e) => act('PATCH', `/users/${me.id}`, { color: e.target.value }, { invalidate: [['bootstrap']] })} />
            </Field>
          </div>
        </div>
      </Card>
      <Card title="Смена пароля">
        <form
          className="form"
          onSubmit={async (e) => {
            e.preventDefault()
            await act('POST', `/users/${me.id}/password`, { old, new: pw }, { success: 'Пароль изменён. Другие сеансы завершены.' })
            setOld('')
            setPw('')
          }}
        >
          <div className="grid-2">
            <Field label="Текущий пароль">
              <input className="input" type="password" autoComplete="current-password" value={old} onChange={(e) => setOld(e.target.value)} />
            </Field>
            <Field label="Новый пароль" hint="Минимум 8 символов">
              <input className="input" type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} />
            </Field>
          </div>
          <div>
            <Button variant="primary" type="submit" disabled={pw.length < 8 || !old}>
              Изменить пароль
            </Button>
          </div>
        </form>
      </Card>
    </>
  )
}

// ---- users -----------------------------------------------------------------

function Users() {
  const d = useDicts()
  const act = useAction()
  const confirm = useConfirm()
  const [adding, setAdding] = useState(false)
  const [shown, setShown] = useState<{ login: string; pw: string } | null>(null)
  const inv = [['bootstrap']]
  const reset = async (u: User) => {
    if (!(await confirm({ title: `Сбросить пароль ${u.name}?`, text: 'Будет создан новый случайный пароль, все сеансы пользователя завершатся.', confirm: 'Сбросить' }))) return
    const pw = randomPassword()
    await act('POST', `/users/${u.id}/password`, { new: pw })
    setShown({ login: u.login, pw })
  }
  return (
    <Card title="Пользователи" hint="Все участники могут загружать материалы и вести ролики. Администратор управляет пользователями, планом и может удалять навсегда." actions={d.isAdmin && <Button size="sm" icon={<UserPlus size={14} />} onClick={() => setAdding(true)}>Добавить</Button>}>
      {d.users.map((u) => (
        <div key={u.id} className="dict-row">
          <Avatar user={u} />
          <div className="grow" style={{ minWidth: 0 }}>
            <b>{u.name}</b>
            <div className="tiny muted">
              {u.login}
              {u.disabled && ' · заблокирован'}
            </div>
          </div>
          {d.isAdmin && u.id !== d.me.id ? (
            <>
              <select className="select sm" style={{ width: 'auto' }} value={u.role} onChange={(e) => act('PATCH', `/users/${u.id}`, { role: e.target.value }, { invalidate: inv })}>
                <option value="member">Участник</option>
                <option value="admin">Админ</option>
              </select>
              <IconButton label="Сбросить пароль" size="sm" onClick={() => reset(u)}>
                <KeyRound size={14} />
              </IconButton>
              <Toggle on={!u.disabled} onChange={(on) => act('PATCH', `/users/${u.id}`, { disabled: !on }, { invalidate: inv })} label="Доступ" />
            </>
          ) : (
            <span className="chip sm">{u.role === 'admin' ? 'Админ' : 'Участник'}</span>
          )}
        </div>
      ))}
      {adding && <AddUser onClose={() => setAdding(false)} onCreated={(login, pw) => setShown({ login, pw })} />}
      {shown && (
        <Modal open onClose={() => setShown(null)} title="Пароль" footer={<Button variant="primary" onClick={() => setShown(null)}>Готово</Button>}>
          <div className="form">
            <div className="text-2">Передайте данные для входа пользователю — повторно пароль показан не будет.</div>
            <div className="card card-pad mono" style={{ fontSize: 15 }}>
              логин: {shown.login}
              <br />
              пароль: {shown.pw}
            </div>
            <Button onClick={() => navigator.clipboard?.writeText(`${location.origin}\nлогин: ${shown.login}\nпароль: ${shown.pw}`).then(() => toast.success('Скопировано'))}>Скопировать</Button>
          </div>
        </Modal>
      )}
    </Card>
  )
}

function randomPassword() {
  const abc = 'abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'
  const b = crypto.getRandomValues(new Uint32Array(12))
  const s = [...b].map((x) => abc[x % abc.length]).join('')
  return `${s.slice(0, 4)}-${s.slice(4, 8)}-${s.slice(8, 12)}`
}

function AddUser({ onClose, onCreated }: { onClose: () => void; onCreated: (login: string, pw: string) => void }) {
  const act = useAction()
  const [login, setLogin] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState('member')
  const submit = async () => {
    const pw = randomPassword()
    await act('POST', '/users', { login, name, role, password: pw, color: '#' + Math.floor(Math.random() * 0xffffff).toString(16).padStart(6, '0') }, { invalidate: [['bootstrap']] })
    onClose()
    onCreated(login.toLowerCase(), pw)
  }
  return (
    <Modal open onClose={onClose} title="Новый пользователь" footer={<><Button onClick={onClose}>Отмена</Button><Button variant="primary" onClick={submit} disabled={!login}>Создать</Button></>}>
      <div className="form">
        <Field label="Логин" hint="латиница, например masha">
          <input className="input" value={login} onChange={(e) => setLogin(e.target.value)} autoCapitalize="none" />
        </Field>
        <Field label="Имя">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Роль">
          <select className="select" value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="member">Участник</option>
            <option value="admin">Администратор</option>
          </select>
        </Field>
      </div>
    </Modal>
  )
}

// ---- platforms & channels ----------------------------------------------------

function Platforms() {
  const d = useDicts()
  const dict = useDict()
  const [name, setName] = useState('')
  return (
    <>
      <Card hint="Площадки и конкретные аккаунты на них. Аккаунт можно привязать к языку — тогда он появится в публикациях только этой языковой версии. «Считается в план» — публикация на площадке засчитывается в ежедневный план (TikTok, Shorts и Reels по договору).">
        <form
          className="row"
          onSubmit={async (e) => {
            e.preventDefault()
            if (!name.trim()) return
            await dict.create('platforms', { name, color: '#71717a', icon: '' })
            setName('')
          }}
        >
          <input className="input" placeholder="Новая площадка, например VK Клипы" value={name} onChange={(e) => setName(e.target.value)} />
          <Button type="submit" icon={<Plus size={15} />}>
            Добавить
          </Button>
        </form>
      </Card>
      {d.platforms.map((p, i) => (
        <div key={p.id} className="card" style={{ marginBottom: 12, opacity: p.archived ? 0.6 : 1 }}>
          <div className="card-body">
            <div className="row wrap">
              <PlatformIcon platform={p} />
              <Inline value={p.name} onSave={(v) => dict.patch('platforms', p.id, { name: v })} style={{ maxWidth: 220, fontWeight: 600 }} />
              <input type="color" className="color-dot" value={p.color} onChange={(e) => dict.patch('platforms', p.id, { color: e.target.value })} title="Цвет" />
              <select className="select sm" style={{ width: 'auto' }} value={p.icon} onChange={(e) => dict.patch('platforms', p.id, { icon: e.target.value })} title="Иконка">
                <option value="">Буква</option>
                {PLATFORM_ICONS.map((x) => (
                  <option key={x} value={x}>
                    {x}
                  </option>
                ))}
              </select>
              <span className="row small text-2" style={{ gap: 6 }}>
                <Toggle on={p.counts_for_quota} onChange={(v) => dict.patch('platforms', p.id, { counts_for_quota: v })} label="Считается в план" /> в план
              </span>
              <span className="grow" />
              <Move list={d.platforms.map((x) => x.id)} index={i} onMove={(ids) => dict.reorder('platforms', ids)} />
              <ArchiveBtn archived={p.archived} onChange={(v) => dict.patch('platforms', p.id, { archived: v })} />
              <DeleteBtn what="площадку" onDelete={() => dict.remove('platforms', p.id)} />
            </div>
            <div style={{ marginTop: 10, paddingLeft: 30 }}>
              {d.channels
                .filter((c) => c.platform_id === p.id)
                .map((c) => (
                  <div key={c.id} className="dict-row" style={{ flexWrap: 'wrap', opacity: c.archived ? 0.55 : 1 }}>
                    <Inline value={c.name} onSave={(v) => dict.patch('channels', c.id, { name: v })} style={{ maxWidth: 200 }} placeholder="@аккаунт" />
                    <select className="select sm" style={{ width: 'auto' }} value={c.language_code ?? ''} onChange={(e) => dict.patch('channels', c.id, { language_code: e.target.value || null })}>
                      <option value="">Любой язык</option>
                      {d.languages.map((l) => (
                        <option key={l.code} value={l.code}>
                          {l.flag} {l.name}
                        </option>
                      ))}
                    </select>
                    <Inline value={c.url} onSave={(v) => dict.patch('channels', c.id, { url: v })} placeholder="ссылка на профиль" className="grow" style={{ minWidth: 150 }} />
                    <ArchiveBtn archived={c.archived} onChange={(v) => dict.patch('channels', c.id, { archived: v })} />
                    <DeleteBtn what="аккаунт" onDelete={() => dict.remove('channels', c.id)} />
                  </div>
                ))}
              <Button size="sm" variant="ghost" icon={<Plus size={14} />} onClick={() => dict.create('channels', { platform_id: p.id, name: 'Новый аккаунт', language_code: d.primaryLang })}>
                Аккаунт
              </Button>
            </div>
          </div>
        </div>
      ))}
    </>
  )
}

// ---- languages ---------------------------------------------------------------

function Languages() {
  const d = useDicts()
  const dict = useDict()
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [flag, setFlag] = useState('')
  return (
    <Card title="Языки" hint="Каждый ролик может иметь языковые версии: своя озвучка, субтитры, финал и публикации. Основной язык создаётся у каждого нового ролика автоматически.">
      {d.languages.map((l, i) => (
        <div key={l.code} className="dict-row" style={{ opacity: l.archived ? 0.55 : 1 }}>
          <Inline value={l.flag} onSave={(v) => dict.patch('languages', l.code, { flag: v })} style={{ width: 52, textAlign: 'center' }} />
          <span className="mono" style={{ width: 44 }}>
            {l.code}
          </span>
          <Inline value={l.name} onSave={(v) => dict.patch('languages', l.code, { name: v })} className="grow" />
          {l.is_primary ? (
            <span className="chip sm green">основной</span>
          ) : (
            <Button size="sm" variant="ghost" onClick={() => dict.patch('languages', l.code, { is_primary: true })}>
              Сделать основным
            </Button>
          )}
          <Move list={d.languages.map((x) => x.code)} index={i} onMove={(ids) => dict.reorder('languages', ids)} />
          <ArchiveBtn archived={l.archived} onChange={(v) => dict.patch('languages', l.code, { archived: v })} />
          <DeleteBtn what="язык" onDelete={() => dict.remove('languages', l.code)} />
        </div>
      ))}
      <form
        className="row wrap"
        style={{ marginTop: 12 }}
        onSubmit={async (e) => {
          e.preventDefault()
          await dict.create('languages', { code: code.trim(), name, flag })
          setCode('')
          setName('')
          setFlag('')
        }}
      >
        <input className="input sm" style={{ width: 60 }} placeholder="🇪🇸" value={flag} onChange={(e) => setFlag(e.target.value)} />
        <input className="input sm" style={{ width: 80 }} placeholder="es" value={code} onChange={(e) => setCode(e.target.value)} />
        <input className="input sm grow" placeholder="Español" value={name} onChange={(e) => setName(e.target.value)} />
        <Button size="sm" type="submit" icon={<Plus size={14} />} disabled={!code || !name}>
          Добавить
        </Button>
      </form>
    </Card>
  )
}

// ---- stages ------------------------------------------------------------------

function Stages() {
  const d = useDicts()
  const dict = useDict()
  return (
    <Card
      title="Этапы производства"
      hint="Колонки доски и статусы роликов. Тип этапа нужен для аналитики: «готов» — ролик идёт в резерв, «опубликован» — сюда ролик переносится автоматически после первой публикации."
      actions={<Button size="sm" icon={<Plus size={14} />} onClick={() => dict.create('stages', { name: 'Новый этап', color: '#71717a', kind: 'work' })}>Этап</Button>}
    >
      {d.stages.map((s, i) => (
        <div key={s.id} className="dict-row" style={{ opacity: s.archived ? 0.55 : 1, flexWrap: 'wrap' }}>
          <input type="color" className="color-dot" value={s.color} onChange={(e) => dict.patch('stages', s.id, { color: e.target.value })} />
          <Inline value={s.name} onSave={(v) => dict.patch('stages', s.id, { name: v })} className="grow" style={{ minWidth: 140 }} />
          <select className="select sm" style={{ width: 'auto' }} value={s.kind} onChange={(e) => dict.patch('stages', s.id, { kind: e.target.value })}>
            {Object.entries(STAGE_KIND).map(([k, l]) => (
              <option key={k} value={k}>
                {l}
              </option>
            ))}
          </select>
          <Move list={d.stages.map((x) => x.id)} index={i} onMove={(ids) => dict.reorder('stages', ids)} />
          <ArchiveBtn archived={s.archived} onChange={(v) => dict.patch('stages', s.id, { archived: v })} />
          <DeleteBtn what="этап" onDelete={() => dict.remove('stages', s.id)} />
        </div>
      ))}
    </Card>
  )
}

// ---- asset kinds -------------------------------------------------------------

function Kinds() {
  const d = useDicts()
  const dict = useDict()
  return (
    <Card
      title="Типы файлов"
      hint="Слоты материалов в карточке ролика. «Обязательный» — входит в минимальный архив (п.7.2: финал, чистый голос, дорожка без голоса). «Языковой» — свой файл для каждого языка, «общий» — один на все языки."
      actions={<Button size="sm" icon={<Plus size={14} />} onClick={() => dict.create('kinds', { name: 'Новый тип', scope: 'shared' })}>Тип</Button>}
    >
      {d.kinds.map((k, i) => (
        <div key={k.key} className="dict-row" style={{ flexWrap: 'wrap', opacity: k.archived ? 0.55 : 1 }}>
          <Inline value={k.name} onSave={(v) => dict.patch('kinds', k.key, { name: v })} style={{ width: 190, fontWeight: 600 }} />
          <Inline value={k.hint} onSave={(v) => dict.patch('kinds', k.key, { hint: v })} placeholder="подсказка" className="grow" style={{ minWidth: 140 }} />
          <select className="select sm" style={{ width: 'auto' }} value={k.scope} onChange={(e) => dict.patch('kinds', k.key, { scope: e.target.value })}>
            <option value="variant">Языковой</option>
            <option value="shared">Общий</option>
          </select>
          <span className="row small text-2" style={{ gap: 6 }}>
            <Toggle on={k.required} onChange={(v) => dict.patch('kinds', k.key, { required: v })} label="Обязательный" /> обяз.
          </span>
          <Inline value={k.accept} onSave={(v) => dict.patch('kinds', k.key, { accept: v })} placeholder="video/*, .srt" style={{ width: 120 }} />
          <Move list={d.kinds.map((x) => x.key)} index={i} onMove={(ids) => dict.reorder('kinds', ids)} />
          <ArchiveBtn archived={k.archived} onChange={(v) => dict.patch('kinds', k.key, { archived: v })} />
          <DeleteBtn what="тип файла" onDelete={() => dict.remove('kinds', k.key)} />
        </div>
      ))}
    </Card>
  )
}

// ---- checklist template --------------------------------------------------------

function ChecklistTpl() {
  const d = useDicts()
  const dict = useDict()
  const [label, setLabel] = useState('')
  return (
    <Card title="Шаблон чек-листа" hint="Эти пункты добавляются в каждый новый ролик. В самом ролике список можно дополнять.">
      {d.checklist.map((c, i) => (
        <div key={c.id} className="dict-row">
          <Inline value={c.label} onSave={(v) => dict.patch('checklist', c.id, { label: v })} className="grow" />
          <Move list={d.checklist.map((x) => x.id)} index={i} onMove={(ids) => dict.reorder('checklist', ids)} />
          <DeleteBtn what="пункт" onDelete={() => dict.remove('checklist', c.id)} />
        </div>
      ))}
      <form
        className="row"
        style={{ marginTop: 10 }}
        onSubmit={async (e) => {
          e.preventDefault()
          if (!label.trim()) return
          await dict.create('checklist', { label })
          setLabel('')
        }}
      >
        <input className="input sm" placeholder="Новый пункт" value={label} onChange={(e) => setLabel(e.target.value)} />
        <Button size="sm" type="submit" icon={<Plus size={14} />}>
          Добавить
        </Button>
      </form>
    </Card>
  )
}

// ---- tags ----------------------------------------------------------------------

function Tags() {
  return (
    <>
      <TagScopeCard scope="video" title="Теги роликов" hint="Рубрики, форматы и всё, по чему удобно фильтровать ролики." />
      <TagScopeCard scope="music" title="Теги музыки" hint="Жанр, настроение, темп, назначение — фильтры в библиотеке музыки." />
    </>
  )
}

function TagScopeCard({ scope, title, hint }: { scope: TagScope; title: string; hint: string }) {
  const d = useDicts()
  const dict = useDict()
  const groups = d.groups(scope)
  return (
    <Card title={title} hint={hint} actions={<Button size="sm" icon={<Plus size={14} />} onClick={() => dict.create('tag_groups', { scope, name: 'Новая группа' })}>Группа</Button>}>
      {groups.map(({ group, tags }) => (
        <div key={group?.id ?? 0} style={{ padding: '10px 0', borderTop: '1px solid var(--border)' }}>
          <div className="row" style={{ marginBottom: 8 }}>
            {group ? <Inline value={group.name} onSave={(v) => dict.patch('tag_groups', group.id, { name: v })} style={{ maxWidth: 220, fontWeight: 600 }} /> : <b className="small">Без группы</b>}
            <span className="grow" />
            {group && <DeleteBtn what="группу (теги останутся без группы)" onDelete={() => dict.remove('tag_groups', group.id)} />}
          </div>
          <div className="chips">
            {tags.map((t) => (
              <span key={t.id} className="chip" style={{ paddingRight: 3 }}>
                {t.name}
                <button className="icon-btn sm" style={{ width: 18, height: 18 }} aria-label="Удалить тег" onClick={() => dict.remove('tags', t.id)}>
                  ×
                </button>
              </span>
            ))}
            <AddTag onAdd={(name) => dict.create('tags', { scope, name, group_id: group?.id ?? null })} />
          </div>
        </div>
      ))}
    </Card>
  )
}

function AddTag({ onAdd }: { onAdd: (name: string) => Promise<unknown> }) {
  const [v, setV] = useState('')
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault()
        if (!v.trim()) return
        await onAdd(v.trim())
        setV('')
      }}
    >
      <input className="input sm" style={{ width: 130, height: 24, borderRadius: 99, fontSize: 12 }} placeholder="+ тег" value={v} onChange={(e) => setV(e.target.value)} />
    </form>
  )
}

// ---- plan settings ---------------------------------------------------------------

function Plan() {
  const d = useDicts()
  const act = useAction()
  const s = d.settings
  const set = (k: string, v: string) => act('PATCH', '/settings', { [k]: v }, { invalidate: [['bootstrap'], ['dashboard'], ['calendar'], ['videos']], success: 'Сохранено' })
  const ro = !d.isAdmin
  return (
    <Card title="План публикаций и архив" hint={ro ? 'Изменять может только администратор.' : 'Параметры из договора. Метрики на сводке и в календаре считаются по ним.'}>
      <fieldset disabled={ro} style={{ border: 0, padding: 0, margin: 0 }} className="form">
        <div className="grid-2">
          <Field label="Новых роликов в день" hint="п.6.1 — не менее одного">
            <Inline value={s.quota_per_day} onSave={(v) => set('quota_per_day', v)} />
          </Field>
          <Field label="Начало ежедневного плана" hint="Дни до этой даты не учитываются">
            <input className="input sm" type="date" value={s.quota_start} onChange={(e) => set('quota_start', e.target.value)} />
          </Field>
          <Field label="Срок сдачи в архив, часов" hint="п.7.6 — 48 часов после первой публикации">
            <Inline value={s.archive_hours} onSave={(v) => set('archive_hours', v)} />
          </Field>
          <Field label="Окно подсчёта пропусков, дней" hint="п.26.3 — любые 30 дней">
            <Inline value={s.window_days} onSave={(v) => set('window_days', v)} />
          </Field>
          <Field label="Часовой пояс проекта" hint="Границы календарных дней">
            <Inline value={s.timezone} onSave={(v) => set('timezone', v)} />
          </Field>
          <Field label="Префикс кода роликов" hint="LP → LP-0042">
            <Inline value={s.code_prefix} onSave={(v) => set('code_prefix', v)} />
          </Field>
        </div>
        <div className="row">
          <Toggle on={s.auto_done_stage === '1'} onChange={(v) => set('auto_done_stage', v ? '1' : '0')} disabled={ro} label="Автоэтап" />
          <span className="text-2">Переводить ролик на этап «Опубликован» после первой публикации</span>
        </div>
      </fieldset>
    </Card>
  )
}
