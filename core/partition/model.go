package partition

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/EgoSay/kairos/internal/webhook"
	"github.com/EgoSay/kairos/store"
)

const (
	PartitionKind = "partition"
	TargetKind    = "partition-target"
	MappingKind   = "partition-mapping"
	DeliveryKind  = "partition-delivery"
	ExternalKind  = "partition-external"
	SequenceKind  = "partition-sequence"
)

var ErrNotFound = errors.New("partition resource not found")
var ErrInvalid = errors.New("invalid partition request")

// Repository persists each partition, target and delivery independently with CAS.
type Repository interface {
	GetJournalDocument(context.Context, int32, string, string) (*store.JournalDocument, error)
	ListJournalDocuments(context.Context, *store.FindJournalDocument) ([]*store.JournalDocument, error)
	PutJournalDocument(context.Context, *store.JournalDocument, int64) (*store.JournalDocument, error)
	DeleteJournalDocument(context.Context, int32, string, string, int64) error
}

type Partition struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

type Fields struct {
	Content bool `json:"content"`
	Date    bool `json:"date"`
	Media   bool `json:"media"`
}

// Target is inactive until Enabled is explicitly selected. Epoch invalidates
// queued work whenever configuration changes, including pause/resume.
type Target struct {
	ID               string `json:"id"`
	PartitionID      string `json:"partitionId"`
	Name             string `json:"name"`
	URL              string `json:"url"`
	SigningSecret    string `json:"signingSecret,omitempty"`
	HasSigningSecret bool   `json:"hasSigningSecret"`
	Enabled          bool   `json:"enabled"`
	AutoUpdate       bool   `json:"autoUpdate"`
	Retract          bool   `json:"retract"`
	Fields           Fields `json:"fields"`
	Epoch            int64  `json:"epoch"`
	Version          int64  `json:"version"`
}

type Mapping struct {
	Suspended   bool   `json:"suspended"`
	MemoUID     string `json:"memoUid"`
	PartitionID string `json:"partitionId"`
	Version     int64  `json:"version"`
}

// Media contains only bytes explicitly approved for this target. Metadata is
// stripped before construction; no global access token is delivered.
type Media struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content []byte `json:"content"`
}

type Memo struct {
	UID        string
	Content    string
	RecordTime int64
	Revision   string
	Active     bool
	Media      []Media
}

type Delivery struct {
	MappingVersion int64  `json:"mappingVersion"`
	Sequence       int64  `json:"sequence"`
	ID             string `json:"id"`
	SourceEventID  string `json:"sourceEventId,omitempty"`
	MemoUID        string `json:"memoUid"`
	PartitionID    string `json:"partitionId"`
	TargetID       string `json:"targetId"`
	TargetName     string `json:"targetName"`
	Revision       string `json:"revision"`
	Epoch          int64  `json:"epoch"`
	Event          string `json:"event"`
	Status         string `json:"status"`
	Attempts       int    `json:"attempts"`
	NextAttemptTs  int64  `json:"nextAttemptTs"`
	LeaseUntilTs   int64  `json:"leaseUntilTs"`
	CreatedTs      int64  `json:"createdTs"`
	UpdatedTs      int64  `json:"updatedTs"`
	LastError      string `json:"lastError,omitempty"`
	ExternalID     string `json:"externalId,omitempty"`
	PublishedURL   string `json:"publishedUrl,omitempty"`
	Version        int64  `json:"version"`
}

type Payload struct {
	TargetID   string  `json:"targetId"`
	Sequence   int64   `json:"sequence"`
	EventID    string  `json:"eventId"`
	Event      string  `json:"event"`
	MemoUID    string  `json:"memoUid"`
	Revision   string  `json:"revision,omitempty"`
	ExternalID string  `json:"externalId,omitempty"`
	Content    *string `json:"content,omitempty"`
	RecordTime *int64  `json:"recordTime,omitempty"`
	Media      []Media `json:"media,omitempty"`
}

// Receipt is an optional receiver attestation, distinct from HTTP acceptance.
// Matching eventId and externalId are required to accept a publication claim.
type Receipt struct {
	EventID      string `json:"eventId"`
	Status       string `json:"status"`
	ExternalID   string `json:"externalId"`
	PublishedURL string `json:"publishedUrl"`
}

type External struct {
	MemoUID      string `json:"memoUid"`
	TargetID     string `json:"targetId"`
	ExternalID   string `json:"externalId"`
	PublishedURL string `json:"publishedUrl"`
}

type ReadMemo func(context.Context, int32, string, bool) (*Memo, error)
type Send func(context.Context, *webhook.Request) (*webhook.DeliveryReceipt, error)

type Service struct {
	Repo     Repository
	ReadMemo ReadMemo
	Send     Send
	Now      func() time.Time
	mu       sync.Mutex
	inflight map[string]context.CancelFunc
}

func New(repo Repository, read ReadMemo) *Service {
	return &Service{Repo: repo, ReadMemo: read, Send: webhook.Deliver, Now: time.Now, inflight: make(map[string]context.CancelFunc)}
}

func decode[T any](doc *store.JournalDocument) (*T, error) {
	if doc == nil {
		return nil, ErrNotFound
	}
	var result T
	if err := json.Unmarshal(doc.Payload, &result); err != nil {
		return nil, errors.Wrap(err, "invalid partition document")
	}
	switch value := any(&result).(type) {
	case *Partition:
		value.Version = doc.Version
	case *Target:
		value.Version = doc.Version
	case *Mapping:
		value.Version = doc.Version
	case *Delivery:
		value.Version = doc.Version
	default:
	}
	return &result, nil
}

func (s *Service) put(ctx context.Context, owner int32, kind, key string, value any, version int64) (*store.JournalDocument, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return s.Repo.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner, Kind: kind, Key: key, Payload: payload}, version)
}

func (s *Service) documents(ctx context.Context, owner int32, kind string) ([]*store.JournalDocument, error) {
	return s.Repo.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &owner, Kind: kind})
}
