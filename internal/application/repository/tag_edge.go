package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/taggraph"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type knowledgeTagEdgeRepository struct {
	db *gorm.DB
}

// NewKnowledgeTagEdgeRepository creates a repository for tag-to-tag edges.
func NewKnowledgeTagEdgeRepository(db *gorm.DB) interfaces.KnowledgeTagEdgeRepository {
	return &knowledgeTagEdgeRepository{db: db}
}

// Create validates endpoints and inserts an edge. IS_A insertion locks the
// owning KB row on PostgreSQL so concurrent requests cannot race the cycle
// check. Other stores are still protected by the unique constraint and the
// same graph validation.
func (r *knowledgeTagEdgeRepository) Create(ctx context.Context, edge *types.KnowledgeTagEdge) error {
	if edge == nil || !taggraph.ValidRelationType(edge.RelationType) {
		return taggraph.ErrInvalidRelation
	}
	if edge.FromTagID == "" || edge.ToTagID == "" || edge.FromTagID == edge.ToTagID {
		return taggraph.ErrCycle
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if edge.RelationType == taggraph.RelationIsA && tx.Dialector.Name() == "postgres" {
			var kbID string
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Table("knowledge_bases").Select("id").
				Where("tenant_id = ? AND id = ?", edge.TenantID, edge.KnowledgeBaseID).
				Take(&kbID).Error; err != nil {
				return err
			}
		}

		var endpointCount int64
		if err := tx.Model(&types.KnowledgeTag{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", edge.TenantID, edge.KnowledgeBaseID, []string{edge.FromTagID, edge.ToTagID}).
			Count(&endpointCount).Error; err != nil {
			return err
		}
		if endpointCount != 2 {
			return taggraph.ErrTagEndpointMissing
		}

		if edge.RelationType == taggraph.RelationIsA {
			var stored []types.KnowledgeTagEdge
			if err := tx.Select("from_tag_id", "to_tag_id").
				Where("tenant_id = ? AND knowledge_base_id = ? AND relation_type = ?", edge.TenantID, edge.KnowledgeBaseID, taggraph.RelationIsA).
				Find(&stored).Error; err != nil {
				return err
			}
			graphEdges := make([]taggraph.Edge, 0, len(stored))
			for _, existing := range stored {
				graphEdges = append(graphEdges, taggraph.Edge{FromTagID: existing.FromTagID, ToTagID: existing.ToTagID})
			}
			if taggraph.WouldCreateCycle(graphEdges, edge.FromTagID, edge.ToTagID) {
				return taggraph.ErrCycle
			}
		}

		result := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "knowledge_base_id"},
				{Name: "from_tag_id"},
				{Name: "to_tag_id"},
				{Name: "relation_type"},
			},
			DoNothing: true,
		}).Create(edge)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existing types.KnowledgeTagEdge
			if err := tx.Where("tenant_id = ? AND knowledge_base_id = ? AND from_tag_id = ? AND to_tag_id = ? AND relation_type = ?",
				edge.TenantID, edge.KnowledgeBaseID, edge.FromTagID, edge.ToTagID, edge.RelationType).
				First(&existing).Error; err != nil {
				return err
			}
			*edge = existing
		}
		return nil
	})
}

func (r *knowledgeTagEdgeRepository) ListByKB(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagID string,
) ([]*types.KnowledgeTagEdge, error) {
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	if tagID != "" {
		query = query.Where("(from_tag_id = ? OR to_tag_id = ?)", tagID, tagID)
	}
	var edges []*types.KnowledgeTagEdge
	if err := query.Order("relation_type ASC, from_tag_id ASC, to_tag_id ASC").Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

func (r *knowledgeTagEdgeRepository) GetByID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	id string,
) (*types.KnowledgeTagEdge, error) {
	var edge types.KnowledgeTagEdge
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, id).
		First(&edge).Error; err != nil {
		return nil, err
	}
	return &edge, nil
}

func (r *knowledgeTagEdgeRepository) Delete(ctx context.Context, tenantID uint64, kbID string, id string) error {
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, id).
		Delete(&types.KnowledgeTagEdge{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

var _ interfaces.KnowledgeTagEdgeRepository = (*knowledgeTagEdgeRepository)(nil)
