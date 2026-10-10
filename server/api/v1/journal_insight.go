package v1

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"

	"github.com/EgoSay/kairos/core/journal"
	"github.com/EgoSay/kairos/internal/ratelimit"
	"github.com/EgoSay/kairos/provider/ai"
	"github.com/EgoSay/kairos/provider/ai/insight"
	"github.com/EgoSay/kairos/store"
)

type journalAIConfig struct {
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
}
type journalInsightSource struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
}
type journalInsightResult struct {
	ID        string                 `json:"id"`
	Text      string                 `json:"text"`
	Citations []string               `json:"citations"`
	Sources   []journalInsightSource `json:"sources"`
	CreatedTs int64                  `json:"createdTs"`
	Stale     bool                   `json:"stale"`
}

func (s *APIV1Service) registerJournalInsightRoutes(g *echo.Group) {
	g.GET("/ai/config", s.journalAISettings)
	g.PUT("/ai/config", s.journalAIConfigure)
	g.POST("/insights/generate", s.journalGenerateInsight)
	g.GET("/insights", s.journalListInsights)
	g.POST("/insights", s.journalSaveInsight)
	g.DELETE("/insights/:id", func(c *echo.Context) error {
		u, err := s.journalOwner(c)
		if err != nil {
			return journalError(c, err)
		}
		if err := s.Store.DeleteJournalDocument(c.Request().Context(), u.ID, "insight", c.Param("id"), -1); err != nil {
			return journalError(c, err)
		}
		return c.JSON(http.StatusOK, map[string]bool{"deleted": true})
	})
}

func (s *APIV1Service) journalAISettings(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	setting, err := s.Store.GetInstanceAISetting(ctx)
	if err != nil {
		return journalError(c, err)
	}
	providers := []map[string]string{}
	for _, p := range setting.GetProviders() {
		if convertAIProviderTypeFromStore(p.GetType()) == ai.ProviderOpenAI {
			providers = append(providers, map[string]string{"id": p.GetId(), "title": p.GetTitle(), "endpoint": p.GetEndpoint()})
		}
	}
	config := journalAIConfig{}
	doc, err := s.Store.GetJournalDocument(ctx, u.ID, "ai-config", "main")
	if err != nil {
		return journalError(c, err)
	}
	if doc != nil {
		if err := json.Unmarshal(doc.Payload, &config); err != nil {
			return journalError(c, err)
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"providers": providers, "config": config})
}

func (s *APIV1Service) journalAIConfigure(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	var config journalAIConfig
	if err := journalBind(c, &config); err != nil {
		return journalError(c, err)
	}
	if len(config.Model) > 200 || strings.TrimSpace(config.Model) == "" {
		return journalError(c, errors.New("请填写模型名称"))
	}
	setting, err := s.Store.GetInstanceAISetting(ctx)
	if err != nil {
		return journalError(c, err)
	}
	p, err := s.resolveAIProvider(setting, config.ProviderID)
	if err != nil {
		return journalError(c, err)
	}
	if p.Type != ai.ProviderOpenAI {
		return journalError(c, errors.New("请选择 OpenAI 兼容服务"))
	}
	doc, err := s.Store.GetJournalDocument(ctx, u.ID, "ai-config", "main")
	if err != nil {
		return journalError(c, err)
	}
	version := int64(0)
	if doc != nil {
		version = doc.Version
	}
	payload, _ := json.Marshal(config)
	_, err = s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: u.ID, Kind: "ai-config", Key: "main", Payload: payload}, version)
	if err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusOK, config)
}

func memoFingerprint(m *store.Memo) string {
	sum := sha256.Sum256([]byte(m.UID + "\x00" + m.Content + "\x00" + time.Unix(m.CreatedTs, 0).UTC().Format(time.RFC3339)))
	return hex.EncodeToString(sum[:])
}

