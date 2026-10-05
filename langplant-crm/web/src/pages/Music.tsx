import clsx from 'clsx'
import { Check, Download, Ellipsis, Heart, ListChecks, Music as MusicIcon, Pause, Play, Search, Tag as TagIcon, Trash, Upload, X } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router'
import { TagChips, TagPicker } from '../components/pickers'
import { Button, Empty, Field, IconButton, Loading, Menu, MenuItem, MenuSep, Modal, PlatformIcon, Spinner, useConfirm, Waveform } from '../components/ui'
import { derivedUrl, fileUrl } from '../lib/api'
import { fmtBytes, fmtDuration, plural } from '../lib/format'
import { readId3 } from '../lib/media'
import { playTrack, seek, usePlayer, toggle } from '../lib/player'
import { useAction, useDicts, useTrack, useTracks } from '../lib/queries'
import type { ID, Track } from '../lib/types'
import { enqueue } from '../lib/uploads'

export const SOURCES = ['Своя / заказная', 'Epidemic Sound', 'Artlist', 'Musicbed', 'YouTube Audio Library', 'TikTok — библиотека', 'Instagram — библиотека', 'Pixabay', 'Free Music Archive', 'Другое']

type Sort = 'new' | 'title' | 'artist' | 'duration' | 'bpm'

