package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type knowledgeTagWikiStateRepository struct {
	db *gorm.DB
}

func NewKnowledgeTagWikiStateRepository(db *gorm.DB) interfaces.KnowledgeTagWikiStateRepository {
	return &knowledgeTagWikiStateRepository{db: db}
}

func (r *knowledgeTagWikiStateRepository) MarkPending(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagID string,
) (*types.KnowledgeTagWikiState, error) {
	var state *types.KnowledgeTagWikiState
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var activeTagCount int64
		if err := tx.Model(&types.KnowledgeTag{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id = ? AND status = ?",
				tenantID, kbID, tagID, types.KnowledgeTagStatusActive).
			Count(&activeTagCount).Error; err != nil {
			return err
		}
		if activeTagCount != 1 {
			return gorm.ErrRecordNotFound
		}

		now := time.Now()
		state = &types.KnowledgeTagWikiState{
			TagID: tagID, TenantID: tenantID, KnowledgeBaseID: kbID,
			Status: types.KnowledgeTagWikiStatusPending, LockVersion: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tag_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"tenant_id":         tenantID,
				"knowledge_base_id": kbID,
				"status":            types.KnowledgeTagWikiStatusPending,
				"last_error":        "",
				"lock_version":      gorm.Expr("tag_wiki_states.lock_version + 1"),
				"updated_at":        now,
			}),
		}).Create(state).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND knowledge_base_id = ? AND tag_id = ?", tenantID, kbID, tagID).
			First(state).Error
	})
	return state, err
}

func (r *knowledgeTagWikiStateRepository) Get(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagID string,
) (*types.KnowledgeTagWikiState, error) {
	var state types.KnowledgeTagWikiState
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND tag_id = ?", tenantID, kbID, tagID).
		First(&state).Error; err != nil {
		return nil, err
	}
	return &state, nil
}

func (r *knowledgeTagWikiStateRepository) Claim(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagID string,
	lockVersion uint64,
) (bool, error) {
	result := r.db.WithContext(ctx).Model(&types.KnowledgeTagWikiState{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND tag_id = ? AND status IN ? AND lock_version = ?",
			tenantID, kbID, tagID, []string{types.KnowledgeTagWikiStatusPending, types.KnowledgeTagWikiStatusFailed}, lockVersion).
		Updates(map[string]interface{}{"status": types.KnowledgeTagWikiStatusProcessing, "updated_at": time.Now()})
	return result.RowsAffected == 1, result.Error
}

func (r *knowledgeTagWikiStateRepository) MarkReady(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagID string,
	lockVersion uint64,
	pageID string,
	sourceHash string,
	contentHash string,
) (bool, error) {
	result := r.db.WithContext(ctx).Model(&types.KnowledgeTagWikiState{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND tag_id = ? AND status = ? AND lock_version = ?",
			tenantID, kbID, tagID, types.KnowledgeTagWikiStatusProcessing, lockVersion).
		Updates(map[string]interface{}{
			"wiki_page_id": pageID,
			"source_hash":  sourceHash,
			"content_hash": contentHash,
			"status":       "ready",
			"last_error":   "",
			"updated_at":   time.Now(),
		})
	return result.RowsAffected == 1, result.Error
}

func (r *knowledgeTagWikiStateRepository) MarkFailed(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagID string,
	lockVersion uint64,
	lastError string,
) error {
	return r.db.WithContext(ctx).Model(&types.KnowledgeTagWikiState{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND tag_id = ? AND status = ? AND lock_version = ?",
			tenantID, kbID, tagID, types.KnowledgeTagWikiStatusProcessing, lockVersion).
		Updates(map[string]interface{}{
			"status":     types.KnowledgeTagWikiStatusFailed,
			"last_error": lastError,
			"updated_at": time.Now(),
		}).Error
}

func (r *knowledgeTagWikiStateRepository) ListRecoverable(ctx context.Context) ([]*types.KnowledgeTagWikiState, error) {
	var states []*types.KnowledgeTagWikiState
	err := r.db.WithContext(ctx).
		Where("status IN ?", []string{
			types.KnowledgeTagWikiStatusPending,
			types.KnowledgeTagWikiStatusProcessing,
			types.KnowledgeTagWikiStatusFailed,
		}).
		Order("updated_at ASC, tag_id ASC").
		Find(&states).Error
	return states, err
}

var _ interfaces.KnowledgeTagWikiStateRepository = (*knowledgeTagWikiStateRepository)(nil)
