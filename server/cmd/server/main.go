// Command server 啟動 Channel 後端 HTTP 服務(SQLite 原型)。
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tim72117/shuttle/internal/adminauth"
	"github.com/tim72117/shuttle/internal/adminconsole"
	"github.com/tim72117/shuttle/internal/api"
	"github.com/tim72117/shuttle/internal/auth"
	"github.com/tim72117/shuttle/internal/llm"
	"github.com/tim72117/shuttle/internal/model"
	"github.com/tim72117/shuttle/internal/store"
	"github.com/tim72117/shuttle/internal/wanttools"

	"github.com/joho/godotenv"
)

func main() {
	// 載入 .env(若存在):讓 DATABASE_URL 等環境變數免手動 export。
	// 找不到 .env 不算錯誤(維持本機 SQLite 後備)。
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("載入 .env: %v", err)
	}

	// 預設只綁 127.0.0.1:本機開發不對外部網路開放,Windows 防火牆不會跳出詢問框。
	// 雲端(Cloud Run 等)需要監聽所有介面時,由下方 PORT 環境變數覆寫。
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP 監聽位址")
	dbPath := flag.String("db", "shuttle.db", "DB 連線:SQLite 檔案路徑,或 DATABASE_URL 未設時的後備")
	seed := flag.Bool("seed", true, "資料庫為空時寫入示範資料")
	jwtSecret := flag.String("jwt-secret", "dev-secret-change-me", "JWT 簽章金鑰")
	devMode := flag.Bool("dev", true, "開發模式:Apple token 不驗簽章")
	llmKind := flag.String("llm", "want", "分析器:want(真實 LLM)| mock(假 LLM,送出觸發預設情境,供 web 操作)")
	adminEnabled := flag.Bool("admin", false, "是否啟用管理後台(/admin/api/*);未設時整個功能完全不注冊,如同不存在")
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
	// ADMIN_ENABLED 是管理後台這整個功能的總開關。未設定(或設成非 truthy 值)時,
	// 完全不會建立 adminauth/adminconsole 的任何東西,也不會掛任何 /admin/* 路由,
	// 就跟這個功能不存在一樣——刻意不给它任何「半啟用」的中間狀態。
	if v := os.Getenv("ADMIN_ENABLED"); v != "" {
		*adminEnabled = v == "1" || strings.EqualFold(v, "true")
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
			// kind 空字串存 nil(model.Entry.Kind 為 *string),非空才帶指標。
			var kind *string
			if e.Kind != "" {
				kind = &e.Kind
			}
			// 寫入時不自動歸組(TripID 留 nil):record_entry 會列出時間相符的候選行程,
			// 由 LLM 判斷後呼叫 add_to_trip 工具歸入(或新建)。
			err := st.InsertEntry(model.Entry{
				ID:        id,
				ChannelID: channelID,
				Title:     e.Title,
				Start:     e.Start,
				StartTime: e.StartTime,
				End:       e.End,
				EndTime:   e.EndTime,
				Kind:      kind,
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
	wanttools.BindNotify(srv.NotifyEntriesUpdated)
	wanttools.BindEntryUpdating(srv.NotifyEntryUpdating)
	wanttools.BindAskUser(srv.NotifyAskUser)
	wanttools.BindTaskCreated(srv.NotifyTaskCreated)
	wanttools.BindTaskEntryReady(srv.NotifyTaskEntryReady)
	wanttools.BindRecommendedPlaces(srv.NotifyRecommendedPlaces)

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

	// 管理後台(/admin/api/*)完全獨立於上面的業務路由:只有 *adminEnabled 為真
	// 才會建立 adminauth/adminconsole 的 handler 並注冊路由。未啟用時,這裡整段
	// 都不會執行,adminconsole.NewHandler 不會被呼叫、/admin/* 完全不會被注冊,
	// 效果上就跟這個功能從未存在一樣。
	if *adminEnabled {
		adminAuth := adminauth.New(st, !*devMode)
		if created, err := adminAuth.Bootstrap(os.Getenv("ADMIN_BOOTSTRAP_EMAIL"), os.Getenv("ADMIN_BOOTSTRAP_PASSWORD")); err != nil {
			log.Printf("admin bootstrap: %v", err)
		} else if created {
			log.Printf("已建立管理員帳號 %s", os.Getenv("ADMIN_BOOTSTRAP_EMAIL"))
		}
		log.Printf("管理後台已啟用(目前管理員帳號數: %d)", adminAuth.Count())

		adminMux := http.NewServeMux()
		adminconsole.NewHandler(adminAuth, st).Register(adminMux)
		// /admin/* 獨立套一層 CORS:管理 SPA(web/src/admin)是完全獨立部署的
		// Vite 專案,呼叫時帶 credentials: 'include'(見 web/src/admin/src/api.ts),
		// 故不能用 "*" 當 Allow-Origin(帶憑證的跨站請求瀏覽器會擋),改為回顯
		// 請求的 Origin,並開 Allow-Credentials。
		mux.Handle("/admin/", withAdminCORS(adminMux))
	}

	mux.Handle("/", staticHandler())

	log.Printf("Channel server 監聽 %s,DB=%s", *addr, dbKind)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// withAdminCORS 讓 /admin/* 支援跨來源、帶憑證(cookie)的請求:回顯呼叫端的
// Origin(而非 "*"),並開啟 Access-Control-Allow-Credentials,讓獨立部署的
// 管理 SPA(web/src/admin,credentials: 'include')能在開發/正式環境都正常
// 帶到 admin_session cookie。僅影響 /admin/* 這條路徑,不影響既有 /v1、/internal
// 路由(那些路由的 CORS 由 internal/api 的 cors() 自行處理,維持原樣)。
func withAdminCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
		{Title: "開會敲定 Q3 產品規格", Start: "2026-06-29", StartTime: "15:00"},
		{Title: "準備預算上調提案(+15%)", Start: "2026-06-30"},
		{Title: "修登入頁的 bug", Start: ""},
	} {
		e.ID = "ent_" + randHex()
		e.ChannelID = ch.ID
		e.CreatedAt = nowUTC()
		_ = st.InsertEntry(e)
	}
	log.Printf("已寫入示範頻道 %s", ch.ID)
	return nil
}