export default function MusicPage() {
  const d = useDicts()
  const { data, isLoading } = useTracks()
  const player = usePlayer()
  const [q, setQ] = useState('')
  const [sort, setSort] = useState<Sort>('new')
  const [sel, setSel] = useState<Set<ID>>(new Set())
  const [selecting, setSelecting] = useState(false)
  const [tagFilter, setTagFilter] = useState<Set<ID>>(new Set())
  const [fav, setFav] = useState(false)
  const [noLicense, setNoLicense] = useState(false)
  const [open, setOpen] = useState<ID | null>(null)
  const [pending, setPending] = useState<File[] | null>(null)
  const [showFilters, setShowFilters] = useState(false)
  const groups = d.groups('music')

  const list = useMemo(() => {
    let xs = data ?? []
    const query = q.trim().toLowerCase()
    if (query) xs = xs.filter((t) => `${t.title} ${t.artist} ${t.album} ${t.notes}`.toLowerCase().includes(query))
    if (fav) xs = xs.filter((t) => t.favorite)
    if (noLicense) xs = xs.filter((t) => !t.license.trim())
    if (tagFilter.size) {
      // AND across groups, OR within a group
      for (const { tags } of groups) {
        const wanted = tags.filter((t) => tagFilter.has(t.id)).map((t) => t.id)
        if (wanted.length) xs = xs.filter((t) => t.tags.some((id) => wanted.includes(id)))
      }
    }
    const by: Record<Sort, (a: Track, b: Track) => number> = {
      new: (a, b) => b.id - a.id,
      title: (a, b) => a.title.localeCompare(b.title, 'ru'),
      artist: (a, b) => a.artist.localeCompare(b.artist, 'ru') || a.title.localeCompare(b.title, 'ru'),
      duration: (a, b) => (a.duration_ms ?? 0) - (b.duration_ms ?? 0),
      bpm: (a, b) => (a.bpm ?? 999) - (b.bpm ?? 999),
    }
    return [...xs].sort(by[sort])
  }, [data, q, fav, noLicense, tagFilter, sort, groups])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (e.code === 'Space' && !['INPUT', 'TEXTAREA', 'SELECT', 'BUTTON'].includes(t.tagName) && player.track) {
        e.preventDefault()
        toggle()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [player.track])

  const toggleTag = (id: ID) => {
    const n = new Set(tagFilter)
    if (n.has(id)) n.delete(id)
    else n.add(id)
    setTagFilter(n)
  }
  const pick = (files: FileList | null) => {
    const fs = [...(files ?? [])].filter((f) => f.type.startsWith('audio/') || /\.(mp3|m4a|wav|aac|flac|ogg)$/i.test(f.name))
    if (fs.length) setPending(fs)
  }
  const filterCount = tagFilter.size + (fav ? 1 : 0) + (noLicense ? 1 : 0)

  return (
    <div className="page" onDragOver={(e) => e.preventDefault()} onDrop={(e) => {
      e.preventDefault()
      pick(e.dataTransfer.files)
    }}>
      <div className="page-head">
        <div className="hide-m">
          <h1>Библиотека музыки</h1>
          <div className="sub">{data ? `${data.length} ${plural(data.length, 'трек', 'трека', 'треков')}` : ' '}</div>
        </div>
        <div className="actions">
          <Button variant={selecting ? 'primary' : 'default'} icon={<ListChecks size={15} />} onClick={() => {
            setSelecting(!selecting)
            setSel(new Set())
          }}>
            {selecting ? 'Готово' : 'Выбрать'}
          </Button>
          <label className="btn primary">
            <Upload size={15} /> Загрузить MP3
            <input type="file" accept="audio/*,.mp3" multiple hidden onChange={(e) => {
              pick(e.target.files)
              e.target.value = ''
            }} />
          </label>
        </div>
      </div>

      <div className="toolbar">
        <div className="input-icon search">
          <Search size={15} />
          <input className="input" placeholder="Название, исполнитель, заметки" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <select className="select sm" style={{ width: 'auto' }} value={sort} onChange={(e) => setSort(e.target.value as Sort)}>
          <option value="new">Сначала новые</option>
          <option value="title">По названию</option>
          <option value="artist">По исполнителю</option>
          <option value="duration">По длительности</option>
          <option value="bpm">По темпу (BPM)</option>
        </select>
        <button className={clsx('chip', fav && 'on')} onClick={() => setFav(!fav)}>
          <Heart size={12} /> Избранное
        </button>
        <button className={clsx('chip', noLicense && 'on')} onClick={() => setNoLicense(!noLicense)}>
          Без лицензии
        </button>
        <button className={clsx('chip', showFilters && 'on')} onClick={() => setShowFilters(!showFilters)}>
          <TagIcon size={12} /> Теги{tagFilter.size ? ` · ${tagFilter.size}` : ''}
        </button>
        {filterCount > 0 && (
          <Button size="sm" variant="ghost" icon={<X size={14} />} onClick={() => {
            setTagFilter(new Set())
            setFav(false)
            setNoLicense(false)
          }}>
            Сбросить
          </Button>
        )}
      </div>
      {showFilters && (
        <div className="card card-pad" style={{ marginBottom: 14, display: 'flex', flexDirection: 'column', gap: 10 }}>
          {groups.map(({ group, tags }) => (
            <div key={group?.id ?? 0} className="row" style={{ alignItems: 'flex-start' }}>
              <span className="small muted" style={{ width: 110, flex: 'none', paddingTop: 3 }}>
                {group?.name ?? 'Другое'}
              </span>
              <div className="chips">
                {tags.map((t) => (
                  <button key={t.id} className={clsx('chip', tagFilter.has(t.id) && 'on')} onClick={() => toggleTag(t.id)}>
                    {t.name}
                    <span className="muted" style={{ fontWeight: 500 }}>
                      {(data ?? []).filter((x) => x.tags.includes(t.id)).length || ''}
                    </span>
                  </button>
                ))}
                {tags.length === 0 && <span className="small muted">нет тегов</span>}
              </div>
            </div>
          ))}
          <div className="tiny muted">Внутри группы — любой из выбранных тегов, между группами — все условия сразу. Теги и группы настраиваются в настройках.</div>
        </div>
      )}

      {selecting && sel.size > 0 && <BulkBar ids={[...sel]} onDone={() => setSel(new Set())} />}

      {isLoading ? (
        <Loading />
      ) : !data?.length ? (
        <Empty icon={<MusicIcon size={22} />} title="Библиотека пуста" action={<label className="btn primary"><Upload size={15} /> Загрузить MP3<input type="file" accept="audio/*,.mp3" multiple hidden onChange={(e) => pick(e.target.files)} /></label>}>
          Перетащите сюда MP3 — название, исполнитель и обложка подтянутся из тегов файла
        </Empty>
      ) : list.length === 0 ? (
        <Empty icon={<Search size={22} />} title="Ничего не найдено" />
      ) : (
        <div className="card" style={{ padding: 6 }}>
          <div className="tracks">
            {list.map((t) => (
              <TrackRow
                key={t.id}
                t={t}
                queue={list}
                selecting={selecting}
                selected={sel.has(t.id)}
                onSelect={() => {
                  const n = new Set(sel)
                  if (n.has(t.id)) n.delete(t.id)
                  else n.add(t.id)
                  setSel(n)
                }}
                onOpen={() => setOpen(t.id)}
              />
            ))}
          </div>
        </div>
      )}
      {open && <TrackModal id={open} onClose={() => setOpen(null)} />}
      {pending && <UploadTracksModal files={pending} onClose={() => setPending(null)} />}
    </div>
  )
}

