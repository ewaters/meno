package wrapper

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/ewaters/meno/blocks"
	"github.com/golang/glog"
)

var (
	enableLogger = true
)

type lineSubscription struct {
	from, to int
	respC    chan visibleLine
}

func (ls *lineSubscription) lineWanted(idx int) bool {
	if ls.from > idx {
		return false
	}
	if ls.to != -1 && ls.to < idx {
		return false
	}
	return true
}

type wrapEvent struct {
	lines int
}

func (we wrapEvent) String() string {
	return fmt.Sprintf("total lines: %d", we.lines)
}

type lineWrapper struct {
	width   int
	lineSep []byte

	reqC  chan chanRequest
	doneC chan bool
	quitC chan bool
}

func newLineWrapper(width int, lineSep []byte) *lineWrapper {
	return &lineWrapper{
		width:   width,
		lineSep: lineSep,
		reqC:    make(chan chanRequest),
		doneC:   make(chan bool, 1),
		quitC:   make(chan bool),
	}
}

// Runs until Stop is called. Make sure to close blockC before calling stop.
func (lw *lineWrapper) Run(blockC chan blocks.Block, wrapEventC chan wrapEvent) {
	lineC := make(chan visibleLine)

	var lines []visibleLine
	lastSubID := 0
	subsByID := make(map[int]*lineSubscription)
	linesByBlock := make(map[int][]int)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		generateVisibleLines(lw.lineSep, lw.width, blockC, lineC)
		wg.Done()
	}()

outer:
	for {
		select {
		case line, ok := <-lineC:
			if !ok {
				continue
			}
			line.number = len(lines)
			glog.V(1).Infof("got line %v", line)
			lines = append(lines, line)
			for id := line.loc.Start.BlockID; id <= line.loc.End.BlockID; id++ {
				linesByBlock[id] = append(linesByBlock[id], line.number)
			}
			// Not sure if this is a good idea or not.
			if wrapEventC != nil {
				glog.V(1).Infof("<- wrapEventC lines: %d", len(lines))
				wrapEventC <- wrapEvent{
					lines: len(lines),
				}
			}

			for _, sub := range subsByID {
				if !sub.lineWanted(line.number) {
					continue
				}
				glog.V(1).Infof("<- respC sending line %d to subscription", line.number)
				sub.respC <- line
			}
		case <-lw.quitC:
			break outer
		case req := <-lw.reqC:
			glog.V(1).Infof("got req %v", req)
			resp := chanResponse{}
			if req.lineCount {
				resp.lineCount = len(lines)
				req.respC <- resp
				continue
			}
			if sub := req.newSub; sub != nil {
				id := lastSubID
				lastSubID++
				subsByID[id] = sub
				resp.subID = id
				// We must write the response before backfilling the
				// subscription so the caller can sequence the two calls one
				// after the other.
				req.respC <- resp

				for i := sub.from; i < len(lines); i++ {
					if sub.to > -1 && i > sub.to {
						break
					}
					sub.respC <- lines[i]
				}
				continue
			}
			if id := req.cancelSub; id != nil {
				sub, ok := subsByID[*id]
				if !ok {
					resp.err = fmt.Errorf("Invalid subscription id %d", *id)
					req.respC <- resp
					continue
				}

				close(sub.respC)
				delete(subsByID, *id)
				req.respC <- resp
				continue
			}
			if id := req.linesInBlock; id != nil {
				if lineNumbers, ok := linesByBlock[*id]; ok {
					for _, i := range lineNumbers {
						resp.lines = append(resp.lines, lines[i])
					}
				}
				req.respC <- resp
				continue
			}
			resp.err = fmt.Errorf("Unhandled req %v", req)
			req.respC <- resp
		}
	}
	// Drain lineC
	for range lineC {
	}
	if wrapEventC != nil {
		close(wrapEventC)
	}
	wg.Wait()
	lw.doneC <- true
}

func (lw *lineWrapper) Stop() {
	lw.quitC <- true
	<-lw.doneC
}

type chanRequest struct {
	lineCount bool

	newSub       *lineSubscription
	cancelSub    *int
	linesInBlock *int

	respC chan chanResponse
}

func (cr chanRequest) String() string {
	if cr.lineCount {
		return "line count"
	}
	if sub := cr.newSub; sub != nil {
		return fmt.Sprintf("subscription of lines %d:%d", sub.from, sub.to)
	}
	if sub := cr.cancelSub; sub != nil {
		return fmt.Sprintf("cancel subscription %d", *sub)
	}
	if block := cr.linesInBlock; block != nil {
		return fmt.Sprintf("lines in block %d", block)
	}
	return "unknown"
}

type chanResponse struct {
	lineCount int
	subID     int
	lines     []visibleLine
	err       error
}

