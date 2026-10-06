import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router'
import { StagePicker, TagGroupsEditor } from '../components/pickers'
import { Button, Field, Modal, StagePill } from '../components/ui'
import { useAction, useDicts } from '../lib/queries'
import type { ID, Video } from '../lib/types'

export function NewVideoModal({ open, onClose, planDate }: { open: boolean; onClose: () => void; planDate?: string }) {
  const d = useDicts()
  const act = useAction()
  const nav = useNavigate()
  const [title, setTitle] = useState('')
  const [stage, setStage] = useState<ID | null>(null)
  const [date, setDate] = useState('')
  const [tags, setTags] = useState<ID[]>([])
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (open) {
      setTitle('')
      setStage(d.activeStages[0]?.id ?? null)
      setDate(planDate ?? '')
      setTags([])
    }
  }, [open, planDate, d.activeStages])

  const submit = async (e?: React.FormEvent) => {
    e?.preventDefault()
    if (!title.trim()) return
    setBusy(true)
    try {
      const v = await act<Video>('POST', '/videos', { title, stage_id: stage, plan_date: date || null, tags, assignee_id: null }, { invalidate: [['videos'], ['dashboard'], ['calendar']] })
      onClose()
      if (v) nav(`/videos/${v.id}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Новый ролик"
      footer={
        <>
          <Button onClick={onClose}>Отмена</Button>
          <Button variant="primary" onClick={() => submit()} loading={busy} disabled={!title.trim()}>
            Создать
          </Button>
        </>
      }
    >
      <form className="form" onSubmit={submit}>
        <Field label="Название" hint="Код ролика (LP-0001…) присвоится автоматически">
          <input className="input" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Например: 5 фраз для заказа кофе" autoFocus />
        </Field>
        <div className="grid-2">
          <Field label="Этап">
            <StagePicker value={stage} onChange={setStage} trigger={<button type="button" className="btn" style={{ justifyContent: 'flex-start' }}><StagePill stage={stage ? d.stageById.get(stage) : null} /></button>} />
          </Field>
          <Field label="Дата публикации" hint="Можно оставить пустой">
            <input className="input" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
          </Field>
        </div>
        <Field label="Теги">
          <TagGroupsEditor scope="video" value={tags} onChange={setTags} />
        </Field>
        <button type="submit" hidden />
      </form>
    </Modal>
  )
}
