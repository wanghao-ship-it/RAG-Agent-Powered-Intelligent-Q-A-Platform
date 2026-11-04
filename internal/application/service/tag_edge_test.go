package service

import (
	"context"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/taggraph"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type tagEdgeTestKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s tagEdgeTestKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if id != s.kb.ID {
		return nil, gorm.ErrRecordNotFound
	}
	return s.kb, nil
}

type tagEdgeTestShareService struct{ interfaces.KBShareService }

type tagEdgeTestAuditService struct {
	interfaces.AuditLogService
	entries []*types.AuditLog
}

func (s *tagEdgeTestAuditService) Log(_ context.Context, entry *types.AuditLog) error {
	s.entries = append(s.entries, entry)
	return nil
}

func newTagEdgeServiceTest(t *testing.T) (interfaces.KnowledgeTagEdgeService, *gorm.DB, context.Context, *tagEdgeTestAuditService) {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeTag{}, &types.KnowledgeTagEdge{}))
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 7}
	caller := types.Caller{TenantID: 7, UserID: "user-1", Role: types.TenantRoleOwner}
	ctx := types.WithCaller(context.Background(), caller)
	grant, err := access.ResolveKB(ctx, access.KBRequest{Caller: caller}, kb, types.OrgRoleEditor, nil, nil)
	require.NoError(t, err)
	ctx = grant.Context(ctx)

	audit := &tagEdgeTestAuditService{}
	service, err := NewKnowledgeTagEdgeService(
		tagEdgeTestKBService{kb: kb},
		repository.NewKnowledgeTagEdgeRepository(db),
		repository.NewKnowledgeTagRepository(db),
		tagEdgeTestShareService{},
		audit,
	)
	require.NoError(t, err)
	return service, db, ctx, audit
}

func TestKnowledgeTagEdgeServiceCreatesListsAndDeletesRelations(t *testing.T) {
	service, db, ctx, audit := newTagEdgeServiceTest(t)
	for i, tagID := range []string{"child", "parent", "root"} {
		require.NoError(t, db.Create(&types.KnowledgeTag{
			ID: tagID, TenantID: 7, KnowledgeBaseID: "kb-1", Name: tagID, SeqID: int64(i + 1),
		}).Error)
	}

	edge, err := service.CreateRelation(ctx, "kb-1", "child", "parent", " is_a ", 0.85)
	require.NoError(t, err)
	require.Equal(t, taggraph.RelationIsA, edge.RelationType)
	require.Equal(t, 0.85, edge.Confidence)

	_, err = service.CreateRelation(ctx, "kb-1", "parent", "root", taggraph.RelationIsA, 1)
	require.NoError(t, err)
	_, err = service.CreateRelation(ctx, "kb-1", "root", "child", taggraph.RelationIsA, 1)
	require.ErrorContains(t, err, "IS_A 关系会形成环路")
	_, err = service.CreateRelation(ctx, "kb-1", "child", "parent", "UNKNOWN", 1)
	require.Error(t, err)
	_, err = service.CreateRelation(ctx, "kb-1", "child", "parent", taggraph.RelationRelatedTo, math.NaN())
	require.Error(t, err)

	relations, err := service.ListRelations(ctx, "kb-1", "parent")
	require.NoError(t, err)
	require.Len(t, relations, 2)
	for _, relation := range relations {
		require.True(t, relation.FromTagID == "child" || relation.FromTagID == "parent")
	}

	require.NoError(t, service.DeleteRelation(ctx, "kb-1", edge.ID))
	_, err = service.ListRelations(ctx, "kb-1", "child")
	require.NoError(t, err)
	remaining, err := service.ListRelations(ctx, "kb-1", "")
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	require.Len(t, audit.entries, 3, "successful creates and delete are audited")
}

func TestKnowledgeTagEdgeServiceRejectsWriteWithoutEditorGrant(t *testing.T) {
	service, db, _, _ := newTagEdgeServiceTest(t)
	require.NoError(t, db.Create(&types.KnowledgeTag{
		ID: "tag-a", TenantID: 7, KnowledgeBaseID: "kb-1", Name: "a", SeqID: 1,
	}).Error)
	require.NoError(t, db.Create(&types.KnowledgeTag{
		ID: "tag-b", TenantID: 7, KnowledgeBaseID: "kb-1", Name: "b", SeqID: 2,
	}).Error)

	caller := types.Caller{TenantID: 7, UserID: "reader-1", Role: types.TenantRoleViewer}
	ctx := types.WithCaller(context.Background(), caller)
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 7}
	grant, err := access.ResolveKB(ctx, access.KBRequest{Caller: caller}, kb, types.OrgRoleViewer, nil, nil)
	require.NoError(t, err)
	_, err = service.CreateRelation(grant.Context(ctx), "kb-1", "tag-a", "tag-b", taggraph.RelationRelatedTo, 1)
	require.Error(t, err)
}
