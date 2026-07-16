import { useState } from 'react'
import type { Entry } from './types'

// TaskPlaceholder:task_plan 建立任務時(WS task_created)在時間軸該日期下插入的「新增中」佔位卡。
// entry_add 帶對應 taskID 完成寫入後(WS task_entry_ready)移除,由重抓的正式條目接手顯示。
export type TaskPlaceholder = { taskID: number; date: string; text: string; kind: string }

// ---- 工具函式 ----

function parseDateParts(d: string): { year: string; month: string; day: string } {
  const [year = '', month = '', day = ''] = d.split('-')
  return { year, month, day }
}

function entryTimeLabel(e: Entry): string {
  return e.startTime ?? ''
}

function entrySpanLabel(e: Entry): string {
  if (!e.end || e.end === e.start) return ''
  if (e.end === e.start) return e.endTime ? `~ ${e.endTime}` : ''
  return e.endTime ? `~ ${e.end} ${e.endTime}` : `~ ${e.end}`
}

// ---- 資料型別 ----

// 每一列的種類
type TLRow =
  | { kind: 'year';  key: string; label: string; accent: boolean }
  | { kind: 'month'; key: string; label: string; accent: boolean }
  | { kind: 'entry'; key: string; day: string; dayLabel: string | null; dot: 'main' | 'sub' | 'marker'; isBlank: boolean; isPad: boolean; lineTop: 'accent' | 'normal' | 'none'; lineBot: 'accent' | 'normal' | 'none'; card: { kind: 'main' | 'sub' | 'end'; entry: Entry } | { kind: 'task'; placeholder: TaskPlaceholder } | null }

// ---- 建構函式 ----

