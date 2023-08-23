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

type runningSearch struct {
	query         string
	data          *IndexedData
	resultC       chan searchResult
	quitC         <-chan struct{}
	maxResults    int
	searchUp      bool
	startFromLine int
	logf          func(string, ...interface{})
}

func (p *runningSearch) run() {
	returned, max := 0, p.maxResults

	keepGoing := func(i int) bool {
		select {
		case <-p.quitC:
			return false
		default:
		}

		if !p.data.LineMatches(i, p.query) {
			return true
		}

		p.resultC <- searchResult{
			query:      p.query,
			lineNumber: i,
		}
		returned++
		if max > 0 && returned >= max {
			return false
		}
		return true
	}

	if p.searchUp {
		p.logf("searching up from %d to 0", p.startFromLine)
		for i := p.startFromLine; i >= 0; i-- {
			if !keepGoing(i) {
				break
			}
		}
	} else {
		p.logf("searching down from %d to %d", p.startFromLine, len(p.data.lines)-1)
		for i := p.startFromLine; i < len(p.data.lines); i++ {
			if !keepGoing(i) {
				break
			}
		}
	}

	p.resultC <- searchResult{
		query:    p.query,
		finished: true,
	}
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
