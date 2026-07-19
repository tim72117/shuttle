import { defineConfig } from '@playwright/test'

// 這份設定只服務 tests/e2e-mock-llm.spec.ts 這一支端到端測試,不是給一般
// UI 元件測試用(此專案目前沒有其他 Playwright 測試)。刻意保持精簡,只設
// 真的需要覆寫預設值的欄位,其餘用 Playwright 官方預設。
//
// 這支測試「不」負責啟動 mockllm/server/web 三個 process——那是
// server/scripts/run_e2e_mock_llm_test.sh 的職責(該腳本啟動後會停在前景
// 印 log)。故意不用 Playwright 的 webServer 選項自動起 dev server:那三個
// process 有啟動順序依賴(mockllm 先就緒 → server 才能連 → web dev server
// 才有東西可測),混在一起會讓「三個 process 該怎麼串」這件事分散在兩個
// 地方維護,不如讓 shell script 專心管生命週期,這支測試專心當呼叫端。
export default defineConfig({
  testDir: './tests',
  timeout: 30_000, // 見 spec 內個別 expect 的逾時設計說明;mock LLM 全程應在數秒內跑完整段劇本
  fullyParallel: false, // 只有一支測試檔案,且共用同一組後端種子資料,平行跑沒有意義
  retries: 0, // 失敗要如實反映(flaky 用重試蓋過去只會掩蓋真正的時序問題)
  reporter: 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:5173',
    trace: 'retain-on-failure', // 失敗時保留追蹤記錄,方便事後排查是哪一步斷言錯
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
})
