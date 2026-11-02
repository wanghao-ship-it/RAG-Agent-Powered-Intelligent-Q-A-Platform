package chatpipeline

import (
	"context"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// retrievalChannel is one ranked input to the outer retrieval fusion. The
// knowledge_hybrid channel already contains WeKnora's vector+BM25 RRF; graph
// and Tag-Wiki are added here as independent channels before rerank.
type retrievalChannel struct {
	Name    string
	Results []*types.SearchResult
	Weight  float64
}

type fourWayResults struct {
	tagWiki []*types.SearchResult
	graph   []*types.SearchResult
}

func (p *PluginSearch) searchFourWayChannels(ctx context.Context, chatManage *types.ChatManage) fourWayResults {
	if chatManage == nil || strings.TrimSpace(chatManage.RewriteQuery) == "" {
		return fourWayResults{}
	}
	var out fourWayResults
	for _, kbID := range unrestrictedSearchKBIDs(chatManage) {
		tenantID := chatManage.SearchTargets.GetTenantIDForKB(kbID)
		if tenantID == 0 {
			tenantID = chatManage.TenantID
		}
		kbCtx := types.WithExecutionTenant(ctx, tenantID)
		if p.tagWikiService != nil {
			hits, err := p.tagWikiService.Search(kbCtx, tenantID, kbID, chatManage.RewriteQuery, max(5, chatManage.EmbeddingTopK))
			if err == nil {
				out.tagWiki = append(out.tagWiki, tagWikiSearchResults(hits)...)
			}
		}
		if p.graphSearchService == nil || p.knowledgeBaseService == nil {
			continue
		}
		kb, err := p.knowledgeBaseService.GetKnowledgeBaseByIDOnly(kbCtx, kbID)
		if err != nil || kb == nil || !kb.IsGraphEnabled() {
			continue
		}
		graph, err := p.graphSearchService.Search(kbCtx, kb, types.GraphSearchRequest{Query: chatManage.RewriteQuery})
		if err == nil {
			out.graph = append(out.graph, graphSearchResults(graph)...)
		}
	}
	return out
}

func unrestrictedSearchKBIDs(chatManage *types.ChatManage) []string {
	if chatManage == nil {
		return nil
	}
	if len(chatManage.SearchTargets) == 0 {
		return dedupStrings(chatManage.KnowledgeBaseIDs)
	}
	ids := make([]string, 0, len(chatManage.SearchTargets))
	for _, target := range chatManage.SearchTargets {
		if target == nil || target.KnowledgeBaseID == "" || target.Type != types.SearchTargetTypeKnowledgeBase {
			continue
		}
		if len(target.KnowledgeIDs) > 0 || len(target.TagIDs) > 0 || len(target.ScopeTagIDs) > 0 {
			continue
		}
		ids = append(ids, target.KnowledgeBaseID)
	}
	return dedupStrings(ids)
}

func tagWikiSearchResults(hits []*types.KnowledgeTagWikiSearchResult) []*types.SearchResult {
	results := make([]*types.SearchResult, 0, len(hits))
	for _, hit := range hits {
		if hit == nil || hit.Page == nil {
			continue
		}
		page := hit.Page
		metadata := map[string]string{"source": "tag_wiki", "slug": page.Slug}
		if meta, err := page.PageMetadata.Map(); err == nil {
			if tagID, ok := meta["tag_id"].(string); ok && tagID != "" {
				metadata["tag_id"] = tagID
			}
		}
		results = append(results, &types.SearchResult{
			ID:              "tagwiki:" + page.ID,
			Content:         page.Content,
			KnowledgeID:     firstString(page.SourceKnowledgeIDs()),
			KnowledgeTitle:  page.Title,
			Score:           hit.Score,
			VectorScore:     hit.Score,
			MatchType:       types.MatchTypeEmbedding,
			ChunkType:       string(types.ChunkTypeWikiPage),
			KnowledgeBaseID: page.KnowledgeBaseID,
			Metadata:        metadata,
		})
	}
	return results
}

func graphSearchResults(result *types.GraphSearchResult) []*types.SearchResult {
	if result == nil {
		return nil
	}
	results := make([]*types.SearchResult, 0, len(result.SourceChunks))
	for i, source := range result.SourceChunks {
		score := 1.0 / float64(i+1)
		results = append(results, &types.SearchResult{
			ID:              "graph:" + source.ID,
			Content:         source.Content,
			KnowledgeID:     source.KnowledgeID,
			ChunkIndex:      source.ChunkIndex,
			KnowledgeTitle:  source.Title,
			Score:           score,
			MatchType:       types.MatchTypeGraph,
			ChunkType:       "graph",
			KnowledgeBaseID: result.KnowledgeBaseID,
			Metadata:        map[string]string{"source": "graph"},
		})
	}
	return results
}

// fuseFourWayRRF merges ranked channels, deduplicating both stable IDs and
// identical content before the existing rerank stage. RRF is rank based, so
// vector, BM25, graph and Tag-Wiki score scales do not need to match.
func fuseFourWayRRF(channels []retrievalChannel, topK int) []*types.SearchResult {
	const rrfK = 60
	denominator := 0.0
	for _, channel := range channels {
		if channel.Weight > 0 {
			denominator += channel.Weight / float64(rrfK+1)
		}
	}
	if denominator == 0 {
		return nil
	}
	type ranked struct {
		result *types.SearchResult
		score  float64
	}
	merged := make(map[string]ranked)
	for _, channel := range channels {
		if channel.Weight <= 0 || len(channel.Results) == 0 {
			continue
		}
		results := append([]*types.SearchResult(nil), channel.Results...)
		sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
		seen := make(map[string]struct{}, len(results))
		for rank, result := range results {
			if result == nil {
				continue
			}
			key := result.ID
			if signature := searchutil.BuildContentSignature(result.Content); signature != "" {
				key = "content:" + signature
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			score := channel.Weight / float64(rrfK+rank+1)
			if existing, ok := merged[key]; ok {
				existing.score += score
				if result.Score > existing.result.Score {
					existing.result = result
				}
				merged[key] = existing
			} else {
				merged[key] = ranked{result: result, score: score}
			}
		}
	}
	out := make([]*types.SearchResult, 0, len(merged))
	for _, item := range merged {
		item.result.Score = item.score / denominator
		out = append(out, item.result)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out
}

func dedupStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