function TrackRow({ t, queue, selecting, selected, onSelect, onOpen }: { t: Track; queue: Track[]; selecting: boolean; selected: boolean; onSelect: () => void; onOpen: () => void }) {
  const p = usePlayer()
  const act = useAction()
  const isCur = p.track?.id === t.id
  const playing = isCur && p.playing
  return (
    <div className={clsx('track', isCur && 'playing', selected && 'selected')} onClick={selecting ? onSelect : undefined}>
      {selecting ? (
        <input type="checkbox" className="check" checked={selected} onChange={onSelect} onClick={(e) => e.stopPropagation()} style={{ margin: '0 auto' }} />
      ) : (
        <button className="playbtn" onClick={() => playTrack(t, queue)} aria-label={playing ? 'Пауза' : 'Играть'}>
          {isCur && p.loading ? <Spinner size={14} /> : playing ? <Pause size={15} /> : <Play size={15} style={{ marginLeft: 2 }} />}
        </button>
      )}
      <div className="tcover hide-m">{t.has_cover ? <img src={derivedUrl(t.sha256, 'thumb.jpg')} alt="" loading="lazy" /> : <MusicIcon size={16} />}</div>
      <div style={{ minWidth: 0, cursor: 'pointer' }} onClick={selecting ? undefined : onOpen}>
        <div className="ttl ellipsis">{t.title}</div>
        <div className="art ellipsis">
          {t.artist || 'Неизвестный исполнитель'}
          <span className="hide-d">
            {' · '}
            {fmtDuration(t.duration_ms)}
            {t.bpm ? ` · ${t.bpm} BPM` : ''}
          </span>
        </div>
      </div>
      <div className="hide-m" style={{ minWidth: 0, overflow: 'hidden' }}>
        <TagChips ids={t.tags} max={3} />
        {!t.license && <span className="chip sm amber" style={{ marginTop: t.tags.length ? 3 : 0 }}>нет лицензии</span>}
      </div>
      <div className="hide-m">
        <Waveform peaks={t.peaks} progress={isCur && p.duration ? p.time / p.duration : 0} onSeek={(f) => (isCur ? seek(f) : playTrack(t, queue))} bars={54} height={26} />
      </div>
      <div className="small muted nums hide-m">{fmtDuration(t.duration_ms)}</div>
      <div className="small muted nums hide-m">{t.bpm ?? ''}</div>
      <div className="row" style={{ gap: 0 }}>
        <IconButton label={t.favorite ? 'Убрать из избранного' : 'В избранное'} size="sm" onClick={() => act(t.favorite ? 'DELETE' : 'PUT', `/music/${t.id}/favorite`, undefined, { invalidate: [['music']] })}>
          <Heart size={15} fill={t.favorite ? 'var(--red)' : 'none'} color={t.favorite ? 'var(--red)' : undefined} />
        </IconButton>
        <span className="hide-d">
          <IconButton label="Подробнее" size="sm" onClick={onOpen}>
            <Ellipsis size={16} />
          </IconButton>
        </span>
      </div>
    </div>
  )
}

