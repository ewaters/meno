package data

import (
	"fmt"
	"strings"
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

	matchesLine := func(i int) bool {
		vline := p.data.lines[i]

		// If the line contains the query, great!
		if strings.Contains(vline.line, p.query) {
			return true
		}

		// Otherwise, concatenate a suffix to the string to see if the query
		// *starts* on the lineNumber i but isn't *entirely* on that line.

		suffix := ""
		{
			var sb strings.Builder
			j := i + 1
			for sb.Len() < len(p.query) {
				if j > len(p.data.lines)-1 {
					break
				}
				vl := p.data.lines[j]
				sb.WriteString(vl.line)
				if vl.hasNewline {
					sb.WriteRune('\n')
				}
				j++
			}
			suffix = sb.String()
			//p.logf("doSearch fetched %d suffix lines", j-i)
		}

		// However, if this suffix entirely has the query, then we return
		// false since line 'i' doesn't contain it.
		if strings.Contains(suffix, p.query) {
			return false
		}

		final := fmt.Sprintf("%s%s", vline.line, suffix)
		return strings.Contains(final, p.query)
	}

	keepGoing := func(i int) bool {
		select {
		case <-p.quitC:
			return false
		default:
		}

		if !matchesLine(i) {
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
