package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeTagWikiStateRepositoryUsesLockVersionForStaleWorkers(t *testing.T) {
	testRepo := newKnowledgeTagEdgeTestRepo(t)
	db := testRepo.db
	require.NoError(t, db.AutoMigrate(&types.KnowledgeTagWikiState{}))
	require.NoError(t, db.Create(&types.KnowledgeTag{
		ID: "active-tag", TenantID: 7, KnowledgeBaseID: "kb-1", Name: "Safety",
		Status: types.KnowledgeTagStatusActive,
	}).Error)
	repo := NewKnowledgeTagWikiStateRepository(db)
	ctx := context.Background()

	first, err := repo.MarkPending(ctx, 7, "kb-1", "active-tag")
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.LockVersion)
	claimed, err := repo.Claim(ctx, 7, "kb-1", "active-tag", first.LockVersion)
	require.NoError(t, err)
	require.True(t, claimed)

	// A new source event invalidates the worker that claimed the older version.
	second, err := repo.MarkPending(ctx, 7, "kb-1", "active-tag")
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.LockVersion)
	ready, err := repo.MarkReady(ctx, 7, "kb-1", "active-tag", first.LockVersion, "page-1", "source-1", "content-1")
	require.NoError(t, err)
	require.False(t, ready, "stale worker must not publish over the newer pending source version")
	claimed, err = repo.Claim(ctx, 7, "kb-1", "active-tag", second.LockVersion)
	require.NoError(t, err)
	require.True(t, claimed)
	ready, err = repo.MarkReady(ctx, 7, "kb-1", "active-tag", second.LockVersion, "page-1", "source-2", "content-2")
	require.NoError(t, err)
	require.True(t, ready)

	stored, err := repo.Get(ctx, 7, "kb-1", "active-tag")
	require.NoError(t, err)
	require.Equal(t, "ready", stored.Status)
	require.Equal(t, "source-2", stored.SourceHash)
	require.Equal(t, "content-2", stored.ContentHash)
	require.Equal(t, "page-1", stored.WikiPageID)
	recoverable, err := repo.ListRecoverable(ctx)
	require.NoError(t, err)
	require.Empty(t, recoverable)
}
