package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/EgoSay/kairos/core/journal"
	"github.com/EgoSay/kairos/server/auth"
	"github.com/EgoSay/kairos/store"
)

// registerJournalRoutes reuses the existing session/PAT authorizer for private JSON routes.
func (s *APIV1Service) registerJournalRoutes(ctx context.Context, e *echo.Echo) {
	authorizer := NewAuthorizer(s.Store, s.Secret).WithRateLimiter(s.RateLimiter)
	group := e.Group("/api/v1/journal")
	group.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			setAPIResponseNoStoreHeaders(c.Response().Header())
			r := c.Request()
			result := authorizer.Authenticate(r.Context(), r.Header.Get("Authorization"))
			if err := authorizer.CheckAccess(r.Context(), "/memos.api.v1.JournalService/Private", result); err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"message": "请先登录"})
			}
			r = r.WithContext(auth.ApplyToContext(r.Context(), result))
			limit := int64(MaxAPIRequestBytes)
			if r.URL.Path == "/api/v1/journal/backups/preview" {
				limit = 1 << 30
			}
			r.Body = http.MaxBytesReader(c.Response(), r.Body, limit)
			c.SetRequest(r)
			if _, err := s.journalOwner(c); err != nil {
				return journalError(c, err)
			}
			return next(c)
		}
	})
	group.GET("/preferences", s.journalPreferences)
	group.PATCH("/preferences", s.journalUpdatePreferences)
	group.GET("/review", s.journalReview)
	group.GET("/calendar", s.journalCalendar)
	s.registerJournalPartitionRoutes(group)
	s.registerJournalRecordRoutes(group)
	s.registerJournalInsightRoutes(group)
	s.registerJournalShareRoutes(group, e)
	s.registerJournalBackupRoutes(group)
	go s.RunPartitionDeliveryWorker(ctx)
	go s.RunJournalRetention(ctx)
}

func (s *APIV1Service) journalOwner(c *echo.Context) (*store.User, error) {
	u, err := s.fetchCurrentUser(c.Request().Context())
	if err != nil {
		return nil, err
	}
	if u == nil || u.RowStatus != store.Normal {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	return u, nil
}

func journalBind(c *echo.Context, target any) error {
	d := json.NewDecoder(c.Request().Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return status.Error(codes.InvalidArgument, "invalid request body")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return status.Error(codes.InvalidArgument, "request must contain one JSON object")
	}
	return nil
}

func journalError(c *echo.Context, err error) error {
	code := http.StatusBadRequest
	if errors.Is(err, store.ErrJournalConflict) {
		code = http.StatusConflict
	} else if st, ok := status.FromError(err); ok {
		code = runtime.HTTPStatusFromCode(st.Code())
	}
	return c.JSON(code, map[string]string{"message": err.Error()})
}

func (s *APIV1Service) journalPreferences(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	p, err := journal.GetPreferences(c.Request().Context(), s.Store, u.ID)
	if err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

func (s *APIV1Service) journalUpdatePreferences(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	var patch struct {
		ExcludedMemoNames *[]string `json:"excludedMemoNames"`
		CalendarVisible   *bool     `json:"calendarVisible"`
		CalendarColor     *bool     `json:"calendarColor"`
		Timezone          *string   `json:"timezone"`
	}
	if err := journalBind(c, &patch); err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	for range 3 {
		doc, err := s.Store.GetJournalDocument(ctx, u.ID, "preferences", "main")
		if err != nil {
			return journalError(c, err)
		}
		p := journal.Preferences{ExcludedMemoNames: []string{}, CalendarVisible: true, CalendarColor: true, Timezone: "Asia/Shanghai"}
		version := int64(0)
		if doc != nil {
			if err := json.Unmarshal(doc.Payload, &p); err != nil {
				return journalError(c, err)
			}
			version = doc.Version
		}
		if patch.ExcludedMemoNames != nil {
			if len(*patch.ExcludedMemoNames) > 10000 {
				return journalError(c, errors.New("too many exclusions"))
			}
			p.ExcludedMemoNames = *patch.ExcludedMemoNames
		}
		if patch.CalendarVisible != nil {
			p.CalendarVisible = *patch.CalendarVisible
		}
		if patch.CalendarColor != nil {
			p.CalendarColor = *patch.CalendarColor
		}
		if patch.Timezone != nil {
			if _, err := time.LoadLocation(*patch.Timezone); err != nil {
				return journalError(c, errors.New("invalid timezone"))
			}
			p.Timezone = *patch.Timezone
		}
		payload, _ := json.Marshal(p)
		_, err = s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: u.ID, Kind: "preferences", Key: "main", Payload: payload}, version)
		if errors.Is(err, store.ErrJournalConflict) {
			continue
		}
		if err != nil {
			return journalError(c, err)
		}
		return c.JSON(http.StatusOK, p)
	}
	return journalError(c, store.ErrJournalConflict)
}

func (s *APIV1Service) journalReview(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	date, zone := c.QueryParam("date"), c.QueryParam("timezone")
	names, err := journal.DailyReview(c.Request().Context(), s.Store, u.ID, date, zone)
	if err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"date": date, "memoNames": names})
}

func (s *APIV1Service) journalCalendar(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	counts, err := journal.Calendar(c.Request().Context(), s.Store, u.ID, c.QueryParam("month"), c.QueryParam("timezone"))
	if err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"counts": counts})
}
