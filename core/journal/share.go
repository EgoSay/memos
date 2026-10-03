package journal

import (
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/usememos/memos/store"
)

// ShareFilter fixes readable selection semantics independently from pagination.
type ShareFilter struct {
	From           string   `json:"from"`
	To             string   `json:"to"`
	Timezone       string   `json:"timezone"`
	Tags           []string `json:"tags"`
	TagMode        string   `json:"tagMode"`
	IncludeSubtags bool     `json:"includeSubtags"`
	MemoNames      []string `json:"memoNames"`
}

// Validate requires an intentional range, tag or manual selection.
func (f ShareFilter) Validate() error {
	if len(f.MemoNames) == 0 && len(f.Tags) == 0 && f.From == "" && f.To == "" {
		return errors.New("请选择日期、标签或记录")
	}
	if len(f.MemoNames) > 200 || len(f.Tags) > 20 {
		return errors.New("分享范围过大，请缩小范围")
	}
	if f.TagMode != "" && f.TagMode != "any" && f.TagMode != "all" {
		return errors.New("invalid tag selection")
	}
	loc, err := time.LoadLocation(f.Timezone)
	if err != nil {
		return errors.New("请选择有效时区")
	}
	var start, end time.Time
	if f.From != "" {
		start, err = time.ParseInLocation("2006-01-02", f.From, loc)
		if err != nil {
			return errors.New("invalid start date")
		}
	}
	if f.To != "" {
		end, err = time.ParseInLocation("2006-01-02", f.To, loc)
		if err != nil {
			return errors.New("invalid end date")
		}
	}
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		return errors.New("结束日期不能早于开始日期")
	}
	for _, tag := range f.Tags {
		if strings.TrimSpace(tag) == "" {
			return errors.New("标签不能为空")
		}
	}
	return nil
}

// Matches applies date and tags as an intersection with optional manual members.
func (f ShareFilter) Matches(m *store.Memo) bool {
	loc, err := time.LoadLocation(f.Timezone)
	if err != nil {
		return false
	}
	date := time.Unix(m.CreatedTs, 0).In(loc).Format("2006-01-02")
	if f.From != "" && date < f.From || f.To != "" && date > f.To {
		return false
	}
	if len(f.MemoNames) > 0 {
		found := false
		for _, name := range f.MemoNames {
			if name == "memos/"+m.UID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	matched := 0
	for _, required := range f.Tags {
		required = strings.TrimPrefix(required, "#")
		for _, actual := range m.Payload.GetTags() {
			if actual == required || f.IncludeSubtags && strings.HasPrefix(actual, required+"/") {
				matched++
				break
			}
		}
	}
	if len(f.Tags) > 0 {
		if f.TagMode == "all" {
			return matched == len(f.Tags)
		}
		return matched > 0
	}
	return true
}

// ShareMedia contains only intentionally shared sanitized bytes, never private URLs.
type ShareMedia struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Content  []byte `json:"content"`
}

// ShareItem freezes displayed text and media independently from the original memo.
type ShareItem struct {
	MemoName  string       `json:"memoName"`
	Content   string       `json:"content"`
	CreatedTs int64        `json:"createdTs"`
	Media     []ShareMedia `json:"media"`
}

// Share is an owner's revocable bearer grant. Secrets are never included in visitor output.
type Share struct {
	ID                        string      `json:"id"`
	Token                     string      `json:"token"`
	Title                     string      `json:"title"`
	Mode                      string      `json:"mode"`
	Filter                    ShareFilter `json:"filter"`
	ExcludedMemoNames         []string    `json:"excludedMemoNames"`
	ApprovedImportedMemoNames []string    `json:"approvedImportedMemoNames,omitempty"`
	Items                     []ShareItem `json:"items"`
	PasscodeHash              string      `json:"passcodeHash,omitempty"`
	ExpiresTs                 int64       `json:"expiresTs"`
	CreatedTs                 int64       `json:"createdTs"`
	Paused                    bool        `json:"paused"`
	Version                   int64       `json:"version"`
}
