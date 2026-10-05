import clsx from 'clsx'
import {
  Cloud,
  CircleCheck,
  Download,
  Ellipsis,
  File as FileIcon,
  FileImage,
  FileMusic,
  FilePlay,
  FileText,
  Captions,
  Folder,
  HardDrive,
  Pencil,
  Play,
  Trash,
  TriangleAlert,
  Upload,
} from 'lucide-react'
import { useMemo, useRef, useState, type DragEvent } from 'react'
import { Avatar, Button, IconButton, Menu, MenuItem, MenuLabel, MenuSep, Modal, Seg, useConfirm } from '../../components/ui'
import { derivedUrl, fileUrl } from '../../lib/api'
import { fmtAgo, fmtBytes, fmtDuration } from '../../lib/format'
import { guessKind, matchesAccept } from '../../lib/labels'
import { useAction, useDicts } from '../../lib/queries'
import type { Asset, AssetKind, ID, VariantSummary, VideoDetail } from '../../lib/types'
import { enqueue } from '../../lib/uploads'

export function kindIcon(kind: string, mime: string, size = 16) {
  if (kind === 'subs') return <Captions size={size} />
  if (kind === 'project' || kind === 'raw') return <Folder size={size} />
  if (mime.startsWith('video/')) return <FilePlay size={size} />
  if (mime.startsWith('audio/')) return <FileMusic size={size} />
  if (mime.startsWith('image/')) return <FileImage size={size} />
  if (mime.startsWith('text/') || kind === 'script') return <FileText size={size} />
  return <FileIcon size={size} />
}

/** File slots of one scope: a language version (variant) or the shared files. */
export function Materials({ video, variant, onFiles }: { video: VideoDetail; variant: VariantSummary | null; onFiles: (files: File[], kind?: string) => void }) {
  const d = useDicts()
  const scope = variant ? 'variant' : 'shared'
  const assets = video.assets.filter((a) => (variant ? a.variant_id === variant.id : a.variant_id === null))
  const byKind = useMemo(() => {
    const m = new Map<string, Asset[]>()
    for (const a of assets) m.set(a.kind, [...(m.get(a.kind) ?? []), a])
    return m
  }, [assets])
  const sharedKinds = new Set(video.assets.filter((a) => a.variant_id === null).map((a) => a.kind))
  const kinds = d.kinds.filter((k) => (k.scope === scope && !k.archived) || byKind.has(k.key))

  return (
    <div className="slots">
      {kinds.map((k) => (
        <Slot
          key={k.key}
          kind={k}
          files={byKind.get(k.key) ?? []}
          coveredByShared={!!variant && k.required && sharedKinds.has(k.key)}
          video={video}
          onFiles={(files) => onFiles(files, k.key)}
        />
      ))}
    </div>
  )
}

function Slot({ kind, files, coveredByShared, video, onFiles }: { kind: AssetKind; files: Asset[]; coveredByShared: boolean; video: VideoDetail; onFiles: (f: File[]) => void }) {
  const input = useRef<HTMLInputElement>(null)
  const [over, setOver] = useState(false)
  const [showOld, setShowOld] = useState(false)
  const single = ['final', 'voice', 'bed', 'clean', 'cover', 'subs'].includes(kind.key)
  const visible = single && !showOld ? files.slice(0, 1) : files
  const hidden = files.length - visible.length
  const ok = files.length > 0 || coveredByShared
  const drop = (e: DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setOver(false)
    const fs = [...e.dataTransfer.files]
    if (fs.length) onFiles(fs)
  }
  return (
    <div
      className={clsx('slot', over && 'drop')}
      onDragOver={(e) => {
        e.preventDefault()
        e.stopPropagation()
        setOver(true)
      }}
      onDragLeave={() => setOver(false)}
      onDrop={drop}
    >
      <div className="kind">
        <div>
          <div className="name">
            {kind.name}
            {kind.required && <span className={clsx('req', ok && 'ok')}>{ok ? '✓' : 'обязательно'}</span>}
          </div>
          {kind.hint && <div className="hint">{kind.hint}</div>}
          {coveredByShared && files.length === 0 && <div className="hint">есть в общих файлах</div>}
        </div>
      </div>
      <div className="files">
        {visible.map((a) => (
          <FileRow key={a.id} asset={a} video={video} old={single && a !== files[0]} />
        ))}
        {hidden > 0 && (
          <button className="btn ghost sm" style={{ alignSelf: 'flex-start' }} onClick={() => setShowOld(true)}>
            Предыдущие версии: {hidden}
          </button>
        )}
        {files.length === 0 && <div className="small muted" style={{ paddingTop: 6 }}>Перетащите файл сюда или нажмите «Загрузить»</div>}
      </div>
      <div>
        <Button size="sm" icon={<Upload size={14} />} onClick={() => input.current?.click()}>
          <span className="hide-m">{files.length && single ? 'Новая версия' : 'Загрузить'}</span>
        </Button>
        <input
          ref={input}
          type="file"
          hidden
          multiple={!single}
          accept={kind.accept || undefined}
          onChange={(e) => {
            const fs = [...(e.target.files ?? [])]
            e.target.value = ''
            if (fs.length) onFiles(fs)
          }}
        />
      </div>
    </div>
  )
}

