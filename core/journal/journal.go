// Package journal owns private review preferences, daily selections and journal access.
package journal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/EgoSay/kairos/core/access"
	"github.com/EgoSay/kairos/store"
)

// Preferences are optional reading and display choices, never tasks.
type Preferences struct {
	ExcludedMemoNames []string `json:"excludedMemoNames"`
	CalendarVisible   bool     `json:"calendarVisible"`
	CalendarColor     bool     `json:"calendarColor"`
	Timezone          string   `json:"timezone"`
}

// GetPreferences returns private settings with calm defaults.
func GetPreferences(ctx context.Context, s *store.Store, owner int32) (Preferences, error) {
	p := Preferences{ExcludedMemoNames: []string{}, CalendarVisible: true, CalendarColor: true, Timezone: "Asia/Shanghai"}
	doc, err := s.GetJournalDocument(ctx, owner, "preferences", "main")
	if err != nil {
		return p, err
	}
	if doc != nil {
		err = json.Unmarshal(doc.Payload, &p)
	}
	return p, err
}

// MemoNames returns only the owner's records and excludes soft-deleted items.
func MemoNames(ctx context.Context, s *store.Store, owner int32, includeArchived bool) ([]*store.Memo, error) {
	viewer, err := s.GetUser(ctx, &store.FindUser{ID: &owner})
	if err != nil {
		return nil, err
	}
	if !access.IsActiveUser(viewer) {
		return nil, errors.New("journal owner is not active")
	}
	find := &store.FindMemo{CreatorID: &owner, ExcludeComments: true}
	if !includeArchived {
		normal := store.Normal
		find.RowStatus = &normal
	}
	memos, err := s.ListMemos(ctx, find)
	if err != nil {
		return nil, err
	}
	trash, err := s.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &owner, Kind: "trash"})
	if err != nil {
		return nil, err
	}
	deleted := map[string]bool{}
	for _, d := range trash {
		deleted[d.Key] = true
	}
	result := make([]*store.Memo, 0, len(memos))
	for _, m := range memos {
		if deleted[m.UID] {
			continue
		}
		readContext, err := access.ResolveMemoReadContext(ctx, s, m, viewer, false, nil)
		if err != nil {
			return nil, err
		}
		if access.CheckMemoReadContext(readContext).Allowed() {
			result = append(result, m)
		}
	}
	return result, nil
}

// OwnedMemo checks ownership and the current soft-delete boundary on every read.
func OwnedMemo(ctx context.Context, s *store.Store, owner int32, name string) (*store.Memo, error) {
	uid := strings.TrimPrefix(name, "memos/")
	if uid == "" || strings.Contains(uid, "/") {
		return nil, errors.New("invalid memo name")
	}
	m, err := s.GetMemo(ctx, &store.FindMemo{UID: &uid, CreatorID: &owner})
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("record is not available")
	}
	trash, err := s.GetJournalDocument(ctx, owner, "trash", uid)
	if err != nil {
		return nil, err
	}
	if trash != nil {
		return nil, errors.New("record is not available")
	}
	viewer, err := s.GetUser(ctx, &store.FindUser{ID: &owner})
	if err != nil {
		return nil, err
	}
	if !access.IsActiveUser(viewer) {
		return nil, errors.New("record is not available")
	}
	readContext, err := access.ResolveMemoReadContext(ctx, s, m, viewer, false, nil)
	if err != nil {
		return nil, err
	}
	if !access.CheckMemoReadContext(readContext).Allowed() {
		return nil, errors.New("record is not available")
	}
	return m, nil
}

// DailyReview freezes an initial set for a local date, then only removes ineligible items.
func DailyReview(ctx context.Context, s *store.Store, owner int32, date, timezone string) ([]string, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, errors.New("invalid timezone")
	}
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, errors.New("invalid date")
	}
	p, err := GetPreferences(ctx, s, owner)
	if err != nil {
		return nil, err
	}
	memos, err := MemoNames(ctx, s, owner, false)
	if err != nil {
		return nil, err
	}
	excluded := map[string]bool{}
	for _, name := range p.ExcludedMemoNames {
		excluded[name] = true
	}
	eligible := map[string]*store.Memo{}
	for _, m := range memos {
		if m.CreatedTs < day.Unix() && !excluded["memos/"+m.UID] {
			eligible["memos/"+m.UID] = m
		}
	}
	key := date + "@" + timezone
	doc, err := s.GetJournalDocument(ctx, owner, "daily-review", key)
	if err != nil {
		return nil, err
	}
	names := []string{}
	if doc == nil {
		if date != time.Now().In(loc).Format("2006-01-02") {
			return nil, errors.New("review date must be today in the selected timezone")
		}
		for name := range eligible {
			names = append(names, name)
		}
		slices.SortFunc(names, func(a, b string) int { return strings.Compare(rank(date, a), rank(date, b)) })
		for i, name := range names {
			t := time.Unix(eligible[name].CreatedTs, 0).In(loc)
			if t.Year() < day.Year() && t.Month() == day.Month() && t.Day() == day.Day() {
				names = append([]string{name}, append(names[:i], names[i+1:]...)...)
				break
			}
		}
		if len(names) > 3 {
			names = names[:3]
		}
		payload, _ := json.Marshal(names)
		doc, err = s.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner, Kind: "daily-review", Key: key, Payload: payload}, 0)
		if errors.Is(err, store.ErrJournalConflict) {
			doc, err = s.GetJournalDocument(ctx, owner, "daily-review", key)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(doc.Payload, &names); err != nil {
		return nil, err
	}
	result := []string{}
	for _, name := range names {
		if eligible[name] != nil {
			result = append(result, name)
		}
	}
	return result, nil
}

func rank(day, name string) string {
	sum := sha256.Sum256([]byte(day + "/" + name))
	return hex.EncodeToString(sum[:])
}

// Calendar counts original records by recorded date, including archives but not trash.
func Calendar(ctx context.Context, s *store.Store, owner int32, month, timezone string) (map[string]int, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, errors.New("invalid timezone")
	}
	start, err := time.ParseInLocation("2006-01", month, loc)
	if err != nil {
		return nil, errors.New("invalid month")
	}
	end := start.AddDate(0, 1, 0)
	memos, err := MemoNames(ctx, s, owner, true)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, m := range memos {
		t := time.Unix(m.CreatedTs, 0).In(loc)
		if !t.Before(start) && t.Before(end) {
			counts[t.Format("2006-01-02")]++
		}
	}
	return counts, nil
}
