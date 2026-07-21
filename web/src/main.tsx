import React from 'react'
import ReactDOM from 'react-dom/client'
import { App } from './App'
import { DebugApp } from './DebugApp'
import './styles.css'

const isDebug = new URLSearchParams(window.location.search).has('debug')
// isDemo:網址帶 ?demo 時,正式桌面版介面(DesktopRail)會多顯示幾顆「試做用」
// 導覽項目(推薦景點卡片/橫滑/地圖),方便不切去 DebugApp 也能看試做效果。
// 與 isDebug(整個切換成獨立工作台畫面)是不同機制,兩者互不影響、可同時存在。
const isDemo = new URLSearchParams(window.location.search).has('demo')

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    {isDebug ? <DebugApp /> : <App isDemo={isDemo} />}
  </React.StrictMode>,
)
