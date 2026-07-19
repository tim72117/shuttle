#!/usr/bin/env bash
# 一次性端到端測試:啟動「動態 mock LLM + 真實 shuttle/tripace server(SQLite)+
# 真實前端 dev server」三個 process,驗證「LLM 推論安排整段行程,過程中新增/
# 刪除/修改,前端即時更新」這件事真的能動。只 mock LLM 這一層,其餘(真實瀏覽器、
# 真實 WebSocket、真實後端、真實 want orchestrator、真實 entry_add/entry_update/
# entry_delete 工具執行、真實 DB 寫入、真實 entries_updated WS 廣播、真實前端
# MultiTrackTimeline 渲染)全部是真的。
#
# 技術細節見 server/internal/mockllm/script.go 開頭的說明(為什麼是「假 LLM 開一台
# vLLM 相容 HTTP 伺服器」這個做法,而不是直接把自訂 provider.AIProvider 實例注入
# want orchestrator——已確認 want v0.0.2 沒有這樣的公開入口)。
#
# 用法:
#   bash server/scripts/run_e2e_mock_llm_test.sh
#
# 啟動後這支腳本會停在前景印 log(三個 process 的 stdout 都轉導到這裡的
# terminal,加前綴區分);按 Ctrl-C 會一併關閉三個 process 並清理暫存 DB 檔案。
#
# 啟動完成後,有兩種方式驗證(推薦用第一種:不需要人工操作瀏覽器,結果明確、
# 可重複執行、適合放進日常開發流程或 CI):
#
# 【推薦】自動化驗證:三個 process 就緒後,另開一個 terminal 視窗執行
#   cd web && npm run test:e2e:mockllm
# 這會跑 web/tests/e2e-mock-llm.spec.ts(Playwright),自動完成登入、進頻道、
# 觸發 assist、逐步斷言時間軸依序出現「東京晴空塔 14:00」→ 卡片進入「更新中」
# 動畫 → 標題/時間變成「東京晴空塔(展望台預約 14:30)14:30」→ 新增「淺草寺
# 10:00」→「東京晴空塔」那筆從時間軸消失,並額外呼叫真實後端 REST API 交叉
# 驗證 DB 最終狀態與畫面一致、全程無 JS 例外。測試逾時內沒通過就是真的有問題,
# 不需要肉眼盯著畫面看變化有沒有發生。baseURL 預設 http://localhost:5173,
# 可用 E2E_BASE_URL 環境變數覆寫(例如 SERVER_ADDR 也被覆寫時,搭配
# E2E_API_BASE_URL 一併覆寫交叉驗證用的後端位址)。
#
# 【手動】想親眼看效果變化的人,依下列步驟操作:
#   1. 瀏覽器開 http://localhost:5173
#   2. 設定頁登入 me@channel.dev / password
#   3. 進入任一頻道(預設種子頻道「產品討論」即可,owner 是 me@channel.dev)
#   4. 點輸入列的「對話」圓鈕展開輸入框,打一句話(內容不影響 mock 劇本本身
#      會依序執行什麼,因為 mock 不解析文字語意——但建議打「幫我安排一趟東京
#      兩天行程」以符合劇本敘事),按 Enter 或送出鈕
#   5. 觀察時間軸:應該依序看到「東京晴空塔 14:00」→ 短暫變成「更新中」動畫後
#      改標題與時間為「東京晴空塔(展望台預約 14:30)14:30」→ 新增「淺草寺
#      10:00」→ 「東京晴空塔」那筆從時間軸消失(entry_delete 執行完成)
#      全程不需要重新整理頁面(真實 WS entries_updated 廣播 + 前端輪詢雙重機制
#      皆會觸發重抓)。
#
# 環境需求:Go 1.26+、Node/npm(前端 vite dev server)。不需要 Docker/Postgres——
# 刻意用 SQLite 檔案(比連 docker Postgres 啟動快很多),符合「加快測試速度」的目標。
# Playwright(自動化驗證用)是 web/package.json 的正式 devDependency,
# `cd web && npm install` 時會一併裝好;第一次使用需另外執行一次
# `cd web && npx playwright install chromium` 下載瀏覽器 binary(不隨 npm
# install 自動下載,是 Playwright 套件本身的機制)。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
WEB_DIR="$(cd "$SERVER_DIR/../web" && pwd)"

MOCKLLM_ADDR="${MOCKLLM_ADDR:-:9999}"
# 預設用 8180(而非 cmd/server 的慣用 :8080),避免與開發者自己可能已在跑的
# 正式(非測試)shuttle/tripace server 實例衝突,兩者可同時存在互不干擾。
SERVER_ADDR="${SERVER_ADDR:-127.0.0.1:8180}"
# 同理,web dev server 預設 port 也可覆寫:開發者可能已經在跑自己平常用的
# `npm run dev`(預設也是 5173),vite 沒設 strictPort,遇到衝突會悄悄漂移到
# 下一個可用 port(5174/5175/…)而不會報錯——這會讓 Playwright 腳本(預設連
# 5173)連到錯的實例。需要與既有 vite process 共存時,設 WEB_PORT 換一個
# 不衝突的 port,並記得 Playwright 那邊用同一個值設 E2E_BASE_URL。
WEB_PORT="${WEB_PORT:-5173}"
# 測試用 SQLite 檔案:每次重跑前清掉,確保劇本假設的初始狀態(空 DB → seed
# 產生「產品討論」頻道與示範資料)一致,不受上次殘留影響。
DB_PATH="${DB_PATH:-/tmp/shuttle_e2e_mockllm_test.db}"

