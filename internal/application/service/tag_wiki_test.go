package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestValidateTagWikiResponseDropsInventedAndUncitedClaims(t *testing.T) {
	evidence := []tagWikiEvidence{
		{ChunkID: "chunk-1", Content: "The safety review runs every quarter."},
	}
	response := tagWikiModelResponse{
		Summary:         "The review runs every quarter.",
		SummaryEvidence: []tagWikiCitation{{ChunkIndex: 1, Quote: "runs every quarter"}},
		Facts: []tagWikiFact{
			{Statement: "The review runs every quarter.", Evidence: []tagWikiCitation{{ChunkIndex: 1, Quote: "runs every quarter"}}},
			{Statement: "It is approved by the board.", Evidence: []tagWikiCitation{{ChunkIndex: 1, Quote: "approved by the board"}}},
		},
	}
	validated := validateTagWikiResponse(response, evidence)
	require.Equal(t, "The review runs every quarter.", validated.Summary)
	require.Len(t, validated.Facts, 1)
	require.Equal(t, "The review runs every quarter.", validated.Facts[0].Statement)
}

func TestRenderTagWikiContentCarriesSourceReferencesAndStableHash(t *testing.T) {
	tag := &types.KnowledgeTag{ID: "tag-1", Name: "Safety", Description: "Safety controls"}
	evidence := []tagWikiEvidence{{KnowledgeID: "doc-1", ChunkID: "chunk-1", Title: "Manual", Content: "Review every quarter."}}
	response := &tagWikiModelResponse{
		Summary:         "Review every quarter.",
		SummaryEvidence: []tagWikiCitation{{ChunkIndex: 1, Quote: "every quarter"}},
		Facts:           []tagWikiFact{{Statement: "The review is quarterly.", Evidence: []tagWikiCitation{{ChunkIndex: 1, Quote: "Review every quarter"}}}},
	}
	content, summary := renderTagWikiContent(tag, response, evidence, nil)
	require.Contains(t, content, "# Safety")
	require.Contains(t, content, "chunk-1")
	require.Equal(t, "Review every quarter.", summary)
	require.Equal(t, tagWikiHash(content), tagWikiHash(content))
	require.Equal(t, types.StringArray{"doc-1|Manual"}, tagWikiSourceRefs(response, evidence))
	require.Equal(t, types.StringArray{"chunk-1"}, tagWikiChunkRefs(response, evidence))
}

func TestTagWikiSourceHashChangesWhenEvidenceChanges(t *testing.T) {
	tag := &types.KnowledgeTag{ID: "tag-1", Name: "Safety"}
	first := []tagWikiEvidence{{KnowledgeID: "doc-1", ChunkID: "chunk-1", Content: "one"}}
	second := []tagWikiEvidence{{KnowledgeID: "doc-1", ChunkID: "chunk-1", Content: "two"}}
	a, err := makeTagWikiSourceHash(tag, first, nil)
	require.NoError(t, err)
	b, err := makeTagWikiSourceHash(tag, second, nil)
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}
