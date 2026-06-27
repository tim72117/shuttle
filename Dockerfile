# Channel server 容器映像。
# 重點:build context 必須是「專案根目錄」(含 server/ 與 want/),
# 因為 server/go.mod 用 replace want => ../../want 指向本地 want 源碼,
# context 限縮在 server/ 會看不到 want/ 而編譯失敗。
# 故 COPY 路徑都相對「根目錄 context」寫(server/... 與 want/...)。
#
# 建置(從專案根目錄):
#   docker build -t channel-server .
# 本機跑(env 由 --env-file 注入,不會把 .env 烤進映像):
#   docker run --rm -p 8080:8080 --env-file server/.env channel-server

# ---- 階段 1:build 前端 ----
FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- 階段 2:編譯 Go ----
# 容器內須重現本地的相對結構:server/go.mod 的 replace 為 ../../want,
# 即從 /src/server 往上兩層(/)再進 want → /want。故 server 放 /src/server、
# want 放 /want,relative replace 才解析得到。
FROM golang:1.26 AS build

RUN git config --global url."https://${GH_PAT}@github.com/".insteadOf "https://github.com/"

# 先單獨複製 go.mod / go.sum 以利 layer 快取(相依沒變時不重抓)。
COPY server/go.mod server/go.sum /src/server/
RUN cd /src/server && GOPRIVATE=github.com/tim72117/want go mod download

# 再複製完整源碼。
COPY server/ /src/server/

# 把前端 dist 放到 server embed 路徑
COPY --from=web-build /web/dist /src/server/cmd/server/web/dist

# 靜態編譯:關 CGO 產出不依賴 libc 的單一執行檔,可放進極小的 base image。
RUN cd /src/server && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/server ./cmd/server

# ---- 階段 3:執行 ----
# distroless:只含執行檔需要的最小 runtime,無 shell、體積小、攻擊面小。
# 內含 CA 憑證,連 Neon(sslmode=require)的 TLS 才驗得過。
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server

# Cloud Run 會注入 PORT(預設 8080);main.go 讀 PORT 覆寫監聽位址。
EXPOSE 8080
ENTRYPOINT ["/app/server"]
