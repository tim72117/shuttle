// LLM(assist/語意查詢)回答時使用的語言偏好。
//
// 只影響 LLM 推論用語,不做任何介面 UI 多語言——介面文字全部維持繁體中文不變。
// 存 localStorage(跨分頁/重新整理仍記得使用者選擇,與專案裡其他設定值一致的做法)。
//
// 獨立成這個小檔案(而非直接放在 App.tsx)是為了讓 api.ts 也能讀取:
// api.ts 送出 assist/query 請求時需要這個值,若把它定義在 App.tsx、
// 讓 api.ts 去 import App.tsx,會形成 api.ts → App.tsx → api.ts 的循環依賴
// (App.tsx 本來就 import * as api from './api')。
export type AssistLang = 'zh-TW' | 'en'

export const ASSIST_LANG_KEY = 'shuttle.assistLang'

const DEFAULT_ASSIST_LANG: AssistLang = 'zh-TW'

// getAssistLang 讀取目前的語言偏好;未設定或值非法時回退預設值(維持現有行為:繁體中文)。
export function getAssistLang(): AssistLang {
  const v = localStorage.getItem(ASSIST_LANG_KEY)
  return v === 'en' ? 'en' : DEFAULT_ASSIST_LANG
}
