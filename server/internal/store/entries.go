package store

import (
	"errors"

	"github.com/channel/server/internal/model"
	"gorm.io/gorm"
)

func toEntry(r entryRow) model.Entry {
	return model.Entry{
		ID:        r.ID,
		ChannelID: r.ChannelID,
		Item:      r.Item,
		Start:     r.Start,
		StartTime: r.StartTime,
		End:       r.End,
		EndTime:   r.EndTime,
		Location:  r.Location,
		Lat:       r.Lat,
		Lng:       r.Lng,
		TripID:    r.TripID,
		Category:  r.Category,
		Tags:      r.Tags,
		Summary:   r.Summary,
		Kind:      r.Kind,
		Detail:    r.Detail,
		CreatedAt: r.CreatedAt,
	}
}

// InsertEntry 寫入一筆條目。entry 為主體,可獨立存在(不依附 message)。
// 來源訊息的關聯改由 LinkEntryMessage 另外建立(多對多)。
func (s *Store) InsertEntry(e model.Entry) error {
	r := entryRow{
		ID:        e.ID,
		ChannelID: e.ChannelID,
		Item:      e.Item,
		Start:     e.Start,
		StartTime: e.StartTime,
		End:       e.End,
		EndTime:   e.EndTime,
		Location:  e.Location,
		Lat:       e.Lat,
		Lng:       e.Lng,
		TripID:    e.TripID,
		Category:  e.Category,
		Tags:      e.Tags,
		Summary:   e.Summary,
		Kind:      e.Kind,
		Detail:    e.Detail,
		CreatedAt: e.CreatedAt,
	}
	return s.db.Create(&r).Error
}

// SetEntryLatLng 更新 entry 的經緯度（由 geo goroutine 非同步呼叫）。
func (s *Store) SetEntryLatLng(id string, lat, lng float64) error {
	return s.db.Model(&entryRow{}).Where("id = ?", id).
		Updates(map[string]any{"lat": lat, "lng": lng}).Error
}

// UpdateEntry 更新一筆 entry 的可編輯欄位；留空字串的欄位不更新。
func (s *Store) UpdateEntry(id, item, start, startTime, end, endTime, location, summary, kind string, detail map[string]any) error {
	fields := map[string]any{}
	if item != "" {
		fields["item"] = item
	}
	if start != "" {
		fields["start"] = start
	}
	if startTime != "" {
		fields["start_time"] = startTime
	}
	if end != "" {
		fields["end_at"] = end
	}
	if endTime != "" {
		fields["end_time"] = endTime
	}
	if location != "" {
		fields["location"] = location
	}
	if summary != "" {
		fields["summary"] = summary
	}
	if kind != "" {
		fields["kind"] = kind
	}
	if detail != nil {
		fields["detail"] = detail
	}
	if len(fields) == 0 {
		return nil
	}
	return s.db.Model(&entryRow{}).Where("id = ?", id).Updates(fields).Error
}

// EntryExists 確認 entry 是否存在。
func (s *Store) EntryExists(id string) (bool, error) {
	var count int64
	err := s.db.Model(&entryRow{}).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}

// DeleteEntry 刪除一筆條目。
func (s *Store) DeleteEntry(id string) error {
	return s.db.Where("id = ?", id).Delete(&entryRow{}).Error
}

// GetEntry 依 ID 取單一條目;查無回 ErrNotFound。
// add_to_trip 工具新建 trip 時用它取得 entry 的時間範圍當 trip 初值。
func (s *Store) GetEntry(entryID string) (model.Entry, error) {
	var r entryRow
	err := s.db.Where("id = ?", entryID).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Entry{}, ErrNotFound
	}
	if err != nil {
		return model.Entry{}, err
	}
	return toEntry(r), nil
}

// ListEntriesByChannel 回傳頻道的所有條目,依開始時間排序。
func (s *Store) ListEntriesByChannel(channelID string) ([]model.Entry, error) {
	var rows []entryRow
	err := s.db.Where("channel_id = ?", channelID).
		Order("start ASC, created_at ASC").Find(&rows).Error
	return mapEntries(rows), err
}

// ListEntriesByRange 回傳頻道中 start 落在 [from, to] 的條目,依開始時間排序。
// from / to 為 'YYYY-MM-DD' 或 'YYYY-MM-DD HH:MM';留空表示該端不設限。
// start 以 ISO 格式字串儲存,字典序即時間序,故可用字串比較做範圍。
// 註:start 為空字串(無時間)的條目不會落在任何範圍內,僅在 from、to 皆空時納入。
func (s *Store) ListEntriesByRange(channelID, from, to string) ([]model.Entry, error) {
	q := s.db.Where("channel_id = ?", channelID)
	if from != "" {
		q = q.Where("start >= ?", from)
	}
	if to != "" {
		// to 若只到日期(YYYY-MM-DD),補到當日最後一刻,讓當天有時刻的條目也納入。
		upper := to
		if len(to) == 10 {
			upper = to + " 23:59"
		}
		q = q.Where("start <> '' AND start <= ?", upper)
	}
	var rows []entryRow
	err := q.Order("start ASC, created_at ASC").Find(&rows).Error
	return mapEntries(rows), err
}

func mapEntries(rows []entryRow) []model.Entry {
	out := make([]model.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, toEntry(r))
	}
	return out
}
