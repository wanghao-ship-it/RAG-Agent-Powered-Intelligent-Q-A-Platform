package evaluation

import "slices"

// Metrics is the compact scorecard used by retrieval and grounding
// evaluation. All values are in [0,1].
type Metrics struct {
	Recall           float64 `json:"recall"`
	MRR              float64 `json:"mrr"`
	TagAccuracy      float64 `json:"tag_accuracy"`
	WikiRecall       float64 `json:"wiki_recall"`
	CitationAccuracy float64 `json:"citation_accuracy"`
}

// RetrievalMetrics computes mean Recall@k and mean reciprocal rank over cases.
// IDs are treated as opaque strings and duplicate retrieved IDs are ignored
// after the first occurrence.
func RetrievalMetrics(cases []Case, retrieved map[string][]string, k int) (recall, mrr float64) {
	if len(cases) == 0 {
		return 0, 0
	}
	if k <= 0 {
		k = 10
	}
	for _, c := range cases {
		relevant := make(map[string]struct{}, len(c.RelevantIDs))
		for _, id := range c.RelevantIDs {
			relevant[id] = struct{}{}
		}
		if len(relevant) == 0 {
			continue
		}
		seen := make(map[string]struct{}, k)
		hits := 0
		first := 0
		for rank, id := range retrieved[c.ID] {
			if rank >= k {
				break
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			if _, ok := relevant[id]; ok {
				hits++
				if first == 0 {
					first = rank + 1
				}
			}
		}
		recall += float64(hits) / float64(len(relevant))
		if first > 0 {
			mrr += 1 / float64(first)
		}
	}
	return recall / float64(len(cases)), mrr / float64(len(cases))
}

// SetAccuracy returns exact set-match accuracy for tag predictions.
func SetAccuracy(cases []Case, predicted map[string][]string, expected func(Case) []string) float64 {
	if len(cases) == 0 {
		return 0
	}
	correct := 0
	for _, c := range cases {
		want := uniqueSorted(expected(c))
		got := uniqueSorted(predicted[c.ID])
		if slices.Equal(want, got) {
			correct++
		}
	}
	return float64(correct) / float64(len(cases))
}

// WikiRecall measures how many expected Wiki pages were returned, averaged
// over cases. It is intentionally separate from retrieval Recall because a
// Wiki page can be the answer even when its source document is not returned.
func WikiRecall(cases []Case, pages map[string][]string) float64 {
	return meanRecall(cases, pages, func(c Case) []string { return c.ExpectedPages })
}

// CitationAccuracy is the fraction of cited IDs that belong to the retrieved
// evidence set. Empty citation lists are scored as 1 when no citations are
// expected, and 0 when citations are expected.
func CitationAccuracy(cases []Case, citations, allowed map[string][]string) float64 {
	if len(cases) == 0 {
		return 0
	}
	total := 0.0
	for _, c := range cases {
		want := len(c.Citations)
		got := citations[c.ID]
		if want == 0 {
			if len(got) == 0 {
				total++
			}
			continue
		}
		valid := make(map[string]struct{}, len(allowed[c.ID]))
		for _, id := range allowed[c.ID] {
			valid[id] = struct{}{}
		}
		hits := 0
		seen := make(map[string]struct{}, len(got))
		for _, id := range got {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			if _, ok := valid[id]; ok {
				hits++
			}
		}
		total += float64(hits) / float64(len(got))
	}
	return total / float64(len(cases))
}

func meanRecall(cases []Case, actual map[string][]string, expected func(Case) []string) float64 {
	if len(cases) == 0 {
		return 0
	}
	total := 0.0
	for _, c := range cases {
		want := make(map[string]struct{})
		for _, id := range expected(c) {
			want[id] = struct{}{}
		}
		if len(want) == 0 {
			continue
		}
		hits := 0
		seen := make(map[string]struct{})
		for _, id := range actual[c.ID] {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			if _, ok := want[id]; ok {
				hits++
			}
		}
		total += float64(hits) / float64(len(want))
	}
	return total / float64(len(cases))
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}