// buildTLRows 接受正式條目與 task 佔位卡(只需 date,插在該日期清單最後面;
// 沒有 date 的佔位卡無處可放,直接略過不顯示)。
function buildTLRows(entries: Entry[], taskPlaceholders: TaskPlaceholder[] = []): TLRow[] {
  const sorted = [...entries].sort((a, b) => {
    // 有 start 的條目排在前，沒有的排在後
    if (!a.start && b.start) return 1
    if (a.start && !b.start) return -1
    if (!a.start && !b.start) return 0

    // 都有 start，同一天内：有 startTime 排在前，沒有的排在後
    const aHasTime = !!a.startTime
    const bHasTime = !!b.startTime
    if (aHasTime && !bHasTime) return -1
    if (!aHasTime && bHasTime) return 1

    // 都有時間或都沒有時間，按日期+時間排序
    const aTime = `${a.start}${a.startTime ? ' ' + a.startTime : ''}`
    const bTime = `${b.start}${b.startTime ? ' ' + b.startTime : ''}`
    return aTime.localeCompare(bTime)
  })

  // 1. 判斷主線
  // 主線條件：有結束時間且跨越不同日
  const mainSet = new Set(sorted.filter(e => {
    if (!e.end || e.end === e.start) return false
    return e.end.slice(0, 10) !== (e.start ?? '').slice(0, 10)
  }).map(e => e.id))
  const mainEntries = sorted.filter(e => mainSet.has(e.id))

  // 2. 某日是否在主線跨度內（用於畫橘線）
  function inMainSpan(day: string): boolean {
    return mainEntries.some(m => {
      const s = (m.start ?? '').slice(0, 10)
      const e = (m.end && m.end !== m.start ? m.end : m.start ?? '').slice(0, 10)
      return day >= s && day <= e
    })
  }

  // 3. 收集所有要顯示的天（entry 起始日 + 主線中間天 + 主線結束日 + 最後結束隔天 + 佔位卡日期）
  const daySet = new Set(sorted.map(e => e.start?.slice(0, 10) ?? '').filter(Boolean))
  for (const p of taskPlaceholders) {
    if (p.date) daySet.add(p.date.slice(0, 10))
  }
  let lastMainEnd = ''
  for (const m of mainEntries) {
    const s = (m.start ?? '').slice(0, 10)
    const e = (m.end && m.end !== m.start ? m.end : m.start ?? '').slice(0, 10)
    if (!s || !e) continue
    const d = new Date(s + 'T00:00:00')
    const endD = new Date(e + 'T00:00:00')
    while (d <= endD) { daySet.add(d.toISOString().slice(0, 10)); d.setDate(d.getDate() + 1) }
    if (e > lastMainEnd) lastMainEnd = e
  }
  if (lastMainEnd) {
    const after = new Date(lastMainEnd + 'T00:00:00')
    after.setDate(after.getDate() + 1)
    daySet.add(after.toISOString().slice(0, 10))
  }
  const days = [...daySet].sort()

  // 把主線結束標記當虛擬 entry，用 end 時間排入 sortedAll
  type VEntry = { id: string; sortKey: string; isEnd: boolean; source: Entry }
  const sortedAll: VEntry[] = sorted.map(e => {
    // sortKey 格式：日期 + 時間戳(用於區分有無時間)
    // 沒有 start：用 'zzz~' 排到最後
    // 有 start 無 startTime：用 'YYYY-MM-DD~' (~ 排在空格後，無時間排在後)
    // 有 start 有 startTime：用 'YYYY-MM-DD HH:MM'
    let sortKey: string
    if (!e.start) {
      sortKey = 'zzz'
    } else if (!e.startTime) {
      sortKey = `${e.start}~` // ~ 的 ASCII (126) 大於空格 (32)，排到有時間條目後
    } else {
      sortKey = `${e.start} ${e.startTime}`
    }
    return { id: e.id, sortKey, isEnd: false, source: e }
  })
  for (const m of mainEntries) {
    const endStr = m.end && m.end !== m.start
      ? m.endTime ? `${m.end} ${m.endTime}` : `${m.end}~`
      : null
    if (endStr) sortedAll.push({ id: `end-${m.id}`, sortKey: endStr, isEnd: true, source: m })
  }
  sortedAll.sort((a, b) => a.sortKey.localeCompare(b.sortKey))

  // 4. 先把所有 entry 列（不含年月）按順序收集，再填線條
  type Pre = Omit<Extract<TLRow, { kind: 'entry' }>, 'lineTop' | 'lineBot'>
  const pre: Pre[] = []

  for (const day of days) {
    const { day: dayNum } = parseDateParts(day)
    const todayAll = sortedAll.filter(v => v.sortKey.slice(0, 10) === day)
    const todayTasks = taskPlaceholders.filter(p => p.date.slice(0, 10) === day)

    const dayRows: Pre[] = []

    if (todayAll.length === 0 && todayTasks.length === 0) {
      dayRows.push({ kind: 'entry', key: `day-${day}`, day, dayLabel: null, isBlank: true, isPad: false, dot: 'marker', card: null })
    } else {
      todayAll.forEach(v => {
        if (v.isEnd) {
          dayRows.push({ kind: 'entry', key: v.id, day, dayLabel: null, isBlank: false, isPad: false, dot: 'main', card: { kind: 'end', entry: v.source } })
        } else {
          dayRows.push({
            kind: 'entry', key: v.id, day, dayLabel: null, isBlank: false, isPad: false,
            dot: mainSet.has(v.id) ? 'main' : 'sub',
            card: { kind: mainSet.has(v.id) ? 'main' : 'sub', entry: v.source },
          })
        }
      })
      // 佔位卡插在該日期清單最後面(不需精確排序)。
      todayTasks.forEach(p => {
        dayRows.push({ kind: 'entry', key: `task-${p.taskID}`, day, dayLabel: null, isBlank: false, isPad: false, dot: 'sub', card: { kind: 'task', placeholder: p } })
      })
    }

    // 中間天佔位列不顯示日期
    const isBlankDay = todayAll.length === 0 && todayTasks.length === 0
    if (dayRows.length > 0 && !isBlankDay) dayRows[0] = { ...dayRows[0], dayLabel: dayNum }
    dayRows.forEach(r => pre.push(r))
  }

  // 首尾各插一個灰色佔位列
  const firstDay = pre[0]?.day ?? ''
  const lastDay  = pre[pre.length - 1]?.day ?? ''
  const padRow = (day: string): typeof pre[0] => ({ kind: 'entry', key: `pad-${day}`, day, dayLabel: null, isBlank: false, isPad: true, dot: 'marker', card: null })
  const preWithPad = [padRow(firstDay), ...pre, padRow(lastDay)]

  // 5. 填線條
  const withLines = preWithPad.map((row, i): Extract<TLRow, { kind: 'entry' }> => {
    const cur  = !row.isPad && inMainSpan(row.day)
    const prev = i > 0 ? (!preWithPad[i - 1].isPad && inMainSpan(preWithPad[i - 1].day)) : false
    const next = i < preWithPad.length - 1 ? (!preWithPad[i + 1].isPad && inMainSpan(preWithPad[i + 1].day)) : false
    return {
      ...row,
      lineTop: i === 0 ? 'none' : (cur || prev) ? 'accent' : 'normal',
      lineBot: i === preWithPad.length - 1 ? 'none' : (cur && next) ? 'accent' : 'normal',
    }
  })

  // 6. 逐列輸出：遇到年/月變化先插年月列，再插 entry 列
  const rows: TLRow[] = []
  let prevYear = '', prevMonth = ''

  for (const row of withLines) {
    const { year, month } = parseDateParts(row.day)
    const acc = !row.isPad && !row.isBlank && inMainSpan(row.day)
    if (year !== prevYear) {
      rows.push({ kind: 'year', key: `year-${row.day}`, label: year, accent: acc })
      prevYear = year; prevMonth = ''
    }
    if (month !== prevMonth) {
      rows.push({ kind: 'month', key: `month-${row.day}`, label: `${month}月`, accent: acc })
      prevMonth = month
    }
    rows.push(row)
  }

  return rows
}

