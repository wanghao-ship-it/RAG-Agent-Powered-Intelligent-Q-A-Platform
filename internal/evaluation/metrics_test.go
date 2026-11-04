package evaluation

import (
	"strings"
	"testing"
)

func TestRetrievalMetricsRecallAndMRR(t *testing.T) {
	cases := []Case{{ID: "q1", RelevantIDs: []string{"a", "b"}}, {ID: "q2", RelevantIDs: []string{"c"}}}
	retrieved := map[string][]string{"q1": {"x", "b", "a"}, "q2": {"c"}}
	recall, mrr := RetrievalMetrics(cases, retrieved, 3)
	if recall != 1 || mrr != 0.75 {
		t.Fatalf("recall=%v mrr=%v", recall, mrr)
	}
}

func TestTagWikiAndCitationMetrics(t *testing.T) {
	cases := []Case{
		{ID: "q1", ExpectedTags: []string{"t1", "t2"}, ExpectedPages: []string{"p1", "p2"}, Citations: []string{"doc1"}},
		{ID: "q2", ExpectedTags: []string{"t3"}, ExpectedPages: []string{"p3"}},
	}
	tags := map[string][]string{"q1": {"t2", "t1"}, "q2": {"wrong"}}
	pages := map[string][]string{"q1": {"p1"}, "q2": {"p3"}}
	citations := map[string][]string{"q1": {"doc1", "forged"}}
	allowed := map[string][]string{"q1": {"doc1", "doc2"}}
	if got := SetAccuracy(cases, tags, func(c Case) []string { return c.ExpectedTags }); got != 0.5 {
		t.Fatalf("tag accuracy=%v", got)
	}
	if got := WikiRecall(cases, pages); got != 0.75 {
		t.Fatalf("wiki recall=%v", got)
	}
	if got := CitationAccuracy(cases, citations, allowed); got != 0.75 {
		t.Fatalf("citation accuracy=%v", got)
	}
}

func TestLoadJSONL(t *testing.T) {
	got, err := LoadJSONL(strings.NewReader("\n{\"id\":\"q1\",\"query\":\"tag\"}\n"))
	if err != nil || len(got) != 1 || got[0].ID != "q1" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
