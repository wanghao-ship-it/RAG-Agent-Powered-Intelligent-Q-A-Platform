package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTagLifecycleServiceTest(t *testing.T) (interfaces.KnowledgeTagLifecycleService, *gorm.DB, context.Context, *tagEdgeTestAuditService) {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeTag{}))
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

	kb := &types.KnowledgeBase{ID: "kb-life", TenantID: 7}
	caller := types.Caller{TenantID: 7, UserID: "owner-1", Role: types.TenantRoleOwner}
	ctx := types.WithCaller(context.Background(), caller)
	grant, err := access.ResolveKB(ctx, access.KBRequest{Caller: caller}, kb, types.OrgRoleEditor, nil, nil)
	require.NoError(t, err)
	ctx = grant.Context(ctx)
	audit := &tagEdgeTestAuditService{}
	service, err := NewKnowledgeTagLifecycleService(
		tagEdgeTestKBService{kb: kb},
		repository.NewKnowledgeTagLifecycleRepository(db),
		tagEdgeTestShareService{},
		audit,
	)
	require.NoError(t, err)
	return service, db, ctx, audit
}

func TestKnowledgeTagLifecycleServiceDeduplicatesAndReviewsCandidates(t *testing.T) {
	service, _, ctx, audit := newTagLifecycleServiceTest(t)

	candidate, err := service.CreateCandidate(ctx, "kb-life", " Operations Guide ", "automatic candidate", 0.84)
	require.NoError(t, err)
	require.Equal(t, types.KnowledgeTagStatusPending, candidate.Status)
	require.Equal(t, types.KnowledgeTagSourceAuto, candidate.Source)
	require.Equal(t, "operations guide", candidate.NormalizedName)
	require.Equal(t, 0.84, candidate.Confidence)

	duplicate, err := service.CreateCandidate(ctx, "kb-life", " operations guide ", "different description", 0.9)
	require.NoError(t, err)
	require.Equal(t, candidate.ID, duplicate.ID)
	require.Equal(t, 0.84, duplicate.Confidence)

	pending, err := service.ListPending(ctx, "kb-life")
	require.NoError(t, err)
	require.Len(t, pending, 1)

	approved, err := service.ReviewCandidate(ctx, "kb-life", candidate.ID, "approve")
	require.NoError(t, err)
	require.Equal(t, types.KnowledgeTagStatusActive, approved.Status)
	_, err = service.ReviewCandidate(ctx, "kb-life", candidate.ID, "approve")
	require.NoError(t, err, "repeating the same review is idempotent")
	_, err = service.ReviewCandidate(ctx, "kb-life", candidate.ID, "reject")
	require.ErrorContains(t, err, "只有 pending 标签可以审核")

	pending, err = service.ListPending(ctx, "kb-life")
	require.NoError(t, err)
	require.Empty(t, pending)

	rejectedCandidate, err := service.CreateCandidate(ctx, "kb-life", "Legacy System", "", 0.3)
	require.NoError(t, err)
	rejected, err := service.ReviewCandidate(ctx, "kb-life", rejectedCandidate.ID, "reject")
	require.NoError(t, err)
	require.Equal(t, types.KnowledgeTagStatusDeprecated, rejected.Status)

	_, err = service.CreateCandidate(ctx, "kb-life", "Invalid Confidence", "", 1.01)
	require.Error(t, err)
	_, err = service.ReviewCandidate(ctx, "kb-life", rejectedCandidate.ID, "unknown")
	require.Error(t, err)
	require.Len(t, audit.entries, 4, "only candidate creations and state changes are audited")
}

func TestKnowledgeTagLifecycleServiceListsEvidenceOnlyForPendingCandidates(t *testing.T) {
	service, db, ctx, _ := newTagLifecycleServiceTest(t)
	candidate, err := service.CreateCandidate(ctx, "kb-life", "Safety Controls", "", 0.88)
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tag_bindings
			(knowledge_id, tag_id, tenant_id, knowledge_base_id, chunk_id, confidence, evidence, extractor_version, created_at, updated_at)
		VALUES ('doc-1', ?, 7, 'kb-life', 'chunk-1', 0.88, 'safety controls', 'auto-tag-v1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, candidate.ID).Error)

	evidence, err := service.ListCandidateEvidence(ctx, "kb-life", candidate.ID)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	require.Equal(t, "doc-1", evidence[0].KnowledgeID)
	require.Equal(t, "safety controls", evidence[0].Evidence)

	_, err = service.ReviewCandidate(ctx, "kb-life", candidate.ID, "approve")
	require.NoError(t, err)
	_, err = service.ListCandidateEvidence(ctx, "kb-life", candidate.ID)
	require.ErrorContains(t, err, "只有 pending 标签候选可以查看待审核证据")
}

func TestKnowledgeTagLifecycleServiceRequiresEditorGrantForReview(t *testing.T) {
	service, _, _, _ := newTagLifecycleServiceTest(t)
	kb := &types.KnowledgeBase{ID: "kb-life", TenantID: 7}
	reader := types.Caller{TenantID: 7, UserID: "reader-1", Role: types.TenantRoleViewer}
	ctx := types.WithCaller(context.Background(), reader)
	grant, err := access.ResolveKB(ctx, access.KBRequest{Caller: reader}, kb, types.OrgRoleViewer, nil, nil)
	require.NoError(t, err)

	_, err = service.CreateCandidate(grant.Context(ctx), "kb-life", "Unauthorized", "", 0.8)
	require.Error(t, err)
	_, err = service.ReviewCandidate(grant.Context(ctx), "kb-life", "candidate", "approve")
	require.Error(t, err)
}
