import React from 'react'
import ReactDOM from 'react-dom/client'
import { App } from './App'
import './styles.css'

// isDemo:網址帶 ?demo 時,正式桌面版介面(DesktopRail)會多顯示幾顆「試做用」
// 導覽項目(推薦景點卡片/橫滑/地圖、ClientToolsBridge/onagent 串接試做、API/WS
// 狀態面板開關)。原本還有一個獨立的 ?debug 工作台(DebugApp.tsx,整個切換成
// 左右分割的獨立畫面)已移除——那些試做功能都已併入這裡的 ?demo 模式(見
// App.tsx 的 DesktopRail/PanelMode),不再需要一個平行存在的獨立入口。
const isDemo = new URLSearchParams(window.location.search).has('demo')

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App isDemo={isDemo} />
  </React.StrictMode>,
)
