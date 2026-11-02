package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFuseFourWayRRFDeduplicatesAndNormalizes(t *testing.T) {
	base := &types.SearchResult{ID: "chunk-1", Content: "same evidence", Score: 0.9}
	graph := &types.SearchResult{ID: "graph:1", Content: "same evidence", Score: 1}
	tagWiki := &types.SearchResult{ID: "tagwiki:1", Content: "tag evidence", Score: 0.8}
	got := fuseFourWayRRF([]retrievalChannel{
		{Name: "knowledge_hybrid", Results: []*types.SearchResult{base}, Weight: 0.7},
		{Name: "graph", Results: []*types.SearchResult{graph}, Weight: 0.15},
		{Name: "tag_wiki", Results: []*types.SearchResult{tagWiki}, Weight: 0.15},
	}, 10)
	if len(got) != 2 {
		t.Fatalf("fused result count = %d, want 2 after content deduplication", len(got))
	}
	for _, result := range got {
		if result.Score <= 0 || result.Score > 1 {
			t.Fatalf("RRF score = %v, want (0,1]", result.Score)
		}
	}
}

func TestUnrestrictedSearchKBIDsSkipsScopedTargets(t *testing.T) {
	chat := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			KnowledgeBaseIDs: []string{"kb-fallback"},
			SearchTargets: types.SearchTargets{
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-all"},
				{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-doc", KnowledgeIDs: []string{"doc-1"}},
			},
		},
	}
	got := unrestrictedSearchKBIDs(chat)
	if len(got) != 1 || got[0] != "kb-all" {
		t.Fatalf("unrestricted KBs = %#v, want [kb-all]", got)
	}
}
