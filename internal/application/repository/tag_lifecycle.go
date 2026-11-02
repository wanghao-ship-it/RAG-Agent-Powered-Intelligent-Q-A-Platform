package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/taggraph"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type knowledgeTagLifecycleRepository struct {
	db *gorm.DB
}

// NewKnowledgeTagLifecycleRepository persists normalized automatic tag candidates.
func NewKnowledgeTagLifecycleRepository(db *gorm.DB) interfaces.KnowledgeTagLifecycleRepository {
	return &knowledgeTagLifecycleRepository{db: db}
}

func (r *knowledgeTagLifecycleRepository) CreateCandidate(ctx context.Context, candidate *types.KnowledgeTag) error {
	if candidate == nil {
		return gorm.ErrInvalidData
	}
	candidate.NormalizedName = types.NormalizeKnowledgeTagName(candidate.Name)
	candidate.Status = types.KnowledgeTagStatusPending
	candidate.Source = types.KnowledgeTagSourceAuto

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			var kbID string
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Table("knowledge_bases").Select("id").
				Where("tenant_id = ? AND id = ?", candidate.TenantID, candidate.KnowledgeBaseID).
				Take(&kbID).Error; err != nil {
				return err
			}
		}

		var existing types.KnowledgeTag
		err := tx.Where("tenant_id = ? AND knowledge_base_id = ? AND normalized_name = ?",
			candidate.TenantID, candidate.KnowledgeBaseID, candidate.NormalizedName).
			Order("created_at ASC, seq_id ASC").First(&existing).Error
		if err == nil {
			*candidate = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(candidate).Error
	})
}

func (r *knowledgeTagLifecycleRepository) ListByStatus(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	status string,
) ([]*types.KnowledgeTag, error) {
	var tags []*types.KnowledgeTag
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND status = ?", tenantID, kbID, status).
		Order("created_at ASC, seq_id ASC").Find(&tags).Error
	return tags, err
}

func (r *knowledgeTagLifecycleRepository) ListBindingsByTag(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagID string,
) ([]*types.KnowledgeTagBinding, error) {
	var bindings []*types.KnowledgeTagBinding
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND tag_id = ?", tenantID, kbID, tagID).
		Order("created_at ASC, knowledge_id ASC").
		Find(&bindings).Error
	return bindings, err
}

func (r *knowledgeTagLifecycleRepository) GetByID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	id string,
) (*types.KnowledgeTag, error) {
	var tag types.KnowledgeTag
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, id).
		First(&tag).Error; err != nil {
		return nil, err
	}
	return &tag, nil
}

func (r *knowledgeTagLifecycleRepository) SetStatus(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	id string,
	status string,
) error {
	if status != types.KnowledgeTagStatusActive && status != types.KnowledgeTagStatusDeprecated {
		return taggraph.ErrInvalidTagStatus
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&types.KnowledgeTag{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id = ? AND status = ?",
				tenantID, kbID, id, types.KnowledgeTagStatusPending).
			UpdateColumns(map[string]any{"status": status, "updated_at": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existing types.KnowledgeTag
			if err := tx.Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, id).
				First(&existing).Error; err != nil {
				return err
			}
			if existing.Status != status {
				return taggraph.ErrTagCandidateNotPending
			}
		}

		if status != types.KnowledgeTagStatusActive {
			return nil
		}
		var knowledgeIDs []string
		if err := tx.Table("knowledge_tag_bindings AS b").
			Joins("JOIN knowledges AS k ON k.id = b.knowledge_id").
			Joins("JOIN chunks AS c ON c.id = b.chunk_id").
			Where("b.tenant_id = ? AND b.knowledge_base_id = ? AND b.tag_id = ?", tenantID, kbID, id).
			Where("k.tenant_id = ? AND k.knowledge_base_id = ? AND k.deleted_at IS NULL", tenantID, kbID).
			Where("k.parse_status NOT IN ?", []string{
				types.ParseStatusCancelled, types.ParseStatusDeleting, types.ParseStatusFailed,
			}).
			Where("c.tenant_id = ? AND c.knowledge_base_id = ? AND c.knowledge_id = b.knowledge_id", tenantID, kbID).
			Where("c.chunk_type IN ?", []string{types.ChunkTypeText, types.ChunkTypeImageOCR, types.ChunkTypeImageCaption}).
			Distinct("b.knowledge_id").Pluck("b.knowledge_id", &knowledgeIDs).Error; err != nil {
			return err
		}
		if len(knowledgeIDs) == 0 {
			return nil
		}
		now := time.Now()
		relations := make([]types.KnowledgeTagRelation, 0, len(knowledgeIDs))
		for _, knowledgeID := range knowledgeIDs {
			relations = append(relations, types.KnowledgeTagRelation{
				KnowledgeID: knowledgeID, TagID: id, CreatedAt: now,
			})
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&relations).Error
	})
}

var _ interfaces.KnowledgeTagLifecycleRepository = (*knowledgeTagLifecycleRepository)(nil)
