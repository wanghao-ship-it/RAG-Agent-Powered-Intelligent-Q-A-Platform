package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/taggraph"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newKnowledgeTagEdgeTestRepo(t *testing.T) *knowledgeTagEdgeRepository {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeTag{}, &types.KnowledgeTagEdge{}))
	for _, ddl := range []string{
		`CREATE TABLE knowledges (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, knowledge_base_id TEXT NOT NULL, parse_status TEXT NOT NULL, deleted_at DATETIME)`,
		`CREATE TABLE chunks (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, knowledge_base_id TEXT NOT NULL, knowledge_id TEXT NOT NULL, chunk_type TEXT NOT NULL)`,
		`CREATE TABLE knowledge_tag_bindings (knowledge_id TEXT NOT NULL, tag_id TEXT NOT NULL, tenant_id INTEGER NOT NULL, knowledge_base_id TEXT NOT NULL, chunk_id TEXT NOT NULL, confidence REAL NOT NULL, evidence TEXT NOT NULL, extractor_version TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, PRIMARY KEY (knowledge_id, tag_id))`,
		`CREATE TABLE knowledge_tag_relations (knowledge_id TEXT NOT NULL, tag_id TEXT NOT NULL, created_at DATETIME NOT NULL, PRIMARY KEY (knowledge_id, tag_id))`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return &knowledgeTagEdgeRepository{db: db}
}