function SyncBadge({ asset }: { asset: Asset }) {
  const b = asset.blob
  if (!b) return null
  if (b.state === 'stored')
    return (
      <span className="sync stored" title="Файл сохранён на компе-хранилище">
        <HardDrive size={11} /> на компе
      </span>
    )
  if (b.state === 'missing')
    return (
      <span className="sync missing" title="Хранилище не нашло файл — загрузите заново">
        <TriangleAlert size={11} /> потерян
      </span>
    )
  return (
    <span className="sync buffered" title={b.sync_error || 'Файл на сервере, ждёт переноса на комп'}>
      <Cloud size={11} /> на сервере
    </span>
  )
}

function FileRow({ asset, video, old }: { asset: Asset; video: VideoDetail; old?: boolean }) {
  const d = useDicts()
  const act = useAction()
  const confirm = useConfirm()
  const [preview, setPreview] = useState(false)
  const [rename, setRename] = useState<string | null>(null)
  const hasThumb = asset.blob?.derived.includes('thumb.jpg')
  const playable = asset.mime.startsWith('video/') || asset.mime.startsWith('audio/') || asset.mime.startsWith('image/')
  const u = asset.uploaded_by ? d.userById.get(asset.uploaded_by) : undefined
  const inv = [['video', video.id], ['videos']]
  const dlName = `${video.code}_${asset.filename}`
  const move = (variantId: ID | null) => act('PATCH', `/assets/${asset.id}`, { variant_id: variantId }, { invalidate: inv })
  return (
    <div className={clsx('file', old && 'old')}>
      <button className="ficon" style={{ border: 0, cursor: playable ? 'pointer' : 'default', padding: 0 }} onClick={() => playable && setPreview(true)} aria-label="Просмотр">
        {hasThumb ? <img src={derivedUrl(asset.sha256, 'thumb.jpg')} alt="" /> : kindIcon(asset.kind, asset.mime)}
        {playable && !asset.mime.startsWith('image/') && hasThumb && (
          <span style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', color: 'white', background: 'rgba(0,0,0,.25)' }}>
            <Play size={13} fill="white" />
          </span>
        )}
      </button>
      <div className="grow" style={{ minWidth: 0 }}>
        <div className="fname ellipsis" title={asset.filename}>
          {asset.filename}
        </div>
        <div className="fmeta">
          {asset.version > 1 && <span className="chip sm">v{asset.version}</span>}
          <span>{fmtBytes(asset.size)}</span>
          {asset.blob?.duration_ms ? <span>{fmtDuration(asset.blob.duration_ms)}</span> : null}
          {asset.blob?.width ? (
            <span>
              {asset.blob.width}×{asset.blob.height}
            </span>
          ) : null}
          <SyncBadge asset={asset} />
          <span className="row" style={{ gap: 4 }}>
            <Avatar user={u} size="sm" /> {fmtAgo(asset.created_at)}
          </span>
        </div>
        {asset.note && <div className="tiny text-2">{asset.note}</div>}
      </div>
      <a className="icon-btn sm" href={fileUrl(asset.sha256, { download: true, name: dlName })} download={dlName} aria-label="Скачать" title="Скачать">
        <Download size={15} />
      </a>
      <Menu
        trigger={
          <IconButton label="Действия" size="sm">
            <Ellipsis size={16} />
          </IconButton>
        }
      >
        {playable && (
          <MenuItem icon={<Play size={15} />} onSelect={() => setPreview(true)}>
            Просмотр
          </MenuItem>
        )}
        <MenuItem icon={<Pencil size={15} />} onSelect={() => setRename(asset.filename)}>
          Переименовать / заметка
        </MenuItem>
        <MenuSep />
        <MenuLabel>Тип файла</MenuLabel>
        {d.activeKinds.map((k) => (
          <MenuItem key={k.key} onSelect={() => act('PATCH', `/assets/${asset.id}`, { kind: k.key }, { invalidate: inv })} right={k.key === asset.kind ? '✓' : undefined}>
            {k.name}
          </MenuItem>
        ))}
        <MenuSep />
        <MenuLabel>Переместить</MenuLabel>
        <MenuItem onSelect={() => move(null)} right={asset.variant_id === null ? '✓' : undefined}>
          Общие файлы
        </MenuItem>
        {video.variants.map((v) => (
          <MenuItem key={v.id} onSelect={() => move(v.id)} right={asset.variant_id === v.id ? '✓' : undefined}>
            {d.langByCode.get(v.lang)?.flag} {d.langByCode.get(v.lang)?.name ?? v.lang}
          </MenuItem>
        ))}
        <MenuSep />
        <MenuItem
          icon={<Trash size={15} />}
          danger
          onSelect={async () => {
            if (await confirm({ title: 'Удалить файл?', text: `«${asset.filename}» попадёт в корзину. Восстановить можно в разделе «Корзина».`, confirm: 'В корзину', danger: true }))
              act('DELETE', `/assets/${asset.id}`, undefined, { invalidate: inv, success: 'Файл перемещён в корзину' })
          }}
        >
          Удалить
        </MenuItem>
      </Menu>
      {preview && <PreviewModal asset={asset} title={`${video.code} · ${d.kindByKey.get(asset.kind)?.name ?? asset.kind}`} onClose={() => setPreview(false)} />}
      {rename !== null && <RenameModal asset={asset} onClose={() => setRename(null)} inv={inv} />}
    </div>
  )
}

