// Command server 啟動 Channel 後端 HTTP 服務(SQLite 原型)。
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/channel/server/internal/api"
	"github.com/channel/server/internal/auth"
	"github.com/channel/server/internal/llm"
	"github.com/channel/server/internal/model"
	"github.com/channel/server/internal/store"
	"github.com/channel/server/internal/wanttools"

	"github.com/joho/godotenv"
)

func main() {
	// 載入 .env(若存在):讓 DATABASE_URL 等環境變數免手動 export。
	// 找不到 .env 不算錯誤(維持本機 SQLite 後備)。
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("載入 .env: %v", err)
	}

	addr := flag.String("addr", ":8080", "HTTP 監聽位址")
	dbPath := flag.String("db", "channel.db", "DB 連線:SQLite 檔案路徑,或 DATABASE_URL 未設時的後備")
	seed := flag.Bool("seed", true, "資料庫為空時寫入示範資料")
	jwtSecret := flag.String("jwt-secret", "dev-secret-change-me", "JWT 簽章金鑰")
	devMode := flag.Bool("dev", true, "開發模式:Apple token 不驗簽章")
	llmKind := flag.String("llm", "want", "分析器:want(真實 LLM)| mock(假 LLM,送出觸發預設情境,供 web 操作)")
	flag.Parse()

	// Cloud Run 等托管環境只方便傳環境變數(不方便改 ENTRYPOINT 傳 flag),
	// 故讓環境變數在有設時覆寫對應 flag 預設值;未設則維持本機 flag 行為不變。
	// PORT 由平台注入(Cloud Run 預設 8080),覆寫監聽位址。
	if p := os.Getenv("PORT"); p != "" {
		*addr = ":" + p
	}
	if s := os.Getenv("JWT_SECRET"); s != "" {
		*jwtSecret = s
	}
	if v := os.Getenv("DEV_MODE"); v != "" {
		*devMode = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("SEED"); v != "" {
		*seed = v == "1" || strings.EqualFold(v, "true")
	}

	// DATABASE_URL(postgres://…,如 Neon)優先;未設時退回 -db 的 SQLite。
	dsn := *dbPath
	if env := os.Getenv("DATABASE_URL"); env != "" {
		dsn = env
	}

	st, err := store.Open(dsn)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if *seed {
		if err := seedUsers(st); err != nil {
			log.Printf("seed users: %v", err)
		}
		if err := seedIfEmpty(st); err != nil {
			log.Printf("seed: %v", err)
		}
	}

	// 分析器:預設 want LLM 引擎;-llm mock 改用假分析器(供 web 實際操作,免連 LLM)。
	var analyzer llm.Analyzer
	if *llmKind == "mock" {
		// mock 不接真 LLM:送出觸發預設情境,直接用 store 寫 entry(走相同的
		// FindOrCreateTrip 歸組路徑)。不需 BindSink/BindStore(那是 want 工具用的)。
		analyzer = llm.NewMock(st)
		log.Printf("LLM 分析器: mock(假 LLM,送出觸發預設情境)")
	} else {
		// want LLM 引擎(WantPool,per-session orchestrator 外殼)。初始化失敗直接 fatal。
		pool, err := llm.NewWantPool()
		if err != nil {
			log.Fatalf("初始化 want 分析器失敗: %v", err)
		}
		analyzer = pool
		// 注入條目持久化:record_entry 工具解析出的條目同步寫進 DB(entry 為主體,
		// 獨立寫入),回傳新 entry ID。
		wanttools.BindSink(func(channelID string, e wanttools.RecordedEntry) (string, error) {
			id := "ent_" + randHex()
			// 寫入時不自動歸組(TripID 留 nil):record_entry 會列出時間相符的候選行程,
			// 由 LLM 判斷後呼叫 add_to_trip 工具歸入(或新建)。
			err := st.InsertEntry(model.Entry{
				ID:        id,
				ChannelID: channelID,
				Item:      e.Item,
				Start:     e.Start,
				End:       e.End,
				AllDay:    e.AllDay,
				CreatedAt: nowUTC(),
			})
			return id, err
		})
		// 提供 query_entries 工具查詢用的 store:agent 提問時自己按時間範圍查條目。
		wanttools.BindStore(st)
		log.Printf("LLM 分析器: want 引擎(WantPool)")
	}

	signer := auth.NewSigner(*jwtSecret, 30*24*time.Hour)
	srv := api.New(st, analyzer, signer, *devMode)

	dbKind := "sqlite:" + dsn
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		dbKind = "postgres" // 不印含密碼的 DSN
	}
	// 組合最終 handler:API 路由優先;其餘交給前端靜態檔(SPA fallback)。
	// 注意:/public/{token} 由前端 React 路由處理,不放在後端 API 路由裡。
	mux := http.NewServeMux()
	mux.Handle("/v1/", srv.Routes())
	mux.Handle("/internal/", srv.Routes())
	mux.Handle("/health", srv.Routes())
	mux.Handle("/", staticHandler())

	log.Printf("Channel server 監聽 %s,DB=%s", *addr, dbKind)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// seedUsers 確保可邀請的使用者目錄存在(冪等,每次啟動都套用)。