function BulkBar({ ids, onDone }: { ids: ID[]; onDone: () => void }) {
  const act = useAction()
  const confirm = useConfirm()
  const d = useDicts()
  const [add, setAdd] = useState<ID[]>([])
  const [license, setLicense] = useState<{ source: string; license: string } | null>(null)
  const run = (body: Record<string, unknown>, msg: string) => act('POST', '/music/bulk', { ids, ...body }, { invalidate: [['music']], success: msg }).then(onDone)
  return (
    <div className="card card-pad row wrap" style={{ marginBottom: 12, position: 'sticky', top: 'calc(var(--topbar) * 0 + 8px)', zIndex: 5 }}>
      <b>
        Выбрано: {ids.length}
      </b>
      <span className="grow" />
      <TagPicker
        scope="music"
        value={add}
        onChange={setAdd}
        trigger={<Button size="sm" icon={<TagIcon size={14} />}>Теги…</Button>}
      />
      {add.length > 0 && (
        <>
          <Button size="sm" variant="primary" onClick={() => run({ add_tags: add }, 'Теги добавлены').then(() => setAdd([]))}>
            Добавить {add.map((t) => d.tagById.get(t)?.name).join(', ')}
          </Button>
          <Button size="sm" onClick={() => run({ remove_tags: add }, 'Теги убраны').then(() => setAdd([]))}>
            Убрать
          </Button>
        </>
      )}
      <Button size="sm" onClick={() => setLicense({ source: '', license: '' })}>
        Источник и лицензия…
      </Button>
      <Button
        size="sm"
        variant="danger"
        icon={<Trash size={14} />}
        onClick={async () => {
          if (await confirm({ title: `Удалить ${ids.length} ${plural(ids.length, 'трек', 'трека', 'треков')}?`, text: 'Треки попадут в корзину.', confirm: 'В корзину', danger: true })) run({ delete: true }, 'Треки перемещены в корзину')
        }}
      >
        Удалить
      </Button>
      {license && (
        <Modal
          open
          onClose={() => setLicense(null)}
          title="Источник и лицензия"
          footer={
            <>
              <Button onClick={() => setLicense(null)}>Отмена</Button>
              <Button variant="primary" onClick={() => run({ set: license }, 'Сохранено').then(() => setLicense(null))}>
                Применить к {ids.length}
              </Button>
            </>
          }
        >
          <div className="form">
            <Field label="Источник">
              <input className="input" list="music-sources" value={license.source} onChange={(e) => setLicense({ ...license, source: e.target.value })} />
            </Field>
            <Field label="Лицензия / условия">
              <textarea className="textarea" value={license.license} onChange={(e) => setLicense({ ...license, license: e.target.value })} />
            </Field>
          </div>
          <SourcesList />
        </Modal>
      )}
    </div>
  )
}

const SourcesList = () => (
  <datalist id="music-sources">
    {SOURCES.map((s) => (
      <option key={s} value={s} />
    ))}
  </datalist>
)

