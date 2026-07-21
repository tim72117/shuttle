import { useCallback, useEffect, useRef, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import type { ApiCall, WsEvent } from './api'
import { onApiCall, onWsEvent } from './api'
import { useAppState, PhoneContent } from './App'
import { DebugPanel } from './DebugPanel'
import { RecommendedPlacesList, RecommendedPlacesRow, FAKE_RECOMMENDED_PLACES } from './RecommendedPlaces'
import { RecommendedPlacesMap } from './RecommendedPlacesMap'
import './debug.css'

type DemoMode = 'app' | 'cards' | 'row' | 'map'

// 推薦景點卡片 UI 試做:純假資料展示,不串接任何 API,只是讓主內容區
// 換成 RecommendedPlacesList,方便直接在既有 debug 工作台看到卡片渲染效果。
// 之後若決定要往這方向做,再回頭串進 ChatScreen 的正式資料流。
function RecommendedPlacesDemo() {
  return (
    <>
      <div className="navbar">
        <span style={{ width: 36 }} />
        <span className="title">推薦景點(試做)</span>
        <span style={{ width: 36 }} />
      </div>
      <div className="screen-body">
        <RecommendedPlacesList places={FAKE_RECOMMENDED_PLACES} />
      </div>
    </>
  )
}

// 推薦景點橫向捲動列 UI 試做:同樣純假資料展示,不串接任何 API,把主內容區換成
// RecommendedPlacesRow(卡片橫向一行、可左右拉動瀏覽,比照 App Store/Netflix),
// 復用 RecommendedPlaceCard 的單卡渲染邏輯,只是外層排列方式不同於 cards 模式的垂直清單。
function RecommendedPlacesRowDemo() {
  return (
    <>
      <div className="navbar">
        <span style={{ width: 36 }} />
        <span className="title">推薦景點橫滑(試做)</span>
        <span style={{ width: 36 }} />
      </div>
      <div className="screen-body">
        <RecommendedPlacesRow places={FAKE_RECOMMENDED_PLACES} />
      </div>
    </>
  )
}

// 推薦景點地圖 UI 試做:Maps JavaScript API 極簡風格底圖 + 多標記,同樣用假資料,
// 不串接任何後端。VITE_GOOGLE_MAPS_API_KEY 未設時 RecommendedPlacesMap 會顯示錯誤提示。
function RecommendedPlacesMapDemo() {
  return (
    <>
      <div className="navbar">
        <span style={{ width: 36 }} />
        <span className="title">推薦景點地圖(試做)</span>
        <span style={{ width: 36 }} />
      </div>
      <div className="screen-body" style={{ padding: 0 }}>
        <RecommendedPlacesMap places={FAKE_RECOMMENDED_PLACES} />
      </div>
    </>
  )
}

const LS_PANEL_WIDTH = 'tripace.debugPanelWidth'
const DEFAULT_PANEL_WIDTH = 460
const MIN_PANEL_WIDTH = 320
const MIN_MAIN_WIDTH = 360

// useResizablePanel:拖曳中間分隔線調整右側 debug panel 寬度,寬度存 localStorage
// 下次開啟沿用。左側主內容區用 flex:1 自動吃剩餘空間,不需另外算寬度。
function useResizablePanel() {
  const [panelWidth, setPanelWidth] = useState<number>(() => {
    const saved = Number(localStorage.getItem(LS_PANEL_WIDTH))
    return saved > 0 ? saved : DEFAULT_PANEL_WIDTH
  })
  const draggingRef = useRef(false)

  const onDragStart = useCallback((e: ReactMouseEvent) => {
    e.preventDefault()
    draggingRef.current = true
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
  }, [])

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!draggingRef.current) return
      const next = Math.min(
        Math.max(window.innerWidth - e.clientX, MIN_PANEL_WIDTH),
        window.innerWidth - MIN_MAIN_WIDTH,
      )
      setPanelWidth(next)
    }
    const onUp = () => {
      if (!draggingRef.current) return
      draggingRef.current = false
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      setPanelWidth((w) => {
        localStorage.setItem(LS_PANEL_WIDTH, String(w))
        return w
      })
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
  }, [])

  return { panelWidth, onDragStart }
}

export function DebugApp() {
  const props = useAppState()
  const [calls, setCalls] = useState<ApiCall[]>([])
  const [wsEvents, setWsEvents] = useState<WsEvent[]>([])
  const [demoMode, setDemoMode] = useState<DemoMode>('app')
  const { panelWidth, onDragStart } = useResizablePanel()
  useEffect(() => onApiCall((c) => setCalls((prev) => [c, ...prev].slice(0, 100))), [])
  useEffect(() => onWsEvent((e) => setWsEvents((prev) => [e, ...prev].slice(0, 100))), [])

  return (
    <div className="workbench">
      <div className="workbench-main web-app">
        {demoMode === 'cards' ? (
          <RecommendedPlacesDemo />
        ) : demoMode === 'row' ? (
          <RecommendedPlacesRowDemo />
        ) : demoMode === 'map' ? (
          <RecommendedPlacesMapDemo />
        ) : (
          <PhoneContent {...props} />
        )}
      </div>
      <div className="workbench-resizer" onMouseDown={onDragStart} title="拖曳調整寬度" />
      <div style={{ position: 'fixed', top: 12, left: 12, zIndex: 200, display: 'flex', gap: 8 }}>
        <button
          className="btn-secondary"
          onClick={() => setDemoMode((m) => (m === 'cards' ? 'app' : 'cards'))}
          title="切換推薦景點卡片 UI 試做(假資料)"
        >
          {demoMode === 'cards' ? '← 回到 App' : '推薦景點卡片試做'}
        </button>
        <button
          className="btn-secondary"
          onClick={() => setDemoMode((m) => (m === 'row' ? 'app' : 'row'))}
          title="切換推薦景點橫向捲動 UI 試做(假資料)"
        >
          {demoMode === 'row' ? '← 回到 App' : '推薦景點橫滑試做'}
        </button>
        <button
          className="btn-secondary"
          onClick={() => setDemoMode((m) => (m === 'map' ? 'app' : 'map'))}
          title="切換推薦景點地圖 UI 試做(假資料)"
        >
          {demoMode === 'map' ? '← 回到 App' : '推薦景點地圖試做'}
        </button>
      </div>
      <DebugPanel
        calls={calls}
        onClear={() => setCalls([])}
        wsEvents={wsEvents}
        onClearWsEvents={() => setWsEvents([])}
        cfg={props.cfg}
        channel={props.activeChannel}
        style={{ width: panelWidth, flex: `0 0 ${panelWidth}px` }}
      />
    </div>
  )
}
