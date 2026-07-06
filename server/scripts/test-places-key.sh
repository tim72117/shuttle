#!/usr/bin/env bash
# 測試 Google Places API (New) 金鑰是否可用。
#
# 用途:重置金鑰後,快速確認新 key 能打新版 Places API,再貼進 server/.env。
#
# 用法(擇一):
#   1. 直接帶 key:
#        ./scripts/test-places-key.sh "AIza...你的新key"
#   2. 不帶參數,從 server/.env 的 GOOGLE_PLACES_API_KEY 讀:
#        ./scripts/test-places-key.sh
#
# 成功會印出查到的地點名稱/地址/座標;失敗會印出 Google 回傳的錯誤訊息。

set -euo pipefail

# --- 取得 key ---
KEY="${1:-}"
if [ -z "$KEY" ]; then
  ENV_FILE="$(dirname "$0")/../.env"
  if [ -f "$ENV_FILE" ]; then
    # 從 .env 讀,去除可能的 CR(Windows 換行)與前後空白
    KEY="$(grep '^GOOGLE_PLACES_API_KEY=' "$ENV_FILE" | sed 's/^GOOGLE_PLACES_API_KEY=//' | tr -d '\r\n' | xargs)"
  fi
fi

if [ -z "$KEY" ]; then
  echo "✗ 找不到金鑰:請帶參數 ./scripts/test-places-key.sh \"你的key\",或先在 server/.env 設定 GOOGLE_PLACES_API_KEY" >&2
  exit 1
fi

echo "使用金鑰:${KEY:0:6}…${KEY: -4}(長度 ${#KEY})"

# --- 呼叫新版 Places API (New) Text Search ---
PLACE="${2:-宮古島希爾頓嘉悅里酒店}"
echo "查詢地點:$PLACE"
echo "---"

HTTP_CODE=$(curl -sS -o /tmp/places_resp.json -w "%{http_code}" \
  -X POST "https://places.googleapis.com/v1/places:searchText" \
  -H "Content-Type: application/json" \
  -H "X-Goog-Api-Key: $KEY" \
  -H "X-Goog-FieldMask: places.displayName,places.formattedAddress,places.location" \
  -d "{\"textQuery\":\"$PLACE\",\"pageSize\":1}")

if [ "$HTTP_CODE" = "200" ]; then
  echo "✓ HTTP 200 — 金鑰可用於新版 Places API"
  echo "回傳結果:"
  cat /tmp/places_resp.json
  echo
else
  echo "✗ HTTP $HTTP_CODE — 金鑰無法使用" >&2
  echo "Google 回傳:" >&2
  cat /tmp/places_resp.json >&2
  echo >&2
  echo "---" >&2
  echo "常見原因:" >&2
  echo "  • API Key not found / API_KEY_INVALID → key 錯誤,或 key 的 API 限制沒有涵蓋 places.googleapis.com" >&2
  echo "    到 Console 該 key 的「API 限制」把 Places API (New) 加進允許清單,或改用不限制的 key:" >&2
  echo "    https://console.cloud.google.com/apis/credentials?project=shuttle-045094509" >&2
  echo "  • SERVICE_DISABLED → 專案未啟用 places.googleapis.com(本專案已啟用)" >&2
  exit 1
fi

rm -f /tmp/places_resp.json