function TrackModal({ id, onClose }: { id: ID; onClose: () => void }) {
  const d = useDicts()
  const { data } = useTrack(id)
  const act = useAction()
  const confirm = useConfirm()
  const [form, setForm] = useState<Partial<Track> | null>(null)
  useEffect(() => {
    if (data && !form) setForm(data.track)
  }, [data, form])
  const t = data?.track
  if (!t || !form) {
    return (
      <Modal open onClose={onClose} title="Трек">
        <Loading />
      </Modal>
    )
  }
  const f = (k: keyof Track) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value })
  const platforms = (form.platforms ?? []) as ID[]
  const save = async () => {
    await act(
      'PATCH',
      `/music/${t.id}`,
      {
        title: form.title,
        artist: form.artist,
        album: form.album,
        bpm: form.bpm ? Number(form.bpm) : null,
        musical_key: form.musical_key,
        source: form.source,
        license: form.license,
        license_url: form.license_url,
        notes: form.notes,
        platforms,
        tags: form.tags,
      },
      { invalidate: [['music'], ['track', t.id]], success: 'Сохранено' },
    )
    onClose()
  }
  const dlName = `${t.artist ? t.artist + ' - ' : ''}${t.title}.${t.filename.split('.').pop()}`
  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={t.title}
      footer={
        <>
          <Menu
            align="start"
            trigger={
              <Button style={{ marginRight: 'auto' }} icon={<Ellipsis size={15} />}>
                Ещё
              </Button>
            }
          >
            <MenuItem icon={<Download size={15} />} onSelect={() => (window.location.href = fileUrl(t.sha256, { download: true, name: dlName }))}>
              Скачать
            </MenuItem>
            <MenuSep />
            <MenuItem
              icon={<Trash size={15} />}
              danger
              onSelect={async () => {
                if (await confirm({ title: 'Удалить трек?', text: 'Трек попадёт в корзину.', confirm: 'В корзину', danger: true })) {
                  await act('DELETE', `/music/${t.id}`, undefined, { invalidate: [['music']] })
                  onClose()
                }
              }}
            >
              Удалить
            </MenuItem>
          </Menu>
          <Button onClick={onClose}>Отмена</Button>
          <Button variant="primary" onClick={save}>
            Сохранить
          </Button>
        </>
      }
    >
      <div className="form">
        <div className="row" style={{ gap: 14 }}>
          <div className="tcover" style={{ width: 64, height: 64, borderRadius: 12 }}>
            {t.has_cover ? <img src={derivedUrl(t.sha256, 'thumb.jpg')} alt="" /> : <MusicIcon size={22} />}
          </div>
          <div className="grow" style={{ minWidth: 0 }}>
            <Waveform peaks={t.peaks} bars={120} height={40} onSeek={() => playTrack(t)} />
            <div className="tiny muted" style={{ marginTop: 4 }}>
              {fmtDuration(t.duration_ms)} · {fmtBytes(t.size)} · {t.filename}
            </div>
          </div>
          <button className="bigplay btn primary" style={{ width: 44, height: 44, borderRadius: '50%', padding: 0 }} onClick={() => playTrack(t)} aria-label="Играть">
            <Play size={18} />
          </button>
        </div>
        <div className="grid-2">
          <Field label="Название">
            <input className="input" value={form.title ?? ''} onChange={f('title')} />
          </Field>
          <Field label="Исполнитель">
            <input className="input" value={form.artist ?? ''} onChange={f('artist')} />
          </Field>
          <Field label="Альбом">
            <input className="input" value={form.album ?? ''} onChange={f('album')} />
          </Field>
          <div className="grid-2" style={{ gap: 10 }}>
            <Field label="BPM">
              <input className="input" inputMode="numeric" value={form.bpm ?? ''} onChange={(e) => setForm({ ...form, bpm: e.target.value ? Number(e.target.value.replace(/\D/g, '')) : null })} />
            </Field>
            <Field label="Тональность">
              <input className="input" value={form.musical_key ?? ''} onChange={f('musical_key')} placeholder="Am" />
            </Field>
          </div>
        </div>
        <Field label="Теги">
          <div className="row wrap">
            <TagChips ids={(form.tags ?? []) as ID[]} />
            <TagPicker scope="music" value={(form.tags ?? []) as ID[]} onChange={(tags) => setForm({ ...form, tags })} trigger={<button className="btn sm">Изменить теги</button>} />
          </div>
        </Field>
        <div className="grid-2">
          <Field label="Источник">
            <input className="input" list="music-sources" value={form.source ?? ''} onChange={f('source')} placeholder="Откуда трек" />
          </Field>
          <Field label="Ссылка на лицензию">
            <input className="input" type="url" value={form.license_url ?? ''} onChange={f('license_url')} placeholder="https://" />
          </Field>
        </div>
        <Field label="Лицензия и условия использования" hint="п.14.3: источник музыки и условия использования указываются в архиве">
          <textarea className="textarea" rows={3} value={form.license ?? ''} onChange={f('license')} placeholder="Например: подписка Epidemic до 12.2027, коммерческое использование разрешено" />
        </Field>
        <Field label="Где можно использовать" hint="п.14.4: музыка из библиотеки одной соцсети не означает права на других площадках">
          <div className="chips">
            {d.platforms
              .filter((p) => !p.archived)
              .map((p) => {
                const on = platforms.includes(p.id)
                return (
                  <button key={p.id} type="button" className={clsx('chip', on && 'on')} onClick={() => setForm({ ...form, platforms: on ? platforms.filter((x) => x !== p.id) : [...platforms, p.id] })}>
                    <PlatformIcon platform={p} size="sm" /> {p.name}
                    {on && <Check size={12} />}
                  </button>
                )
              })}
          </div>
        </Field>
        <Field label="Заметки">
          <textarea className="textarea" rows={2} value={form.notes ?? ''} onChange={f('notes')} />
        </Field>
        {data.videos.length > 0 && (
          <Field label={`Используется в роликах (${data.videos.length})`}>
            <div className="chips">
              {data.videos.map((v) => (
                <Link key={v.video_id} to={`/videos/${v.video_id}`} className="chip" onClick={onClose}>
                  {v.code} {v.title}
                </Link>
              ))}
            </div>
          </Field>
        )}
      </div>
      <SourcesList />
    </Modal>
  )
}

interface Draft {
  file: File
  title: string
  artist: string
  album: string
  bpm: number | null
  key: string
  cover?: Blob
  genre?: string
  tags: ID[] // matched from the ID3 genre
}

