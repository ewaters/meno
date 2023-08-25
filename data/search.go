package data

import (
	"fmt"
)

type searchResult struct {
	query      string
	lineNumber int
	finished   bool
}

func (sr searchResult) String() string {
	if sr.finished && sr.lineNumber == 0 {
		return fmt.Sprintf("query: %q has no further results", sr.query)
	}
	return fmt.Sprintf("query: %q is on display line %d", sr.query, sr.lineNumber)
}

type SearchRequest struct {
	Query         string
	ResultC       chan searchResult
	QuitC         chan struct{}
	MaxResults    int
	SearchUp      bool
	StartFromLine int
	Logf          func(string, ...interface{})
}

func NewSearchRequest(query string) SearchRequest {
	return SearchRequest{
		Query:         query,
		ResultC:       make(chan searchResult),
		QuitC:         make(chan struct{}),
		MaxResults:    1,
		SearchUp:      false,
		StartFromLine: 0,
		Logf:          func(string, ...interface{}) {},
	}
}

func (id *IndexedData) Search(req SearchRequest) {
	l := len(req.Query)
	// The query must be at least as long as the trigram size to use the index.
	if l < 3 || l > id.lines.maxQuery {
		id.bruteForceSearch(req)
	} else {
		id.indexedSearch(req)
	}

	close(req.ResultC)
}

func (id *IndexedData) indexedSearch(req SearchRequest) {
	id.Logf("indexedSearch(%q)", req.Query)
	returned, max := 0, req.MaxResults
	skipLines := func(i int) bool {
		if req.SearchUp {
			if i >= req.StartFromLine {
				return true
			}
		} else {
			if i <= req.StartFromLine {
				return true
			}
		}
		return false
	}
	for _, i := range id.lines.LinesMatching(req.Query, skipLines) {
		req.ResultC <- searchResult{
			query:      req.Query,
			lineNumber: i,
		}
		returned++
		if max > 0 && returned >= max {
			break
		}
	}
}

func (id *IndexedData) bruteForceSearch(req SearchRequest) {
	returned, max := 0, req.MaxResults

	keepGoing := func(i int) bool {
		select {
		case <-req.QuitC:
			return false
		default:
		}

		if !id.lines.LineMatches(i, req.Query) {
			return true
		}

		req.ResultC <- searchResult{
			query:      req.Query,
			lineNumber: i,
		}
		returned++
		if max > 0 && returned >= max {
			return false
		}
		return true
	}

	if req.SearchUp {
		req.Logf("searching up from %d to 0", req.StartFromLine)
		for i := req.StartFromLine; i >= 0; i-- {
			if !keepGoing(i) {
				break
			}
		}
	} else {
		req.Logf("searching down from %d to %d", req.StartFromLine, id.VisibleLines())
		for i := req.StartFromLine; i < id.VisibleLines(); i++ {
			if !keepGoing(i) {
				break
			}
		}
	}
}