// ---- 純渲染元件 ----

export function MultiTrackTimeline({ entries, todayRef, updatingIDs, taskPlaceholders }: { entries: Entry[], todayRef?: React.RefObject<HTMLDivElement>, updatingIDs?: Set<string>, taskPlaceholders?: TaskPlaceholder[] }) {
  const rows = buildTLRows(entries, taskPlaceholders ?? [])
  const today = new Date().toISOString().slice(0, 10)
  let todayAttached = false
  return (
    <div className="tl-grid">
      {rows.map(row => {
        if (row.kind === 'year') return (
          <div key={row.key} className="tl-grid-row">
            <div className="tl-col-label tl-year-label">{row.label}</div>
            <div className="tl-col-axis">
              <div className={`tl-vline top${row.accent ? ' accent' : ''}`} />
              <div className={`tl-vline bot${row.accent ? ' accent' : ''}`} />
            </div>
            <div className="tl-col-card" />
          </div>
        )
        if (row.kind === 'month') return (
          <div key={row.key} className="tl-grid-row">
            <div className="tl-col-label tl-month-label">{row.label}</div>
            <div className="tl-col-axis">
              <div className={`tl-vline top${row.accent ? ' accent' : ''}`} />
              <div className={`tl-vline bot${row.accent ? ' accent' : ''}`} />
            </div>
            <div className="tl-col-card" />
          </div>
        )
        // entry row
        const { dot, lineTop, lineBot, card, dayLabel, isBlank } = row
        const rowDate = row.day ?? ''
        const isTodayAnchor = !todayAttached && todayRef && rowDate >= today && !isBlank
        if (isTodayAnchor) todayAttached = true
        return (
          <div key={row.key} ref={isTodayAnchor ? todayRef : undefined} className={`tl-grid-row${isBlank && !row.isPad ? ' blank' : ''}`}>
            {/* 日欄 */}
            <div className="tl-col-label">
              {dayLabel && <span className="tl-date-day">{dayLabel}</span>}
            </div>
            {/* 軸線欄：絕對線 + 置中點 */}
            <div className="tl-col-axis">
              {lineTop !== 'none' && <div className={`tl-vline top${lineTop === 'accent' ? ' accent' : ''}`} />}
              {lineBot !== 'none' && <div className={`tl-vline bot${lineBot === 'accent' ? ' accent' : ''}`} />}
              {isBlank && !row.isPad
                ? <div className="tl-dot-blank" />
                : <div className={dot === 'main' ? 'tl-dot-main' : dot === 'sub' ? 'tl-dot-sub' : 'tl-dot-day'} />
              }
            </div>
            {/* 卡片欄 */}
            <div className="tl-col-card">
              {card?.kind === 'main' && <MainCard entry={card.entry} updating={updatingIDs?.has(card.entry.id)} />}
              {card?.kind === 'sub'  && <SubCard  entry={card.entry} updating={updatingIDs?.has(card.entry.id)} />}
              {card?.kind === 'end'  && <EndCard  entry={card.entry} />}
              {card?.kind === 'task' && <TaskPlaceholderCard placeholder={card.placeholder} />}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function PinIcon() {
  return (
    <svg width="9" height="12" viewBox="0 0 10 14" fill="none" xmlns="http://www.w3.org/2000/svg" style={{ display: 'inline-block', verticalAlign: 'middle', marginRight: 2 }}>
      <path d="M5 0C2.24 0 0 2.24 0 5c0 3.75 5 9 5 9s5-5.25 5-9c0-2.76-2.24-5-5-5z" fill="currentColor"/>
      <circle cx="5" cy="5" r="2" fill="white"/>
    </svg>
  )
}

function NavButton({ location, lat, lng }: { location: string; lat?: number | null; lng?: number | null }) {
  const url = (lat != null && lng != null)
    ? `https://www.google.com/maps/search/?api=1&query=${lat},${lng}`
    : `https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(location)}`
  return (
    <a href={url} target="_blank" rel="noopener noreferrer" className="tl-nav-btn" title="開始導航">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg">
        <path d="M2 12L22 2L12 22L9 13L2 12Z" />
      </svg>
    </a>
  )
}

function MainCard({ entry, updating }: { entry: Entry; updating?: boolean }) {
  const [open, setOpen] = useState(false)
  return (
    <div className={`tl-main-card tl-card-row${updating ? ' updating' : ''}`} onClick={() => setOpen(o => !o)} style={{ cursor: 'pointer' }}>
      <div className="tl-card-content">
        <div className="tl-item">
          <span className="tl-main-title">{entry.title}</span>
        </div>
        {entry.location && <div className="entry-loc"><PinIcon /> {entry.location}</div>}
        <div className={`tl-card-expand${open ? ' open' : ''}`}>
          <div className="tl-card-expand-inner">
            {entry.note && <div className="tl-expand-summary">{entry.note}</div>}
            <div className="tl-expand-row">
              <span className="tl-expand-label">開始</span>
              <span>{entry.start ? (entry.startTime ? `${entry.start} ${entry.startTime}` : entry.start) : '—'}</span>
            </div>
            {entry.end && <div className="tl-expand-row">
              <span className="tl-expand-label">結束</span>
              <span>{entry.endTime ? `${entry.end} ${entry.endTime}` : entry.end}</span>
            </div>}
          </div>
        </div>
      </div>
      {entry.location && <NavButton location={entry.location} lat={entry.lat} lng={entry.lng} />}
    </div>
  )
}

function EndCard({ entry }: { entry: Entry }) {
  return (
    <div className="tl-end-card">
      <span className="tl-end-label">{entry.title} 結束</span>
    </div>
  )
}

function SubCard({ entry, updating }: { entry: Entry; updating?: boolean }) {
  const [open, setOpen] = useState(false)
  const time = entryTimeLabel(entry)
  const span = entrySpanLabel(entry)
  return (
    <div className={`tl-card tl-card-row${span ? ' tl-card-span' : ''}${updating ? ' updating' : ''}`}
      onClick={() => setOpen(o => !o)}
      style={{ cursor: 'pointer' }}>
      <div className="tl-card-content">
        <div className="tl-item">
          {time && <span className="tl-time">{time}</span>}
          {entry.title}
          {span && <span className="tl-span">{span}</span>}
        </div>
        {entry.location && <div className="entry-loc"><PinIcon /> {entry.location}</div>}
        {(entry.category || (entry.tags ?? []).length > 0) && (
          <div className="meta">
            {entry.category && <span className="cat">{entry.category}</span>}
            {(entry.tags ?? []).map(t => <span key={t} className="tag">#{t}</span>)}
          </div>
        )}
        <div className={`tl-card-expand${open ? ' open' : ''}`}>
          <div className="tl-card-expand-inner">
            {entry.note && <div className="tl-expand-summary">{entry.note}</div>}
            {entry.start && <div className="tl-expand-row">
              <span className="tl-expand-label">開始</span>
              <span>{entry.startTime ? `${entry.start} ${entry.startTime}` : entry.start}</span>
            </div>}
            {entry.end && <div className="tl-expand-row">
              <span className="tl-expand-label">結束</span>
              <span>{entry.endTime ? `${entry.end} ${entry.endTime}` : entry.end}</span>
            </div>}
          </div>
        </div>
      </div>
      {entry.location && <NavButton location={entry.location} lat={entry.lat} lng={entry.lng} />}
    </div>
  )
}

// task_plan 建立任務時插入的佔位卡:顯示條目文字,逐字波浪起伏(不淡化);
// entry_add 完成對應 taskID 後由 task_entry_ready 移除、換成正式條目卡。
function TaskPlaceholderCard({ placeholder }: { placeholder: TaskPlaceholder }) {
  const label = placeholder.text
  return (
    <div className="tl-card tl-card-row tl-task-placeholder">
      <div className="tl-card-content">
        <div className="tl-item tl-wave-text" aria-live="polite" aria-label={label}>
          {[...label].map((ch, i) => (
            <span key={i} className="tl-wave-char" style={{ animationDelay: `${i * 0.08}s` }}>
              {ch === ' ' ? ' ' : ch}
            </span>
          ))}
        </div>
      </div>
    </div>
  )
}