func (s *APIV1Service) journalGenerateInsight(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	if err := s.throttleAndCharge(ratelimit.ScopeTranscribeUser, userKey(u.ID), 1); err != nil {
		return journalError(c, err)
	}
	var req struct {
		MemoNames []string `json:"memoNames"`
	}
	if err := journalBind(c, &req); err != nil {
		return journalError(c, err)
	}
	if len(req.MemoNames) < 1 || len(req.MemoNames) > 30 {
		return journalError(c, errors.New("请选择 1 至 30 条记录"))
	}
	doc, err := s.Store.GetJournalDocument(ctx, u.ID, "ai-config", "main")
	if err != nil {
		return journalError(c, err)
	}
	if doc == nil {
		return journalError(c, errors.New("请先配置 AI 服务与模型"))
	}
	var config journalAIConfig
	if err := json.Unmarshal(doc.Payload, &config); err != nil {
		return journalError(c, err)
	}
	setting, err := s.Store.GetInstanceAISetting(ctx)
	if err != nil {
		return journalError(c, err)
	}
	provider, err := s.resolveAIProvider(setting, config.ProviderID)
	if err != nil {
		return journalError(c, err)
	}
	sources := []insight.Source{}
	refs := []journalInsightSource{}
	seen := map[string]bool{}
	for _, name := range req.MemoNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		m, err := journal.OwnedMemo(ctx, s.Store, u.ID, name)
		if err != nil {
			return journalError(c, err)
		}
		if strings.TrimSpace(m.Content) == "" {
			return journalError(c, errors.New("所选记录有仅含媒体的内容；当前洞察只读取文字"))
		}
		sources = append(sources, insight.Source{Name: name, Date: time.Unix(m.CreatedTs, 0).UTC().Format(time.RFC3339), Text: m.Content})
		refs = append(refs, journalInsightSource{Name: name, Fingerprint: memoFingerprint(m)})
	}
	result, err := insight.Generate(ctx, provider, config.Model, sources)
	if err != nil {
		return journalError(c, err)
	}
	for _, ref := range refs {
		m, err := journal.OwnedMemo(ctx, s.Store, u.ID, ref.Name)
		if err != nil || memoFingerprint(m) != ref.Fingerprint {
			return journalError(c, errors.New("来源已发生变化，请重新选择后生成"))
		}
	}
	return c.JSON(http.StatusOK, journalInsightResult{ID: rand.Text(), Text: result.Text, Citations: result.Citations, Sources: refs, CreatedTs: time.Now().Unix()})
}

func (s *APIV1Service) journalSaveInsight(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	var result journalInsightResult
	if err := journalBind(c, &result); err != nil {
		return journalError(c, err)
	}
	if result.ID == "" || len(result.ID) > 128 || len(result.Text) > 12000 || len(result.Sources) < 1 || len(result.Sources) > 30 {
		return journalError(c, errors.New("invalid insight"))
	}
	allowed := map[string]bool{}
	for _, ref := range result.Sources {
		m, err := journal.OwnedMemo(ctx, s.Store, u.ID, ref.Name)
		if err != nil || memoFingerprint(m) != ref.Fingerprint {
			return journalError(c, errors.New("来源已发生变化，请重新生成"))
		}
		allowed[ref.Name] = true
	}
	for _, name := range result.Citations {
		if !allowed[name] {
			return journalError(c, errors.New("洞察引用了未选择的内容"))
		}
	}
	result.CreatedTs = time.Now().Unix()
	result.Stale = false
	payload, _ := json.Marshal(result)
	_, err = s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: u.ID, Kind: "insight", Key: result.ID, Payload: payload}, 0)
	if err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *APIV1Service) journalListInsights(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	docs, err := s.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &u.ID, Kind: "insight"})
	if err != nil {
		return journalError(c, err)
	}
	results := []journalInsightResult{}
	for _, doc := range docs {
		var result journalInsightResult
		if err := json.Unmarshal(doc.Payload, &result); err != nil {
			return journalError(c, err)
		}
		valid := true
		for _, ref := range result.Sources {
			m, err := journal.OwnedMemo(ctx, s.Store, u.ID, ref.Name)
			if err != nil {
				valid = false
				break
			}
			if memoFingerprint(m) != ref.Fingerprint {
				result.Stale = true
			}
		}
		if !valid {
			if err := s.Store.DeleteJournalDocument(ctx, u.ID, "insight", doc.Key, doc.Version); err != nil {
				return journalError(c, err)
			}
			continue
		}
		results = append(results, result)
	}
	return c.JSON(http.StatusOK, results)
}