function RenameModal({ asset, onClose, inv }: { asset: Asset; onClose: () => void; inv: unknown[][] }) {
  const act = useAction()
  const [name, setName] = useState(asset.filename)
  const [note, setNote] = useState(asset.note)
  const save = async () => {
    await act('PATCH', `/assets/${asset.id}`, { filename: name, note }, { invalidate: inv })
    onClose()
  }
  return (
    <Modal
      open
      onClose={onClose}
      title="Файл"
      footer={
        <>
          <Button onClick={onClose}>Отмена</Button>
          <Button variant="primary" onClick={save}>
            Сохранить
          </Button>
        </>
      }
    >
      <div className="form">
        <div className="field">
          <label>Имя файла</label>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="field">
          <label>Заметка</label>
          <textarea className="textarea" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Например: версия без мата, 50 fps, громкость −14 LUFS" />
        </div>
      </div>
    </Modal>
  )
}

export function PreviewModal({ asset, title, onClose }: { asset: Pick<Asset, 'sha256' | 'mime' | 'filename' | 'blob'>; title: string; onClose: () => void }) {
  const hasPreview = asset.blob?.derived.includes('preview.mp4')
  const [quality, setQuality] = useState<'preview' | 'original'>(hasPreview ? 'preview' : 'original')
  const isVideo = asset.mime.startsWith('video/')
  const src = isVideo && quality === 'preview' ? derivedUrl(asset.sha256, 'preview.mp4') : fileUrl(asset.sha256, { name: asset.filename })
  const [error, setError] = useState<string | null>(null)
  const onError = async () => {
    // a media element does not expose the HTTP status; ask the server what happened
    let msg = 'Браузер не может воспроизвести этот формат. Скачайте файл или откройте «Превью».'
    try {
      const r = await fetch(src, { method: 'HEAD' })
      if (r.status === 503) msg = 'Комп-хранилище сейчас офлайн, а копии на сервере нет. Файл откроется, когда комп подключится.'
      else if (r.status === 410) msg = 'Файл отсутствует в хранилище — загрузите его заново.'
      else if (!r.ok) msg = `Сервер ответил ошибкой ${r.status}.`
    } catch {
      msg = 'Нет связи с сервером.'
    }
    setError(msg)
  }
  return (
    <Modal open onClose={onClose} title={title} wide>
      <div className="form" style={{ gap: 10 }}>
        {isVideo && (
          <div className="row">
            <span className="small muted grow ellipsis">{asset.filename}</span>
            <Seg
              value={quality}
              onChange={(v) => {
                setError(null)
                setQuality(v)
              }}
              options={[
                { value: 'preview', label: 'Превью' },
                { value: 'original', label: 'Оригинал' },
              ]}
            />
          </div>
        )}
        {error && <div className="banner">{error}</div>}
        {isVideo ? (
          <video
            key={src}
            className="player-video"
            src={src}
            poster={asset.blob?.derived.includes('poster.jpg') ? derivedUrl(asset.sha256, 'poster.jpg') : undefined}
            controls
            playsInline
            autoPlay
            onError={onError}
          />
        ) : asset.mime.startsWith('audio/') ? (
          <audio src={src} controls autoPlay style={{ width: '100%' }} onError={onError} />
        ) : (
          <img src={src} alt="" style={{ maxHeight: '72dvh', margin: '0 auto', borderRadius: 10 }} onError={onError} />
        )}
      </div>
    </Modal>
  )
}

