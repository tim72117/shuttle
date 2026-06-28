package store

import (
	"errors"
	"time"

	"github.com/channel/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ListChannelsForUser 回傳指定使用者參與(為成員)的頻道,依更新時間新到舊。
// memberCount 與 lastMessagePreview 以子查詢取得。
func (s *Store) ListChannelsForUser(userID string) ([]model.Channel, error) {
	type chanAgg struct {
		ID                 string
		Name               string
		OwnerID            string
		UpdatedAt          time.Time
		MemberCount        int
		LastMessagePreview *string
	}
	// 原話已移至裝置端,後端不再有 messages;預覽改取最近一筆 entry 的事項。
	var rows []chanAgg
	err := s.db.
		Table("channels c").
		Select(`c.id, c.name, c.owner_id, c.updated_at,
			(SELECT COUNT(*) FROM members m2 WHERE m2.channel_id = c.id) AS member_count,
			(SELECT item FROM entries e WHERE e.channel_id = c.id
			 ORDER BY e.created_at DESC LIMIT 1) AS last_message_preview`).
		Joins("JOIN members m ON m.channel_id = c.id AND m.user_id = ?", userID).
		Order("c.updated_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]model.Channel, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.Channel{
			ID:                 r.ID,
			Name:               r.Name,
			OwnerID:            r.OwnerID,
			UpdatedAt:          r.UpdatedAt,
			MemberCount:        r.MemberCount,
			LastMessagePreview: r.LastMessagePreview,
		})
	}
	return out, nil
}

// CreateChannel 建立頻道,建立者即為擁有者(owner),並自動成為成員。
func (s *Store) CreateChannel(id, name string, creator model.User) (model.Channel, error) {
	t := now()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		ch := channelRow{ID: id, Name: name, OwnerID: creator.ID, CreatedAt: t, UpdatedAt: t}
		if err := tx.Create(&ch).Error; err != nil {
			return err
		}
		// 建立者加入成員(中介表)。
		// 建立者即 owner,預設給 editor 角色(可記事/編輯)。
		return tx.Create(&memberLink{ChannelID: id, UserID: creator.ID, Role: model.RoleEditor}).Error
	})
	if err != nil {
		return model.Channel{}, err
	}
	return model.Channel{ID: id, Name: name, OwnerID: creator.ID, MemberCount: 1, UpdatedAt: t}, nil
}

// GetChannelOwner 回傳頻道的 owner_id;頻道不存在回 ErrNotFound。
func (s *Store) GetChannelOwner(channelID string) (string, error) {
	var cr channelRow
	err := s.db.Select("owner_id").Where("id = ?", channelID).First(&cr).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return cr.OwnerID, nil
}

// GetChannelName 回傳頻道名稱；頻道不存在回 ErrNotFound。
func (s *Store) GetChannelName(channelID string) (string, error) {
	var cr channelRow
	err := s.db.Select("name").Where("id = ?", channelID).First(&cr).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return cr.Name, nil
}

// ListAllChannels 回傳所有頻道(供 internal CLI 使用)。
func (s *Store) ListAllChannels() ([]model.Channel, error) {
	var rows []channelRow
	err := s.db.Order("updated_at DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.Channel, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.Channel{ID: r.ID, Name: r.Name, OwnerID: r.OwnerID, UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}

// CountChannels 回傳頻道總數(seed 判斷資料庫是否為空用)。
func (s *Store) CountChannels() (int, error) {
	var n int64
	err := s.db.Model(&channelRow{}).Count(&n).Error
	return int(n), err
}

// channelExists 確認頻道存在。
func (s *Store) channelExists(id string) (bool, error) {
	var n int64
	err := s.db.Model(&channelRow{}).Where("id = ?", id).Count(&n).Error
	return n > 0, err
}

// ----- 成員 -----

// ListMembers 回傳頻道成員(從 users 表撈,依名稱排序)。
func (s *Store) ListMembers(channelID string) ([]model.Member, error) {
	type memberAgg struct {
		ID          string
		Name        string
		AvatarColor string
		Role        string
	}
	var rows []memberAgg
	err := s.db.
		Table("users").
		Select("users.id, users.name, users.avatar_color, m.role").
		Joins("JOIN members m ON m.user_id = users.id").
		Where("m.channel_id = ?", channelID).
		Order("users.name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.Member{
			User: model.User{ID: r.ID, Name: r.Name, AvatarColor: r.AvatarColor},
			Role: r.Role,
		})
	}
	return out, nil
}

// AddMember 加入成員(冪等),以指定角色加入;role 留空則預設 viewer。
// 回傳更新後的成員清單(含角色)。
func (s *Store) AddMember(channelID string, u model.User, role string) ([]model.Member, error) {
	ok, err := s.channelExists(channelID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	if role == "" {
		role = model.RoleViewer
	}
	// 冪等:已是成員則忽略(不覆寫既有角色)。
	link := memberLink{ChannelID: channelID, UserID: u.ID, Role: role}
	if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; err != nil {
		return nil, err
	}
	return s.ListMembers(channelID)
}

// SetMemberRole 變更成員在頻道內的角色(editor/viewer)。成員不存在則回 ErrNotFound。
func (s *Store) SetMemberRole(channelID, userID, role string) error {
	res := s.db.Model(&memberLink{}).
		Where("channel_id = ? AND user_id = ?", channelID, userID).
		Update("role", role)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetMemberRole 回傳成員在頻道內的角色;非成員回 ErrNotFound。
func (s *Store) GetMemberRole(channelID, userID string) (string, error) {
	var link memberLink
	err := s.db.Select("role").
		Where("channel_id = ? AND user_id = ?", channelID, userID).
		First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return link.Role, nil
}

// ----- 使用者目錄 -----

// UpsertUser 寫入或更新一筆使用者(供 seed)。
func (s *Store) UpsertUser(u model.User) error {
	r := userRow{ID: u.ID, Name: u.Name, AvatarColor: u.AvatarColor}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "avatar_color"}),
	}).Create(&r).Error
}

// memberLink 對應 many2many 的中介表 members(用於直接寫入/冪等)。
// Role 決定成員在頻道內的權限(editor/viewer);預設 viewer。
type memberLink struct {
	ChannelID string `gorm:"primaryKey;column:channel_id"`
	UserID    string `gorm:"primaryKey;column:user_id"`
	Role      string `gorm:"column:role;not null;default:viewer"`
}

func (memberLink) TableName() string { return "members" }