function UploadTracksModal({ files, onClose }: { files: File[]; onClose: () => void }) {
  const d = useDicts()
  const [drafts, setDrafts] = useState<Draft[] | null>(null)
  const [tags, setTags] = useState<ID[]>([])
  const [source, setSource] = useState('')
  const [license, setLicense] = useState('')
  const [platforms, setPlatforms] = useState<ID[]>([])
  useEffect(() => {
    Promise.all(
      files.map(async (file) => {
        const id3 = await readId3(file)
        const base = file.name.replace(/\.[^.]+$/, '')
        const [a, b] = base.includes(' - ') ? base.split(' - ', 2) : ['', base]
        // genre from the tags → matching existing music tag of this track
        const genre = id3.genre?.toLowerCase()
        const matched = genre ? d.tags.filter((t) => t.scope === 'music' && t.name.toLowerCase() === genre).map((t) => t.id) : []
        return { file, title: id3.title || b.trim(), artist: id3.artist || a.trim(), album: id3.album || '', bpm: id3.bpm ?? null, key: id3.key ?? '', cover: id3.cover, genre: id3.genre, tags: matched }
      }),
    ).then(setDrafts)
  }, [files])
  const upd = (i: number, p: Partial<Draft>) => setDrafts((ds) => ds!.map((x, j) => (j === i ? { ...x, ...p } : x)))
  const start = () => {
    for (const x of drafts ?? []) {
      enqueue(
        [x.file],
        { type: 'track', title: x.title, artist: x.artist, album: x.album, bpm: x.bpm, musical_key: x.key, tags: [...new Set([...tags, ...x.tags])], source, license, platforms },
        `Музыка · ${x.artist ? x.artist + ' — ' : ''}${x.title}`,
        () => ({ cover: x.cover }),
      )
    }
    onClose()
  }
  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={`Загрузка в библиотеку: ${files.length}`}
      footer={
        <>
          <Button onClick={onClose}>Отмена</Button>
          <Button variant="primary" icon={<Upload size={15} />} onClick={start} disabled={!drafts}>
            Загрузить
          </Button>
        </>
      }
    >
      {!drafts ? (
        <Loading />
      ) : (
        <div className="form">
          <div className="list">
            {drafts.map((x, i) => (
              <div key={i} className="list-row" style={{ flexWrap: 'wrap' }}>
                <div className="tcover" style={{ width: 38, height: 38 }}>
                  {x.cover ? <img src={URL.createObjectURL(x.cover)} alt="" /> : <MusicIcon size={15} />}
                </div>
                <input className="input sm grow" style={{ minWidth: 140 }} value={x.title} onChange={(e) => upd(i, { title: e.target.value })} placeholder="Название" />
                <input className="input sm grow" style={{ minWidth: 120 }} value={x.artist} onChange={(e) => upd(i, { artist: e.target.value })} placeholder="Исполнитель" />
                {x.tags.length > 0 && <TagChips ids={x.tags} />}
                {x.genre && !x.tags.length && <span className="chip sm outline" title="Жанр из тегов файла — такого тега в библиотеке нет">{x.genre}</span>}
                <span className="tiny muted nums" style={{ width: 60, textAlign: 'right' }}>
                  {fmtBytes(x.file.size)}
                </span>
              </div>
            ))}
          </div>
          <Field label="Теги для всех">
            <div className="row wrap">
              <TagChips ids={tags} />
              <TagPicker scope="music" value={tags} onChange={setTags} trigger={<button className="btn sm">+ Теги</button>} />
            </div>
          </Field>
          <div className="grid-2">
            <Field label="Источник">
              <input className="input" list="music-sources" value={source} onChange={(e) => setSource(e.target.value)} />
            </Field>
            <Field label="Лицензия">
              <input className="input" value={license} onChange={(e) => setLicense(e.target.value)} placeholder="Условия использования" />
            </Field>
          </div>
          <Field label="Где можно использовать">
            <div className="chips">
              {d.platforms
                .filter((p) => !p.archived)
                .map((p) => {
                  const on = platforms.includes(p.id)
                  return (
                    <button key={p.id} type="button" className={clsx('chip', on && 'on')} onClick={() => setPlatforms(on ? platforms.filter((x) => x !== p.id) : [...platforms, p.id])}>
                      <PlatformIcon platform={p} size="sm" /> {p.name}
                    </button>
                  )
                })}
            </div>
          </Field>
          <SourcesList />
        </div>
      )}
    </Modal>
  )
}