echo "== mock LLM 端到端測試 =="
echo "mockllm:      http://127.0.0.1${MOCKLLM_ADDR}"
echo "server:       http://${SERVER_ADDR}"
echo "web dev:      http://localhost:${WEB_PORT}"
echo "SQLite DB:    ${DB_PATH}"
echo

rm -f "$DB_PATH"

PIDS=()
cleanup() {
  echo
  echo "== 清理:關閉子 process、刪除測試 DB 檔案 =="
  for pid in "${PIDS[@]:-}"; do
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  wait 2>/dev/null || true
  # `go run ./cmd/x` 本身只是個 wrapper:它會編譯出一個真正的 binary 再 fork
  # 成子 process 執行,實際監聽 port 的是那個子 binary,不是上面 kill 掉的
  # wrapper PID——已實測確認,kill wrapper 不會連帶終止子 binary(它們不共用
  # 訊號處理,SIGTERM 不會自動往下傳)。這會導致子 binary 繼續佔用 mockllm/
  # server 的 port,下次重跑腳本時撞見 `bind: address already in use` 而啟動
  # 失敗(此時 server 那個背景 job 因為在 subshell 裡,啟動失敗不會讓外層
  # `set -e` 中止整支腳本,容易被誤以為「三個 process 都正常啟動」,實際上
  # server 用的是上一輪殘留的舊 process,DB 狀態不可預期——這是實際驗證這支
  # 腳本時真的遇到的問題,不是理論推測)。
  # 用啟動時傳入的位址參數(而非 "cmd/mockllm"/"cmd/server" 這種只在 wrapper
  # 命令列出現、子 binary 命令列不含的字串)去比對、逐一清掉子 binary,
  # 比殺整個 process group 安全(已實測 process-group kill 在某些巢狀 shell
  # 情境下會殃及不該波及的 shell,風險較高,故不採用)。
  pkill -f "mockllm -addr ${MOCKLLM_ADDR}" 2>/dev/null || true
  pkill -f "server -addr ${SERVER_ADDR}" 2>/dev/null || true
  rm -f "$DB_PATH"
}
trap cleanup EXIT INT TERM

# ---- 1. mock LLM(假 vLLM,動態劇本) ----
(
  cd "$SERVER_DIR"
  MOCKLLM_ADDR="$MOCKLLM_ADDR" go run ./cmd/mockllm 2>&1 | sed -u 's/^/[mockllm] /'
) &
PIDS+=($!)

# 等 mockllm 就緒(輪詢 /v1/models,最多 15 秒)。
echo "等待 mockllm 就緒..."
for _ in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:9999/v1/models" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

# ---- 2. 真實 shuttle/tripace server(SQLite,指向 mockllm 當 vLLM) ----
# DATABASE_URL 特意設成空字串(而非直接不設):server/.env 若存在且設了
# DATABASE_URL(如指向 Neon Postgres 的開發用連線字串),main.go 的
# godotenv.Load() 不會覆蓋「已存在於環境變數裡」的值,即使那個值是空字串——
# 空字串仍算「已存在」,.env 檔裡的值就不會覆寫進來。main.go 接著判斷
# `os.Getenv("DATABASE_URL") != ""` 為 false,才會真的退回 -db 指定的 SQLite
# 檔案。若只是不設這個變數(留給 shell 環境沒有這個 key),.env 檔會把它填回去,
# 導致這支腳本以為自己在用 SQLite,實際上連去了開發者的真實 Postgres——
# 已實測發現這個陷阱(見旁邊 log 一度印出 DB=postgres 而非預期的 sqlite:...),
# 修正後才確認 DB=sqlite:<DB_PATH> 且啟動時間對齊「加快測試速度」的目標。
(
  cd "$SERVER_DIR"
  DATABASE_URL= \
  AI_PROVIDER=vllm \
  VLLM_BASE_URL="http://127.0.0.1:9999" \
  go run ./cmd/server -addr "$SERVER_ADDR" -db "$DB_PATH" -llm want -seed=true 2>&1 | sed -u 's/^/[server]  /'
) &
PIDS+=($!)

echo "等待 server 就緒..."
for _ in $(seq 1 60); do
  if curl -fsS "http://${SERVER_ADDR}/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

# ---- 3. 真實前端 dev server ----
# VITE_API_BASE 覆寫 web/.env.development 裡的 http://localhost:8080(已提交版控,
# 不應為了本測試修改):用環境變數方式覆寫,Vite 讀取環境變數的優先序高於
# .env.development 檔案內容,讓前端指向本腳本的測試用 server(SERVER_ADDR,
# 預設 :8180)而非開發者自己可能已在跑的 :8080 正式實例。
(
  cd "$WEB_DIR"
  VITE_API_BASE="http://${SERVER_ADDR}" npm run dev -- --port "$WEB_PORT" --strictPort 2>&1 | sed -u 's/^/[web]     /'
) &
PIDS+=($!)

echo
echo "== 三個 process 都已啟動,登入資訊: me@channel.dev / password =="
echo "== 開啟 http://localhost:${WEB_PORT} 開始測試,按 Ctrl-C 結束並清理 =="
echo

wait
