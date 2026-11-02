package service

import (
	"context"
	"errors"
	"math"
	"strings"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/taggraph"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type knowledgeTagEdgeService struct {
	kbService      interfaces.KnowledgeBaseService
	repo           interfaces.KnowledgeTagEdgeRepository
	tagRepo        interfaces.KnowledgeTagRepository
	kbShareService interfaces.KBShareService
	audit          interfaces.AuditLogService
}

// NewKnowledgeTagEdgeService wires tag relationship operations.
func NewKnowledgeTagEdgeService(
	kbService interfaces.KnowledgeBaseService,
	repo interfaces.KnowledgeTagEdgeRepository,
	tagRepo interfaces.KnowledgeTagRepository,
	kbShareService interfaces.KBShareService,
	audit interfaces.AuditLogService,
) (interfaces.KnowledgeTagEdgeService, error) {
	return &knowledgeTagEdgeService{
		kbService:      kbService,
		repo:           repo,
		tagRepo:        tagRepo,
		kbShareService: kbShareService,
		audit:          audit,
	}, nil
}

func (s *knowledgeTagEdgeService) ListRelations(
	ctx context.Context,
	kbID string,
	tagID string,
) ([]*types.KnowledgeTagEdge, error) {
	if strings.TrimSpace(kbID) == "" {
		return nil, werrors.NewBadRequestError("知识库ID不能为空")
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	effectiveTenantID, err := resolveKBReadTenant(ctx, kb, s.kbShareService)
	if err != nil {
		return nil, err
	}
	tagID = strings.TrimSpace(tagID)
	if tagID != "" {
		tag, err := s.tagRepo.GetByID(ctx, effectiveTenantID, tagID)
		if err != nil {
			return nil, err
		}
		if tag.KnowledgeBaseID != kb.ID {
			return nil, werrors.NewNotFoundError("标签不存在")
		}
	}
	return s.repo.ListByKB(ctx, effectiveTenantID, kbID, tagID)
}

func (s *knowledgeTagEdgeService) CreateRelation(
	ctx context.Context,
	kbID string,
	fromTagID string,
	toTagID string,
	relationType string,
	confidence float64,
) (*types.KnowledgeTagEdge, error) {
	kbID = strings.TrimSpace(kbID)
	fromTagID = strings.TrimSpace(fromTagID)
	toTagID = strings.TrimSpace(toTagID)
	relationType = strings.ToUpper(strings.TrimSpace(relationType))
	if kbID == "" || fromTagID == "" || toTagID == "" {
		return nil, werrors.NewBadRequestError("知识库ID和两个标签ID不能为空")
	}
	if !taggraph.ValidRelationType(relationType) {
		return nil, werrors.NewBadRequestError("不支持的标签关系类型")
	}
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
		return nil, werrors.NewBadRequestError("关系置信度必须在 0 到 1 之间")
	}
	if fromTagID == toTagID {
		return nil, werrors.NewBadRequestError("标签不能与自身建立关系")
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	ctx, err = requireKBWrite(ctx, kb)
	if err != nil {
		return nil, err
	}

	// SYNONYM_OF and RELATED_TO are symmetric; store them in a stable order
	// so reversed duplicate requests resolve to the same persisted edge.
	if relationType != taggraph.RelationIsA && fromTagID > toTagID {
		fromTagID, toTagID = toTagID, fromTagID
	}
	edge := &types.KnowledgeTagEdge{
		ID:              uuid.NewString(),
		TenantID:        kb.TenantID,
		KnowledgeBaseID: kb.ID,
		FromTagID:       fromTagID,
		ToTagID:         toTagID,
		RelationType:    relationType,
		Confidence:      confidence,
	}
	if err := s.repo.Create(ctx, edge); err != nil {
		switch {
		case errors.Is(err, taggraph.ErrCycle):
			return nil, werrors.NewBadRequestError("IS_A 关系会形成环路")
		case errors.Is(err, taggraph.ErrTagEndpointMissing):
			return nil, werrors.NewNotFoundError("一个或多个标签不属于该知识库")
		case errors.Is(err, taggraph.ErrInvalidRelation):
			return nil, werrors.NewBadRequestError("不支持的标签关系类型")
		default:
			return nil, err
		}
	}
	recordKBActivity(ctx, s.audit, edge.TenantID, edge.KnowledgeBaseID, types.AuditActionTagUpdated,
		"knowledge_tag_edge", edge.ID, types.AuditOutcomeSuccess, map[string]any{
			"from_tag_id":   edge.FromTagID,
			"to_tag_id":     edge.ToTagID,
			"relation_type": edge.RelationType,
		})
	return edge, nil
}

func (s *knowledgeTagEdgeService) DeleteRelation(ctx context.Context, kbID string, relationID string) error {
	kbID = strings.TrimSpace(kbID)
	relationID = strings.TrimSpace(relationID)
	if kbID == "" || relationID == "" {
		return werrors.NewBadRequestError("知识库ID和关系ID不能为空")
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return err
	}
	ctx, err = requireKBWrite(ctx, kb)
	if err != nil {
		return err
	}
	edge, err := s.repo.GetByID(ctx, kb.TenantID, kb.ID, relationID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return werrors.NewNotFoundError("标签关系不存在")
		}
		return err
	}
	if err := s.repo.Delete(ctx, kb.TenantID, kb.ID, relationID); err != nil {
		return err
	}
	recordKBActivity(ctx, s.audit, edge.TenantID, edge.KnowledgeBaseID, types.AuditActionTagUpdated,
		"knowledge_tag_edge", edge.ID, types.AuditOutcomeSuccess, map[string]any{
			"from_tag_id":   edge.FromTagID,
			"to_tag_id":     edge.ToTagID,
			"relation_type": edge.RelationType,
			"deleted":       true,
		})
	return nil
}

var _ interfaces.KnowledgeTagEdgeService = (*knowledgeTagEdgeService)(nil)