interface Pending {
  file: File
  kind: string
  target: string // 'shared' or variant id
}

/** Dialog that lets the user confirm the kind and language of dropped files. */
export function AssignModal({ video, files, defaultVariant, onClose }: { video: VideoDetail; files: File[]; defaultVariant: ID | null; onClose: () => void }) {
  const d = useDicts()
  const existing = new Set(video.assets.map((a) => a.kind))
  const [rows, setRows] = useState<Pending[]>(() =>
    files.map((file) => {
      const kind = guessKind(file, d.activeKinds, existing)
      const k = d.kindByKey.get(kind)
      return { file, kind, target: k?.scope === 'variant' && defaultVariant ? String(defaultVariant) : 'shared' }
    }),
  )
  const upd = (i: number, p: Partial<Pending>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...p } : r)))
  const start = () => {
    for (const r of rows) {
      const variant = r.target === 'shared' ? null : Number(r.target)
      const lang = variant ? video.variants.find((v) => v.id === variant)?.lang.toUpperCase() : 'общие'
      enqueue([r.file], { type: 'asset', video_id: video.id, variant_id: variant, kind: r.kind }, `${video.code} · ${d.kindByKey.get(r.kind)?.name ?? r.kind} · ${lang}`)
    }
    onClose()
  }
  const total = rows.reduce((s, r) => s + r.file.size, 0)
  return (
    <Modal
      open
      onClose={onClose}
      wide
      title={`Загрузка в ${video.code}`}
      footer={
        <>
          <span className="muted small grow" style={{ alignSelf: 'center' }}>
            {rows.length} файл(ов), {fmtBytes(total)}
          </span>
          <Button onClick={onClose}>Отмена</Button>
          <Button variant="primary" icon={<Upload size={15} />} onClick={start}>
            Загрузить
          </Button>
        </>
      }
    >
      <div className="small muted" style={{ marginBottom: 10 }}>
        Тип и язык определены по имени файла — проверьте и при необходимости поправьте.
      </div>
      <div className="list">
        {rows.map((r, i) => {
          const k = d.kindByKey.get(r.kind)
          const mismatch = k && !matchesAccept(r.file, k.accept)
          return (
            <div key={i} className="list-row" style={{ flexWrap: 'wrap' }}>
              <span className="ficon" style={{ width: 30, height: 30, borderRadius: 8, display: 'grid', placeItems: 'center', background: 'var(--surface-2)' }}>
                {kindIcon(r.kind, r.file.type, 15)}
              </span>
              <div className="grow" style={{ minWidth: 160 }}>
                <div className="ellipsis" style={{ fontWeight: 550 }}>
                  {r.file.name}
                </div>
                <div className="tiny muted">
                  {fmtBytes(r.file.size)}
                  {mismatch && <span style={{ color: 'var(--amber-text)' }}> · необычный формат для этого типа</span>}
                </div>
              </div>
              <select className="select sm" style={{ width: 'auto' }} value={r.kind} onChange={(e) => upd(i, { kind: e.target.value })}>
                {d.activeKinds.map((k) => (
                  <option key={k.key} value={k.key}>
                    {k.name}
                  </option>
                ))}
              </select>
              <select className="select sm" style={{ width: 'auto' }} value={r.target} onChange={(e) => upd(i, { target: e.target.value })}>
                <option value="shared">Общие</option>
                {video.variants.map((v) => (
                  <option key={v.id} value={v.id}>
                    {d.langByCode.get(v.lang)?.flag} {v.lang.toUpperCase()}
                  </option>
                ))}
              </select>
            </div>
          )
        })}
      </div>
    </Modal>
  )
}

export function ArchiveStatus({ variant, compact }: { variant: VariantSummary; compact?: boolean }) {
  const d = useDicts()
  if (!variant.first_published_at) {
    if (!variant.missing.length)
      return (
        <span className="chip green">
          <CircleCheck size={13} /> обязательные файлы на месте
        </span>
      )
    return compact ? null : <span className="chip">для архива нужны: {variant.missing.map((k) => d.kindByKey.get(k)?.name ?? k).join(', ')}</span>
  }
  if (!variant.missing.length)
    return (
      <span className="chip green">
        <CircleCheck size={13} /> архив собран
      </span>
    )
  const left = variant.deadline_at ? variant.deadline_at - Date.now() : 0
  const overdue = left < 0
  const h = Math.abs(Math.round(left / 3600000))
  return (
    <span className={clsx('chip', overdue ? 'red' : 'amber')}>
      <TriangleAlert size={13} />
      не хватает: {variant.missing.map((k) => d.kindByKey.get(k)?.name ?? k).join(', ')} · {overdue ? `просрочено на ${h} ч` : `осталось ${h} ч`}
    </span>
  )
}
