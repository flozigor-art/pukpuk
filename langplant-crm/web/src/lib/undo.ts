import { useEffect } from 'react'
import { toast } from 'sonner'
import { api, ApiError, setUndoStepHandler, type UndoStepRef } from './api'
import { queryClient } from './queries'

interface RevertResult {
  id: number
  kind: 'undo' | 'redo'
  label: string
  target_id: number | null
}

const TOAST_MS = 7000

/** "Отмена: X" / "Повтор: X" → "X" */
export const baseLabel = (label: string) => label.replace(/^((Отмена|Повтор): )+/, '')

export const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)
export const undoKeys = isMac ? '⌘Z' : 'Ctrl+Z'

/** Every change the server recorded gets a toast with an "Отменить" button. */
function announce(step: UndoStepRef, label?: string) {
  toast(label || step.label, {
    id: `undo-${step.id}`, // a merged step (autosave) updates its toast instead of stacking new ones
    duration: TOAST_MS,
    action: { label: 'Отменить', onClick: () => void revert(step.id) },
  })
}
setUndoStepHandler(announce)

let busy = false

function afterRevert(res: RevertResult) {
  queryClient.invalidateQueries()
  const undone = res.kind === 'undo'
  toast.success(`${undone ? 'Отменено' : 'Возвращено'}: ${baseLabel(res.label)}`, {
    id: `undo-${res.id}`,
    duration: TOAST_MS,
    action: { label: undone ? 'Вернуть' : 'Отменить', onClick: () => void revert(res.id) },
  })
}

async function run(path: string, body?: unknown) {
  if (busy) return
  busy = true
  try {
    afterRevert(await api<RevertResult>(path, { method: 'POST', body }))
  } catch (e) {
    if (e instanceof ApiError && e.code === 'nothing') toast(e.message, { duration: 2500 })
    else toast.error((e as Error).message, { duration: 9000 })
    queryClient.invalidateQueries({ queryKey: ['undo'] })
  } finally {
    busy = false
  }
}

/** Undoes a history step (or redoes it, if the step is itself an undo). */
export const revert = (id: number) => run(`/undo/${id}`)

/** Undoes the user's own latest change, or brings back the latest undo. */
export const revertLast = (redo = false) => run('/undo/last', { redo })

function editable(el: EventTarget | null) {
  if (!(el instanceof HTMLElement)) return false
  return el.isContentEditable || el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT'
}

/** ⌘Z / Ctrl+Z undoes, ⌘⇧Z / Ctrl+Shift+Z / Ctrl+Y brings back. Text fields keep their own undo. */
export function useUndoShortcuts() {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.repeat || editable(e.target)) return
      // e.code works with any keyboard layout (Я on the Russian one)
      if (e.code === 'KeyZ') {
        e.preventDefault()
        void revertLast(e.shiftKey)
      } else if (e.code === 'KeyY' && !isMac) {
        e.preventDefault()
        void revertLast(true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
}
