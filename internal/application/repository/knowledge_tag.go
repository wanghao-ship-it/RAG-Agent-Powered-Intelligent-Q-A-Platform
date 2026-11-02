package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SetKnowledgeTags replaces all tags for a single knowledge entry.
// It deletes existing relations and inserts new ones in a transaction.
func (r *knowledgeRepository) SetKnowledgeTags(
	ctx context.Context,
	knowledgeID string,
	tagIDs []string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete all existing tag relations for this knowledge
		if err := tx.Where("knowledge_id = ?", knowledgeID).
			Delete(&types.KnowledgeTagRelation{}).Error; err != nil {
			return err
		}
		if len(tagIDs) == 0 {
			return tx.Where("knowledge_id = ?", knowledgeID).
				Delete(&types.KnowledgeTagBinding{}).Error
		}
		if err := tx.Where("knowledge_id = ? AND tag_id NOT IN ?", knowledgeID, tagIDs).
			Delete(&types.KnowledgeTagBinding{}).Error; err != nil {
			return err
		}
		// Insert new relations (skip empty and duplicate IDs)
		seen := make(map[string]struct{}, len(tagIDs))
		now := time.Now()
		relations := make([]types.KnowledgeTagRelation, 0, len(tagIDs))
		for _, tagID := range tagIDs {
			if tagID == "" {
				continue
			}
			if _, dup := seen[tagID]; dup {
				continue
			}
			seen[tagID] = struct{}{}
			relations = append(relations, types.KnowledgeTagRelation{
				KnowledgeID: knowledgeID,
				TagID:       tagID,
				CreatedAt:   now,
			})
		}
		if len(relations) == 0 {
			return nil
		}
		return tx.Create(&relations).Error
	})
}

// AddKnowledgeTagRelations incrementally adds validated relations and never
// removes existing manual tags. The composite primary key plus DO NOTHING
// makes retries and duplicate deliveries idempotent.
func (r *knowledgeRepository) AddKnowledgeTagRelations(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
	tagIDs []string,
) error {
	seen := make(map[string]struct{}, len(tagIDs))
	unique := make([]string, 0, len(tagIDs))
	for _, id := range tagIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var knowledgeCount int64
		if err := tx.Model(&types.Knowledge{}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL", knowledgeID, tenantID, kbID).
			Where("parse_status NOT IN ?", []string{
				types.ParseStatusCancelled,
				types.ParseStatusDeleting,
				types.ParseStatusFailed,
			}).
			Count(&knowledgeCount).Error; err != nil {
			return err
		}
		if knowledgeCount != 1 {
			return fmt.Errorf("knowledge %s does not belong to tenant %d and knowledge base %s", knowledgeID, tenantID, kbID)
		}

		var tagCount int64
		if err := tx.Model(&types.KnowledgeTag{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, unique).
			Count(&tagCount).Error; err != nil {
			return err
		}
		if tagCount != int64(len(unique)) {
			return fmt.Errorf("one or more tags do not belong to tenant %d and knowledge base %s", tenantID, kbID)
		}

		now := time.Now()
		relations := make([]types.KnowledgeTagRelation, 0, len(unique))
		for _, tagID := range unique {
			relations = append(relations, types.KnowledgeTagRelation{
				KnowledgeID: knowledgeID,
				TagID:       tagID,
				CreatedAt:   now,
			})
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&relations).Error
	})
}

