package trip

import (
	"strings"

	"github.com/channel/server/internal/store"
	"github.com/channel/server/internal/tripsvc"
	"github.com/channel/server/internal/wanttools"
	"github.com/tim72117/want/types"
)

var tripService *tripsvc.Service

// BindTripStore 注入 store 並初始化 tripService（server 啟動時呼叫）。
func BindTripStore(s *store.Store) {
	tripService = tripsvc.New(s, nil)
}

func currentChannel(ctx types.ToolContext) string { return wanttools.ChannelFrom(ctx) }

func normalizeEntryID(id string) string {
	if !strings.HasPrefix(id, "ent_") {
		return "ent_" + id
	}
	return id
}
