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
	returned, max := 0, req.MaxResults

	keepGoing := func(i int) bool {
		select {
		case <-req.QuitC:
			return false
		default:
		}

		if !id.LineMatches(i, req.Query) {
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

	close(req.ResultC)
}
