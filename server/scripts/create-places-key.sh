#!/usr/bin/env bash
# 建立一把新的 Google API 金鑰,並限制只能呼叫 Places API(新版 + 舊版)。
#
# 為什麼限制兩個服務:
#   - places.googleapis.com        新版 Places API (New),程式碼目前使用
#   - places-backend.googleapis.com 舊版 Places API,保留相容(可日後移除)
#   兩個都加,新舊端點都能用,避免再踩「key 只限舊版、打新版 400」的坑。
#
# 用法:
#   ./scripts/create-places-key.sh
#
# 需求:已安裝並登入 gcloud(gcloud auth login),且對目標專案有建立金鑰的權限。

set -euo pipefail

PROJECT="shuttle-045094509"
KEY_NAME="channel-places-$(date +%Y%m%d 2>/dev/null || echo new)"
SERVICES="places.googleapis.com,places-backend.googleapis.com"

echo "專案:        $PROJECT"
echo "金鑰顯示名稱:$KEY_NAME"
echo "限制服務:    $SERVICES"
echo "---"

# 建立金鑰(--api-target 可重複,分別限制到兩個服務)。
echo "正在建立金鑰…"
CREATE_OUT=$(gcloud services api-keys create \
  --project="$PROJECT" \
  --display-name="$KEY_NAME" \
  --api-target=service=places.googleapis.com \
  --api-target=service=places-backend.googleapis.com \
  --format="value(response.keyString, name)" 2>&1) || {
    echo "✗ 建立失敗:" >&2
    echo "$CREATE_OUT" >&2
    exit 1
  }

# response.keyString 是實際金鑰值;name 是資源路徑(供日後管理/刪除)。
KEY_STRING=$(echo "$CREATE_OUT" | awk '{print $1}')
KEY_RESOURCE=$(echo "$CREATE_OUT" | awk '{print $2}')

if [ -z "$KEY_STRING" ]; then
  echo "✗ 建立指令成功但沒取到 keyString,完整輸出:" >&2
  echo "$CREATE_OUT" >&2
  echo "可到 Console 查看:https://console.cloud.google.com/apis/credentials?project=$PROJECT" >&2
  exit 1
fi

echo "✓ 金鑰已建立"
echo "  資源路徑:$KEY_RESOURCE"
echo "---"

# 立即用新版端點驗證金鑰可用。
echo "驗證金鑰(打新版 Places API)…"
HTTP_CODE=$(curl -sS -o /tmp/places_verify.json -w "%{http_code}" \
  -X POST "https://places.googleapis.com/v1/places:searchText" \
  -H "Content-Type: application/json" \
  -H "X-Goog-Api-Key: $KEY_STRING" \
  -H "X-Goog-FieldMask: places.displayName,places.formattedAddress,places.location" \
  -d '{"textQuery":"宮古島希爾頓嘉悅里酒店","pageSize":1}')

if [ "$HTTP_CODE" = "200" ]; then
  echo "✓ HTTP 200 — 金鑰可用"
else
  echo "△ HTTP $HTTP_CODE — 金鑰剛建立,限制可能尚未生效(通常數十秒~數分鐘),稍後再用 test-places-key.sh 重試" >&2
  echo "  Google 回傳:$(cat /tmp/places_verify.json)" >&2
fi
rm -f /tmp/places_verify.json
echo "---"

# 印出金鑰值供手動貼進 .env(刻意不自動寫檔,避免把值寫進可能被記錄的地方)。
echo "請把下面這行貼進 server/.env(取代現有的 GOOGLE_PLACES_API_KEY):"
echo
echo "GOOGLE_PLACES_API_KEY=$KEY_STRING"
echo
echo "(貼完後可執行 ./scripts/test-places-key.sh 再驗證一次)"
