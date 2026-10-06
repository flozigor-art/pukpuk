// Browser-side media helpers: posters and thumbnails at upload time, audio
// waveforms and ID3 tags. Everything is best-effort; the storage node
// regenerates proper derivatives later.

function canvasJpeg(src: CanvasImageSource, w: number, h: number, maxSide: number, quality = 0.82): Promise<Blob | null> {
  const scale = Math.min(1, maxSide / Math.max(w, h))
  const cw = Math.max(1, Math.round(w * scale))
  const ch = Math.max(1, Math.round(h * scale))
  const canvas = document.createElement('canvas')
  canvas.width = cw
  canvas.height = ch
  const ctx = canvas.getContext('2d')
  if (!ctx) return Promise.resolve(null)
  ctx.drawImage(src, 0, 0, cw, ch)
  return new Promise((resolve) => canvas.toBlob((b) => resolve(b), 'image/jpeg', quality))
}

export async function videoPoster(file: File): Promise<{ poster: Blob; thumb: Blob; duration: number } | null> {
  const url = URL.createObjectURL(file)
  const video = document.createElement('video')
  video.muted = true
  video.playsInline = true
  video.preload = 'auto'
  video.src = url
  try {
    const ready = await new Promise<boolean>((resolve) => {
      const t = window.setTimeout(() => resolve(false), 15000)
      video.onloadeddata = () => {
        video.currentTime = Math.min(1, (video.duration || 1) * 0.25)
      }
      video.onseeked = () => {
        window.clearTimeout(t)
        resolve(true)
      }
      video.onerror = () => {
        window.clearTimeout(t)
        resolve(false)
      }
    })
    if (!ready || !video.videoWidth) return null
    const poster = await canvasJpeg(video, video.videoWidth, video.videoHeight, 1280)
    const thumb = await canvasJpeg(video, video.videoWidth, video.videoHeight, 480, 0.78)
    if (!poster || !thumb) return null
    return { poster, thumb, duration: video.duration * 1000 }
  } finally {
    video.removeAttribute('src')
    video.load()
    URL.revokeObjectURL(url)
  }
}

/** Profile picture: the centre square of an image, scaled to `side` px, as JPEG. */
export async function squareJpeg(file: Blob, side = 256): Promise<Blob | null> {
  try {
    const bmp = await createImageBitmap(file)
    const s = Math.min(bmp.width, bmp.height)
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = Math.min(side, s)
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    ctx.drawImage(bmp, (bmp.width - s) / 2, (bmp.height - s) / 2, s, s, 0, 0, canvas.width, canvas.height)
    bmp.close()
    return await new Promise((resolve) => canvas.toBlob((b) => resolve(b), 'image/jpeg', 0.86))
  } catch {
    return null
  }
}

export async function imageThumb(file: Blob): Promise<Blob | null> {
  try {
    const bmp = await createImageBitmap(file)
    const b = await canvasJpeg(bmp, bmp.width, bmp.height, 480, 0.8)
    bmp.close()
    return b
  } catch {
    return null
  }
}

/** Waveform resolution; the same as proto.WaveformBins on the server. */
export const WAVEFORM_BINS = 1000

/**
 * Loudness (RMS) of WAVEFORM_BINS slices of the track, 0..100. Peaks of
 * mastered music sit at the limiter ceiling almost everywhere; RMS follows the
 * arrangement, so intro, verses, drops and breaks are visible.
 */
export async function audioPeaks(file: Blob, bins = WAVEFORM_BINS): Promise<{ peaks: number[]; duration: number } | null> {
  if (file.size > 80 * 1024 * 1024) return null
  try {
    const AC = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
    const ctx = new AC()
    const buf = await ctx.decodeAudioData(await file.arrayBuffer())
    ctx.close()
    const chans = Array.from({ length: Math.min(2, buf.numberOfChannels) }, (_, i) => buf.getChannelData(i))
    const len = chans[0].length
    const n = Math.min(bins, len)
    const raw: number[] = []
    let peak = 0
    for (let i = 0; i < n; i++) {
      const from = Math.floor((i * len) / n)
      const to = Math.floor(((i + 1) * len) / n)
      let sum = 0
      let cnt = 0
      for (let j = from; j < to; j += 2) {
        let v = 0
        for (const c of chans) v += c[j]
        v /= chans.length
        sum += v * v
        cnt++
      }
      const rms = Math.sqrt(sum / Math.max(1, cnt))
      raw.push(rms)
      if (rms > peak) peak = rms
    }
    return { peaks: raw.map((v) => (peak ? Math.round((v / peak) * 100) : 0)), duration: buf.duration * 1000 }
  } catch {
    return null
  }
}

export async function mediaDuration(file: File): Promise<number | null> {
  const url = URL.createObjectURL(file)
  const el = document.createElement(file.type.startsWith('video') ? 'video' : 'audio')
  el.preload = 'metadata'
  el.src = url
  try {
    return await new Promise((resolve) => {
      const t = window.setTimeout(() => resolve(null), 8000)
      el.onloadedmetadata = () => {
        window.clearTimeout(t)
        resolve(isFinite(el.duration) ? el.duration * 1000 : null)
      }
      el.onerror = () => {
        window.clearTimeout(t)
        resolve(null)
      }
    })
  } finally {
    URL.revokeObjectURL(url)
  }
}