func TestKnowledgeTagEdgeRepositoryRejectsISACyclesAndDeduplicates(t *testing.T) {
	repo := newKnowledgeTagEdgeTestRepo(t)
	ctx := context.Background()
	for i, tagID := range []string{"tag-a", "tag-b", "tag-c"} {
		require.NoError(t, repo.db.Create(&types.KnowledgeTag{
			ID: tagID, TenantID: 7, KnowledgeBaseID: "kb-1", Name: tagID, SeqID: int64(i + 1),
		}).Error)
	}

	first := &types.KnowledgeTagEdge{
		ID: "edge-ab", TenantID: 7, KnowledgeBaseID: "kb-1", FromTagID: "tag-a", ToTagID: "tag-b",
		RelationType: taggraph.RelationIsA, Confidence: 0.9,
	}
	require.NoError(t, repo.Create(ctx, first))

	duplicate := &types.KnowledgeTagEdge{
		ID: "edge-ab-duplicate", TenantID: 7, KnowledgeBaseID: "kb-1", FromTagID: "tag-a", ToTagID: "tag-b",
		RelationType: taggraph.RelationIsA, Confidence: 0.9,
	}
	require.NoError(t, repo.Create(ctx, duplicate))
	require.Equal(t, first.ID, duplicate.ID, "a repeated edge should resolve to the persisted row")

	require.NoError(t, repo.Create(ctx, &types.KnowledgeTagEdge{
		ID: "edge-bc", TenantID: 7, KnowledgeBaseID: "kb-1", FromTagID: "tag-b", ToTagID: "tag-c",
		RelationType: taggraph.RelationIsA, Confidence: 1,
	}))
	cycleErr := repo.Create(ctx, &types.KnowledgeTagEdge{
		ID: "edge-ca", TenantID: 7, KnowledgeBaseID: "kb-1", FromTagID: "tag-c", ToTagID: "tag-a",
		RelationType: taggraph.RelationIsA, Confidence: 1,
	})
	require.ErrorIs(t, cycleErr, taggraph.ErrCycle)

	listed, err := repo.ListByKB(ctx, 7, "kb-1", "tag-b")
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.NoError(t, repo.Delete(ctx, 7, "kb-1", first.ID))
	_, err = repo.GetByID(ctx, 7, "kb-1", first.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestKnowledgeTagEdgeRepositoryRejectsCrossKBTenantEndpoints(t *testing.T) {
	repo := newKnowledgeTagEdgeTestRepo(t)
	require.NoError(t, repo.db.Create(&types.KnowledgeTag{
		ID: "tag-other-kb", TenantID: 8, KnowledgeBaseID: "kb-other", Name: "other",
	}).Error)
	err := repo.Create(context.Background(), &types.KnowledgeTagEdge{
		ID: "edge-cross", TenantID: 7, KnowledgeBaseID: "kb-1", FromTagID: "tag-other-kb", ToTagID: "tag-missing",
		RelationType: taggraph.RelationRelatedTo, Confidence: 1,
	})
	require.ErrorIs(t, err, taggraph.ErrTagEndpointMissing)
}

func TestKnowledgeTagLifecycleRepositoryDeduplicatesAndReviewsCandidates(t *testing.T) {
	edgeRepo := newKnowledgeTagEdgeTestRepo(t)
	repo := NewKnowledgeTagLifecycleRepository(edgeRepo.db)
	ctx := context.Background()

	candidate := &types.KnowledgeTag{
		ID: "candidate-1", TenantID: 7, KnowledgeBaseID: "kb-1", Name: "Machine Learning",
		Description: "models that learn from data", Confidence: 0.72,
	}
	require.NoError(t, repo.CreateCandidate(ctx, candidate))
	require.Equal(t, types.KnowledgeTagStatusPending, candidate.Status)
	require.Equal(t, types.KnowledgeTagSourceAuto, candidate.Source)
	require.Equal(t, "machine learning", candidate.NormalizedName)

	duplicate := &types.KnowledgeTag{
		ID: "candidate-duplicate", TenantID: 7, KnowledgeBaseID: "kb-1", Name: "  machine learning ",
		Confidence: 0.91,
	}
	require.NoError(t, repo.CreateCandidate(ctx, duplicate))
	require.Equal(t, candidate.ID, duplicate.ID, "normalized duplicate candidates reuse the first row")
	require.Equal(t, 0.72, duplicate.Confidence, "duplicate ingestion does not overwrite the existing evidence score")

	pending, err := repo.ListByStatus(ctx, 7, "kb-1", types.KnowledgeTagStatusPending)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.NoError(t, repo.SetStatus(ctx, 7, "kb-1", candidate.ID, types.KnowledgeTagStatusActive))
	require.NoError(t, repo.SetStatus(ctx, 7, "kb-1", candidate.ID, types.KnowledgeTagStatusActive), "same-state review is idempotent")
	require.ErrorIs(t, repo.SetStatus(ctx, 7, "kb-1", candidate.ID, types.KnowledgeTagStatusDeprecated), taggraph.ErrTagCandidateNotPending)

	active, err := repo.GetByID(ctx, 7, "kb-1", candidate.ID)
	require.NoError(t, err)
	require.Equal(t, types.KnowledgeTagStatusActive, active.Status)
	pending, err = repo.ListByStatus(ctx, 7, "kb-1", types.KnowledgeTagStatusPending)
	require.NoError(t, err)
	require.Empty(t, pending)
}

func TestKnowledgeTagLifecycleApprovalAttachesOnlyLiveEvidenceDocuments(t *testing.T) {
	edgeRepo := newKnowledgeTagEdgeTestRepo(t)
	db := edgeRepo.db
	ctx := context.Background()
	repo := NewKnowledgeTagLifecycleRepository(db)
	candidate := &types.KnowledgeTag{
		ID: "candidate-evidence", TenantID: 7, KnowledgeBaseID: "kb-1", Name: "Safety Review",
		Confidence: 0.91,
	}
	require.NoError(t, repo.CreateCandidate(ctx, candidate))

	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, parse_status) VALUES
			('live-doc', 7, 'kb-1', 'completed'), ('cancelled-doc', 7, 'kb-1', 'cancelled')
	`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO chunks (id, tenant_id, knowledge_base_id, knowledge_id, chunk_type) VALUES
			('live-chunk', 7, 'kb-1', 'live-doc', 'text'), ('cancelled-chunk', 7, 'kb-1', 'cancelled-doc', 'text')
	`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tag_bindings
			(knowledge_id, tag_id, tenant_id, knowledge_base_id, chunk_id, confidence, evidence, extractor_version, created_at, updated_at)
		VALUES
			('live-doc', ?, 7, 'kb-1', 'live-chunk', 0.91, 'safety review', 'auto-tag-v1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('cancelled-doc', ?, 7, 'kb-1', 'cancelled-chunk', 0.92, 'safety review', 'auto-tag-v1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, candidate.ID, candidate.ID).Error)

	bindings, err := repo.ListBindingsByTag(ctx, 7, "kb-1", candidate.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	require.NoError(t, repo.SetStatus(ctx, 7, "kb-1", candidate.ID, types.KnowledgeTagStatusActive))

	var linkedKnowledgeIDs []string
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("tag_id = ?", candidate.ID).Pluck("knowledge_id", &linkedKnowledgeIDs).Error)
	require.Equal(t, []string{"live-doc"}, linkedKnowledgeIDs)
}
