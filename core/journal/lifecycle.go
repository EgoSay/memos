package journal

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/pkg/errors"

	"github.com/usememos/memos/store"
)

// RevokeMemoDerivatives permanently withdraws this item from existing shares
// and removes saved AI output that can reveal the withdrawn source.
func RevokeMemoDerivatives(ctx context.Context, s *store.Store, ownerID int32, memoUID string) error {
	name := "memos/" + strings.TrimPrefix(memoUID, "memos/")
	insights, err := s.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &ownerID, Kind: "insight"})
	if err != nil {
		return err
	}
	for _, doc := range insights {
		var data struct {
			Sources []struct {
				Name string `json:"name"`
			} `json:"sources"`
		}
		if err := json.Unmarshal(doc.Payload, &data); err != nil {
			return err
		}
		for _, source := range data.Sources {
			if source.Name == name {
				if err := s.DeleteJournalDocument(ctx, ownerID, "insight", doc.Key, -1); err != nil {
					return err
				}
				break
			}
		}
	}
	shares, err := s.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &ownerID, Kind: "share"})
	if err != nil {
		return err
	}
	for _, initial := range shares {
		for attempt := 0; attempt < 5; attempt++ {
			doc, err := s.GetJournalDocument(ctx, ownerID, "share", initial.Key)
			if err != nil {
				return err
			}
			if doc == nil {
				break
			}
			var data map[string]json.RawMessage
			if err := json.Unmarshal(doc.Payload, &data); err != nil {
				return err
			}
			excluded := []string{}
			if len(data["excludedMemoNames"]) > 0 {
				if err := json.Unmarshal(data["excludedMemoNames"], &excluded); err != nil {
					return err
				}
			}
			found := false
			for _, item := range excluded {
				if item == name {
					found = true
					break
				}
			}
			if found {
				break
			}
			excluded = append(excluded, name)
			data["excludedMemoNames"], _ = json.Marshal(excluded)
			// Remove withdrawn snapshot bytes as well as hiding their membership.
			var items []ShareItem
			if len(data["items"]) > 0 {
				if err := json.Unmarshal(data["items"], &items); err != nil {
					return err
				}
				kept := []ShareItem{}
				for _, item := range items {
					if item.MemoName != name {
						kept = append(kept, item)
					}
				}
				data["items"], _ = json.Marshal(kept)
			}
			doc.Payload, _ = json.Marshal(data)
			if _, err := s.PutJournalDocument(ctx, doc, doc.Version); err != nil {
				if errors.Is(err, store.ErrJournalConflict) && attempt < 4 {
					continue
				}
				return err
			}
			break
		}
	}
	return nil
}