// 同時為示範使用者設定可登入的 email 與預設密碼(開發測試用),
// 帳號為 <name>@channel.dev,密碼一律 "password"。
func seedUsers(st *store.Store) error {
	directory := []struct {
		user  model.User
		email string
	}{
		// usr_me 是示範頻道(seedIfEmpty)的建立者/owner,需先存在於 users 表,
		// 否則寫入 members 中介表會違反外鍵約束(Postgres 會擋,SQLite 預設放行)。
		{model.User{ID: "usr_me", Name: "我", AvatarColor: "#4A90D9"}, "me@channel.dev"},
		{model.User{ID: "usr_alice", Name: "Alice", AvatarColor: "#E07A5F"}, "alice@channel.dev"},
		{model.User{ID: "usr_bob", Name: "Bob", AvatarColor: "#3D9970"}, "bob@channel.dev"},
		{model.User{ID: "usr_carol", Name: "Carol", AvatarColor: "#B07AE0"}, "carol@channel.dev"},
		{model.User{ID: "usr_dave", Name: "Dave", AvatarColor: "#E0B24A"}, "dave@channel.dev"},
	}
	// 預設密碼只算一次雜湊(四個帳號共用同一明文 "password")。
	devHash, err := auth.HashPassword("password")
	if err != nil {
		return err
	}
	for _, d := range directory {
		if err := st.UpsertUser(d.user); err != nil {
			return err
		}
		if err := st.SetUserPassword(d.user.ID, d.email, devHash); err != nil {
			return err
		}
	}
	return nil
}

// seedIfEmpty 在沒有任何頻道時建立一個示範頻道(對齊 App 端 Mock)。
func seedIfEmpty(st *store.Store) error {
	n, err := st.CountChannels()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	me := model.User{ID: "usr_me", Name: "我", AvatarColor: "#4A90D9"}
	ch, err := st.CreateChannel("ch_001", "產品討論", me)
	if err != nil {
		return err
	}
	// 原話不存後端;seed 直接寫入示範 entry(事件/條目),對齊「entry 為主體」。
	for _, e := range []model.Entry{
		{Item: "開會敲定 Q3 產品規格", Start: "2026-06-29 15:00"},
		{Item: "準備預算上調提案(+15%)", Start: "2026-06-30", AllDay: true},
		{Item: "修登入頁的 bug", Start: ""},
	} {
		e.ID = "ent_" + randHex()
		e.ChannelID = ch.ID
		e.CreatedAt = nowUTC()
		_ = st.InsertEntry(e)
	}
	log.Printf("已寫入示範頻道 %s", ch.ID)
	return nil
}
