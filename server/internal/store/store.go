// Package store 封裝持久層(GORM + SQLite)。原型階段用 SQLite,
// 之後可換成 Postgres + pgvector(GORM 換 driver 即可,store 介面不變)。
package store

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/glebarez/sqlite" // 純 Go SQLite driver,免 CGO
	"gorm.io/driver/postgres"    // Postgres driver(Neon / Cloud SQL)
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ErrNotFound 是 store 層統一的「查無資料」錯誤。
var ErrNotFound = errors.New("not found")

type Store struct {
	db *gorm.DB

	// MigrationOK 記錄 Open() 當初呼叫 AutoMigrate 是否成功。AutoMigrate 失敗時
	// Open() 不會回傳 error(見下方註解),而是讓 server 帶著可能不完整的 schema
	// 降級啟動;這個欄位讓之後有需要的呼叫端(例如健康檢查、監控)可以查詢「這個
	// Store 底層 schema 是否可能不完整」,回報服務降級中的訊號。
	MigrationOK bool
}

// Open 開啟(或建立)資料庫並用 AutoMigrate 套用 schema。
// dsn 為 postgres:// 或 postgresql:// 開頭時用 Postgres(Neon / Cloud SQL),
// 否則視為 SQLite 檔案路徑。store 介面不變,GORM 查詢兩邊通用。
func Open(dsn string) (*Store, error) {
	db, err := gorm.Open(dialector(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// members 中介表雖可由 many2many 關聯隱式建立,但那只會建 join 欄位
	// (channel_id / user_id),不含額外的 role 欄。故明確把 memberLink 納入
	// AutoMigrate,GORM 才會補上 role 欄(既有表則 ALTER ADD COLUMN,不損資料)。
	//
	// AutoMigrate 失敗故意不讓 Open() 回傳 error:資料庫連線本身是好的,只是
	// schema 可能有欄位型別衝突、約束衝突等問題未同步,這跟連線失敗是不同的失敗
	// 模式。若讓這裡的 error 往上傳,呼叫端(main.go)目前是 log.Fatalf,會導致
	// 整個 process 直接結束——即使這次的 schema 差異只影響某張表的某個功能,
	// 完全不相關的功能(登入、查頻道列表等)也會一起無法使用。故改成記錄一則
	// 明顯的警示 log 後繼續,讓 server 降級啟動;只有實際用到未同步欄位的功能
	// 才會在被呼叫到時出錯,這是可接受的降級行為。
	migrationOK := true
	if err := db.AutoMigrate(&userRow{}, &channelRow{}, &entryRow{}, &memberLink{}, &tripRow{}, &publicLinkRow{}, &adminUserRow{}, &adminSessionRow{}); err != nil {
		log.Printf("!!! AutoMigrate 失敗,資料庫 schema 可能未同步,部分功能可能異常或無法使用,請盡快檢查: %v", err)
		migrationOK = false
	}
	return &Store{db: db, MigrationOK: migrationOK}, nil
}

// dialector 依 dsn 前綴挑選 GORM driver:
// postgres:// 或 postgresql:// → Postgres;其餘 → SQLite 檔案路徑。
func dialector(dsn string) gorm.Dialector {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return postgres.Open(dsn)
	}
	return sqlite.Open(dsn)
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// now 統一回傳 UTC 時間。
func now() time.Time { return time.Now().UTC() }
