// Package evaluation contains small, dependency-free evaluation primitives.
// The application evaluation service can use these types without coupling the
// metrics to a particular retrieval backend or model provider.
package evaluation

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Case is the JSONL format used by the retrieval, tag and Wiki evaluation
// sets. Fields that do not apply to a case may be omitted.
type Case struct {
	ID            string   `json:"id"`
	Query         string   `json:"query"`
	RelevantIDs   []string `json:"relevant_ids,omitempty"`
	ExpectedTags  []string `json:"expected_tags,omitempty"`
	ExpectedPages []string `json:"expected_pages,omitempty"`
	Answer        string   `json:"answer,omitempty"`
	Citations     []string `json:"citations,omitempty"`
}

// LoadJSONL loads one evaluation case per non-empty line. Blank lines are
// ignored, while malformed records return a line-numbered error.
func LoadJSONL(r io.Reader) ([]Case, error) {
	scanner := bufio.NewScanner(r)
	// Evaluation answers can contain long passages; keep the loader useful for
	// real QA data instead of the Scanner default 64K limit.
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var out []Case
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var c Case
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("evaluation JSONL line %d: %w", line, err)
		}
		out = append(out, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