func (lw *lineWrapper) sendRequest(req chanRequest) chanResponse {
	respC := make(chan chanResponse, 1)
	req.respC = respC
	lw.reqC <- req
	return <-respC
}

func (lw *lineWrapper) LineCount() int {
	resp := lw.sendRequest(chanRequest{
		lineCount: true,
	})
	return resp.lineCount
}

// SubscribeLines returns all the lines that have a number (0-based) from `from`
// (inclusive) to `to` (inclusive). If `to` is -1, all lines past `from` are
// returned. Returns a subscription ID.
//
// The caller should not close `lineC` but instead call `CancelSubscription`
// with the returned subscription ID, which will close `lineC`.
func (lw *lineWrapper) SubscribeLines(from, to int, lineC chan visibleLine) (int, error) {
	if from < 0 || (to > -1 && from > to) {
		return 0, fmt.Errorf("Invalid subscription from %d to %d", from, to)
	}
	sub := &lineSubscription{
		from:  from,
		to:    to,
		respC: lineC,
	}
	resp := lw.sendRequest(chanRequest{
		newSub: sub,
	})
	return resp.subID, resp.err
}

func (lw *lineWrapper) CancelSubscription(id int) error {
	resp := lw.sendRequest(chanRequest{
		cancelSub: &id,
	})
	return resp.err
}

func (lw *lineWrapper) LinesInBlock(id int) ([]visibleLine, error) {
	resp := lw.sendRequest(chanRequest{
		linesInBlock: &id,
	})
	return resp.lines, resp.err
}

type visibleLine struct {
	number          int
	loc             blocks.BlockIDOffsetRange
	endsWithLineSep bool
}

func (vl visibleLine) String() string {
	return fmt.Sprintf("[%d] loc %v, ends with line sep %v", vl.number, vl.loc, vl.endsWithLineSep)
}

func generateVisibleLines(lineSep []byte, width int, blockC chan blocks.Block, lineC chan visibleLine) {
	var leftOver []byte
	var leftOverStart blocks.BlockIDOffset

	endsWithNewline := false

	glog.V(1).Infof("Starting range over blockC")
	for block := range blockC {
		glog.V(1).Infof("<- blockC %d", block.ID)
		start := blocks.BlockIDOffset{
			BlockID: block.ID,
			Offset:  0,
		}
		if len(leftOver) > 0 {
			start = leftOverStart
		}
		end := blocks.BlockIDOffset{
			BlockID: block.ID,
			Offset:  0 - len(leftOver),
		}
		glog.V(2).Infof("reset start: %v, end: %v", start, end)

		combined := append(leftOver, block.Bytes...)
		lines := bytes.Split(combined, lineSep)
		if glog.V(1) {
			var linesStr []string
			for _, line := range lines {
				linesStr = append(linesStr, string(line))
			}
			glog.V(1).Infof("Block [%d] %q, have lines %q", block.ID, string(block.Bytes), linesStr)
		}
		leftOver = nil
		endsWithNewline = bytes.HasSuffix(combined, lineSep)
		if endsWithNewline {
			// The last element in the lines list is an empty string; let's
			// pop it.
			lines = lines[:len(lines)-1]
		}

		for i := 0; i < len(lines); i++ {
			line := lines[i]
			lastLine := i == len(lines)-1
			for len(line) >= width {
				//part := line[:width]
				end.Offset += width - 1
				vl := visibleLine{
					// line:       part,
					loc: blocks.BlockIDOffsetRange{
						Start: start,
						End:   end,
					},
					endsWithLineSep: false,
				}
				glog.V(1).Infof("<- lineC line: %q, sending vl %v (wrapped)", string(line[:width]), vl)
				lineC <- vl
				line = line[width:]
				end.Offset++
				start = end
				glog.V(2).Infof("start: %v, end: %v", start, end)
			}
			if !lastLine || endsWithNewline {
				end.Offset += len(line) - 1 + len(lineSep)
				vl := visibleLine{
					loc: blocks.BlockIDOffsetRange{
						Start: start,
						End:   end,
					},
					endsWithLineSep: true,
				}
				glog.V(1).Infof("<- lineC %v", vl)
				lineC <- vl
				end.Offset++
				start = end
				glog.V(2).Infof("start: %v, end: %v", start, end)
			} else {
				leftOver = line
				leftOverStart = start
			}
		}
	}
	if len(leftOver) > 0 {
		end := leftOverStart
		end.Offset += len(leftOver) - 1
		vl := visibleLine{
			//line:       leftOver,
			loc: blocks.BlockIDOffsetRange{
				Start: leftOverStart,
				End:   end,
			},
			endsWithLineSep: endsWithNewline,
		}
		glog.V(1).Infof("<- lineC leftover line: %q, sending vl %v", string(leftOver), vl)
		lineC <- vl
	}
	glog.V(1).Infof("close(lineC)")
	close(lineC)
}