// PersistKnowledgeTagClassification atomically attaches approved active tags
// and stores source evidence for both active matches and pending candidates.
// Pending candidates are not attached to the document until human approval.
func (r *knowledgeRepository) PersistKnowledgeTagClassification(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
	activeBindings []types.KnowledgeTagBinding,
	candidateBindings []types.KnowledgeTagBinding,
) error {
	if len(activeBindings) == 0 && len(candidateBindings) == 0 {
		return nil
	}

	activeByTag := make(map[string]types.KnowledgeTagBinding, len(activeBindings))
	candidateByTag := make(map[string]types.KnowledgeTagBinding, len(candidateBindings))
	chunkIDs := make(map[string]struct{}, len(activeBindings)+len(candidateBindings))
	prepare := func(bindings []types.KnowledgeTagBinding, destination map[string]types.KnowledgeTagBinding) error {
		for _, binding := range bindings {
			if binding.TagID == "" || binding.ChunkID == "" ||
				strings.TrimSpace(binding.Evidence) == "" ||
				strings.TrimSpace(binding.ExtractorVersion) == "" ||
				binding.Confidence < 0 || binding.Confidence > 1 {
				return fmt.Errorf("invalid automatic tag evidence binding")
			}
			if _, exists := destination[binding.TagID]; exists {
				continue
			}
			binding.TenantID = tenantID
			binding.KnowledgeBaseID = kbID
			binding.KnowledgeID = knowledgeID
			destination[binding.TagID] = binding
			chunkIDs[binding.ChunkID] = struct{}{}
		}
		return nil
	}
	if err := prepare(activeBindings, activeByTag); err != nil {
		return err
	}
	if err := prepare(candidateBindings, candidateByTag); err != nil {
		return err
	}
	for tagID := range activeByTag {
		if _, duplicate := candidateByTag[tagID]; duplicate {
			return fmt.Errorf("tag %s cannot be both active and pending in one classification", tagID)
		}
	}
	if len(activeByTag) == 0 && len(candidateByTag) == 0 {
		return nil
	}

	activeTagIDs := make([]string, 0, len(activeByTag))
	for tagID := range activeByTag {
		activeTagIDs = append(activeTagIDs, tagID)
	}
	candidateTagIDs := make([]string, 0, len(candidateByTag))
	for tagID := range candidateByTag {
		candidateTagIDs = append(candidateTagIDs, tagID)
	}
	chunkIDList := make([]string, 0, len(chunkIDs))
	for chunkID := range chunkIDs {
		chunkIDList = append(chunkIDList, chunkID)
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var knowledgeCount int64
		if err := tx.Model(&types.Knowledge{}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL", knowledgeID, tenantID, kbID).
			Where("parse_status NOT IN ?", []string{
				types.ParseStatusCancelled,
				types.ParseStatusDeleting,
				types.ParseStatusFailed,
			}).
			Count(&knowledgeCount).Error; err != nil {
			return err
		}
		if knowledgeCount != 1 {
			return fmt.Errorf("knowledge %s does not belong to tenant %d and knowledge base %s", knowledgeID, tenantID, kbID)
		}

		for _, scope := range []struct {
			ids    []string
			status string
		}{{activeTagIDs, types.KnowledgeTagStatusActive}, {candidateTagIDs, types.KnowledgeTagStatusPending}} {
			if len(scope.ids) == 0 {
				continue
			}
			var tagCount int64
			if err := tx.Model(&types.KnowledgeTag{}).
				Where("tenant_id = ? AND knowledge_base_id = ? AND status = ? AND id IN ?",
					tenantID, kbID, scope.status, scope.ids).
				Count(&tagCount).Error; err != nil {
				return err
			}
			if tagCount != int64(len(scope.ids)) {
				return fmt.Errorf("one or more tags are not %s in tenant %d and knowledge base %s",
					scope.status, tenantID, kbID)
			}
		}

		var chunkCount int64
		if err := tx.Model(&types.Chunk{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND id IN ?",
				tenantID, kbID, knowledgeID, chunkIDList).
			Where("chunk_type IN ?", []string{types.ChunkTypeText, types.ChunkTypeImageOCR, types.ChunkTypeImageCaption}).
			Count(&chunkCount).Error; err != nil {
			return err
		}
		if chunkCount != int64(len(chunkIDList)) {
			return fmt.Errorf("one or more evidence chunks do not belong to knowledge %s", knowledgeID)
		}

		now := time.Now()
		relations := make([]types.KnowledgeTagRelation, 0, len(activeTagIDs))
		rows := make([]types.KnowledgeTagBinding, 0, len(activeTagIDs)+len(candidateTagIDs))
		for _, tagID := range activeTagIDs {
			relations = append(relations, types.KnowledgeTagRelation{
				KnowledgeID: knowledgeID,
				TagID:       tagID,
				CreatedAt:   now,
			})
			binding := activeByTag[tagID]
			binding.CreatedAt = now
			binding.UpdatedAt = now
			rows = append(rows, binding)
		}
		for _, tagID := range candidateTagIDs {
			binding := candidateByTag[tagID]
			binding.CreatedAt = now
			binding.UpdatedAt = now
			rows = append(rows, binding)
		}
		if len(relations) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&relations).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "knowledge_id"}, {Name: "tag_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"tenant_id", "knowledge_base_id", "chunk_id", "confidence", "evidence", "extractor_version", "updated_at",
			}),
		}).Create(&rows).Error
	})
}

// GetKnowledgeTags returns tags for multiple knowledge IDs.
// The result is a map from knowledge ID to its tag list.
func (r *knowledgeRepository) GetKnowledgeTags(
	ctx context.Context,
	knowledgeIDs []string,
) (map[string][]*types.KnowledgeTag, error) {
	result := make(map[string][]*types.KnowledgeTag)
	if len(knowledgeIDs) == 0 {
		return result, nil
	}

	// Query relations and join with knowledge_tags to get full tag info
	type relationWithTag struct {
		KnowledgeID string `gorm:"column:knowledge_id"`
		types.KnowledgeTag
	}
	var rows []relationWithTag
	if err := r.db.WithContext(ctx).
		Table("knowledge_tag_relations AS ktr").
		Select("ktr.knowledge_id, kt.id, kt.seq_id, kt.tenant_id, kt.knowledge_base_id, kt.name, kt.color, kt.sort_order, kt.created_at, kt.updated_at").
		Joins("JOIN knowledge_tags AS kt ON ktr.tag_id = kt.id").
		Where("ktr.knowledge_id IN (?)", knowledgeIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	for _, row := range rows {
		tag := row.KnowledgeTag
		result[row.KnowledgeID] = append(result[row.KnowledgeID], &tag)
	}
	return result, nil
}

// DeleteKnowledgeTagRelations deletes all tag relations for a knowledge entry.
func (r *knowledgeRepository) DeleteKnowledgeTagRelations(
	ctx context.Context,
	knowledgeID string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("knowledge_id = ?", knowledgeID).
			Delete(&types.KnowledgeTagRelation{}).Error; err != nil {
			return err
		}
		return tx.Where("knowledge_id = ?", knowledgeID).
			Delete(&types.KnowledgeTagBinding{}).Error
	})
}