// ---- ID3 -------------------------------------------------------------------

export interface Id3 {
  title?: string
  artist?: string
  album?: string
  bpm?: number
  key?: string
  genre?: string
  cover?: Blob
}

const dec = (label: string) => {
  try {
    return new TextDecoder(label)
  } catch {
    return new TextDecoder()
  }
}

/** Latin-1 text with Cyrillic bytes is almost always cp1251 in practice. */
function decodeLegacy(b: Uint8Array): string {
  let high = 0
  let cyr = 0
  for (const x of b) {
    if (x >= 0x80) high++
    if (x >= 0xc0) cyr++
  }
  if (high > 0 && cyr / high > 0.6) return dec('windows-1251').decode(b)
  return dec('iso-8859-1').decode(b)
}

function decodeText(enc: number, b: Uint8Array): string {
  let s: string
  switch (enc) {
    case 1:
      s = b[0] === 0xfe && b[1] === 0xff ? dec('utf-16be').decode(b.subarray(2)) : dec('utf-16le').decode(b)
      break
    case 2:
      s = dec('utf-16be').decode(b)
      break
    case 3:
      s = dec('utf-8').decode(b)
      break
    default:
      s = decodeLegacy(b)
  }
  return s.replace(/\u0000+$/g, '').split('\u0000')[0].replace(/^﻿/, '').trim()
}

const syncsafe = (b: Uint8Array, o: number) => ((b[o] & 0x7f) << 21) | ((b[o + 1] & 0x7f) << 14) | ((b[o + 2] & 0x7f) << 7) | (b[o + 3] & 0x7f)
const u32 = (b: Uint8Array, o: number) => ((b[o] << 24) | (b[o + 1] << 16) | (b[o + 2] << 8) | b[o + 3]) >>> 0

export async function readId3(file: File): Promise<Id3> {
  const out: Id3 = {}
  try {
    const head = new Uint8Array(await file.slice(0, 10).arrayBuffer())
    if (head[0] === 0x49 && head[1] === 0x44 && head[2] === 0x33 && (head[3] === 3 || head[3] === 4)) {
      const ver = head[3]
      const size = syncsafe(head, 6)
      const b = new Uint8Array(await file.slice(10, 10 + Math.min(size, 16 * 1024 * 1024)).arrayBuffer())
      let p = 0
      if (head[5] & 0x40) p += ver === 4 ? syncsafe(b, 0) : u32(b, 0) + 4
      while (p + 10 <= b.length) {
        const id = String.fromCharCode(b[p], b[p + 1], b[p + 2], b[p + 3])
        if (!/^[A-Z0-9]{4}$/.test(id)) break
        const fsize = ver === 4 ? syncsafe(b, p + 4) : u32(b, p + 4)
        const data = b.subarray(p + 10, p + 10 + fsize)
        p += 10 + fsize
        if (fsize <= 0 || data.length < fsize) break
        const text = () => decodeText(data[0], data.subarray(1))
        switch (id) {
          case 'TIT2':
            out.title = text()
            break
          case 'TPE1':
            out.artist = text()
            break
          case 'TALB':
            out.album = text()
            break
          case 'TBPM': {
            const n = parseInt(text(), 10)
            if (n > 0 && n < 400) out.bpm = n
            break
          }
          case 'TKEY':
            out.key = text()
            break
          case 'TCON':
            out.genre = text().replace(/^\(\d+\)/, '')
            break
          case 'APIC': {
            if (out.cover) break
            const enc = data[0]
            let q = 1
            let mimeEnd = q
            while (mimeEnd < data.length && data[mimeEnd] !== 0) mimeEnd++
            const mime = dec('iso-8859-1').decode(data.subarray(q, mimeEnd)) || 'image/jpeg'
            q = mimeEnd + 2 // null + picture type
            if (enc === 1 || enc === 2) {
              while (q + 1 < data.length && !(data[q] === 0 && data[q + 1] === 0)) q += 2
              q += 2
            } else {
              while (q < data.length && data[q] !== 0) q++
              q += 1
            }
            if (q < data.length) out.cover = new Blob([data.slice(q)], { type: mime.includes('/') ? mime : 'image/' + mime.toLowerCase() })
            break
          }
        }
      }
    }
    if (!out.title && file.size > 128) {
      const t = new Uint8Array(await file.slice(file.size - 128).arrayBuffer())
      if (t[0] === 0x54 && t[1] === 0x41 && t[2] === 0x47) {
        const field = (o: number, l: number) => decodeLegacy(t.subarray(o, o + l)).replace(/\u0000.*$/, '').trim()
        out.title = field(3, 30) || undefined
        out.artist = out.artist || field(33, 30) || undefined
        out.album = out.album || field(63, 30) || undefined
      }
    }
  } catch {
    /* tags are optional */
  }
  return out
}
