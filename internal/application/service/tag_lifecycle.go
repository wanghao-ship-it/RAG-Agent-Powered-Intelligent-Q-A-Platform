package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/taggraph"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type knowledgeTagLifecycleService struct {
	kbService      interfaces.KnowledgeBaseService
	repo           interfaces.KnowledgeTagLifecycleRepository
	kbShareService interfaces.KBShareService
	audit          interfaces.AuditLogService
}

// NewKnowledgeTagLifecycleService wires candidate creation and review.
func NewKnowledgeTagLifecycleService(
	kbService interfaces.KnowledgeBaseService,
	repo interfaces.KnowledgeTagLifecycleRepository,
	kbShareService interfaces.KBShareService,
	audit interfaces.AuditLogService,
) (interfaces.KnowledgeTagLifecycleService, error) {
	return &knowledgeTagLifecycleService{
		kbService:      kbService,
		repo:           repo,
		kbShareService: kbShareService,
		audit:          audit,
	}, nil
}

func (s *knowledgeTagLifecycleService) CreateCandidate(
	ctx context.Context,
	kbID string,
	name string,
	description string,
	confidence float64,
) (*types.KnowledgeTag, error) {
	kbID = strings.TrimSpace(kbID)
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if kbID == "" || name == "" {
		return nil, werrors.NewBadRequestError("知识库ID和标签名称不能为空")
	}
	if utf8.RuneCountInString(name) > 128 {
		return nil, werrors.NewBadRequestError("标签名称不能超过128个字符")
	}
	if utf8.RuneCountInString(description) > 2000 {
		return nil, werrors.NewBadRequestError("标签说明不能超过2000个字符")
	}
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
		return nil, werrors.NewBadRequestError("标签置信度必须在 0 到 1 之间")
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	ctx, err = requireKBWrite(ctx, kb)
	if err != nil {
		return nil, err
	}

	candidateID := uuid.NewString()
	candidate := &types.KnowledgeTag{
		ID:              candidateID,
		TenantID:        kb.TenantID,
		KnowledgeBaseID: kb.ID,
		Name:            name,
		NormalizedName:  types.NormalizeKnowledgeTagName(name),
		Description:     description,
		Status:          types.KnowledgeTagStatusPending,
		Confidence:      confidence,
		Source:          types.KnowledgeTagSourceAuto,
	}
	if err := s.repo.CreateCandidate(ctx, candidate); err != nil {
		return nil, err
	}
	if candidate.Status == types.KnowledgeTagStatusDeprecated {
		return nil, werrors.NewConflictError("该标签已废弃，不能作为候选标签重新创建")
	}
	if candidate.ID == candidateID {
		recordKBActivity(ctx, s.audit, candidate.TenantID, candidate.KnowledgeBaseID, types.AuditActionTagCreated,
			"knowledge_tag", candidate.ID, types.AuditOutcomeAccepted, map[string]any{
				"name": candidate.Name, "status": candidate.Status, "confidence": candidate.Confidence,
			})
	}
	return candidate, nil
}

func (s *knowledgeTagLifecycleService) ListPending(ctx context.Context, kbID string) ([]*types.KnowledgeTag, error) {
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
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
	return s.repo.ListByStatus(ctx, effectiveTenantID, kbID, types.KnowledgeTagStatusPending)
}

func (s *knowledgeTagLifecycleService) ListCandidateEvidence(
	ctx context.Context,
	kbID string,
	tagID string,
) ([]*types.KnowledgeTagBinding, error) {
	kbID = strings.TrimSpace(kbID)
	tagID = strings.TrimSpace(tagID)
	if kbID == "" || tagID == "" {
		return nil, werrors.NewBadRequestError("知识库ID和标签ID不能为空")
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	effectiveTenantID, err := resolveKBReadTenant(ctx, kb, s.kbShareService)
	if err != nil {
		return nil, err
	}
	candidate, err := s.repo.GetByID(ctx, effectiveTenantID, kbID, tagID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, werrors.NewNotFoundError("标签候选不存在")
		}
		return nil, err
	}
	if candidate.Status != types.KnowledgeTagStatusPending {
		return nil, werrors.NewConflictError("只有 pending 标签候选可以查看待审核证据")
	}
	return s.repo.ListBindingsByTag(ctx, effectiveTenantID, kbID, tagID)
}

func (s *knowledgeTagLifecycleService) ReviewCandidate(
	ctx context.Context,
	kbID string,
	tagID string,
	action string,
) (*types.KnowledgeTag, error) {
	kbID = strings.TrimSpace(kbID)
	tagID = strings.TrimSpace(tagID)
	action = strings.ToLower(strings.TrimSpace(action))
	if kbID == "" || tagID == "" {
		return nil, werrors.NewBadRequestError("知识库ID和标签ID不能为空")
	}
	var targetStatus string
	switch action {
	case "approve":
		targetStatus = types.KnowledgeTagStatusActive
	case "reject":
		targetStatus = types.KnowledgeTagStatusDeprecated
	default:
		return nil, werrors.NewBadRequestError("审核动作只能是 approve 或 reject")
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	ctx, err = requireKBWrite(ctx, kb)
	if err != nil {
		return nil, err
	}
	candidate, err := s.repo.GetByID(ctx, kb.TenantID, kb.ID, tagID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, werrors.NewNotFoundError("标签候选不存在")
		}
		return nil, err
	}
	if candidate.Status == targetStatus {
		return candidate, nil
	}
	if candidate.Status != types.KnowledgeTagStatusPending {
		return nil, werrors.NewConflictError("只有 pending 标签可以审核")
	}
	if err := s.repo.SetStatus(ctx, kb.TenantID, kb.ID, candidate.ID, targetStatus); err != nil {
		if errors.Is(err, taggraph.ErrTagCandidateNotPending) {
			return nil, werrors.NewConflictError("标签候选已被其他请求审核")
		}
		if errors.Is(err, taggraph.ErrInvalidTagStatus) {
			return nil, werrors.NewBadRequestError("标签状态不合法")
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, werrors.NewNotFoundError("标签候选不存在")
		}
		return nil, err
	}
	candidate.Status = targetStatus
	candidate.UpdatedAt = time.Now()
	recordKBActivity(ctx, s.audit, candidate.TenantID, candidate.KnowledgeBaseID, types.AuditActionTagUpdated,
		"knowledge_tag", candidate.ID, types.AuditOutcomeSuccess, map[string]any{
			"status": targetStatus, "action": action,
		})
	return candidate, nil
}

var _ interfaces.KnowledgeTagLifecycleService = (*knowledgeTagLifecycleService)(nil)
