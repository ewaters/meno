package wrapper

import (
	"bytes"
	"fmt"
	"log"
	"sync"

	"github.com/ewaters/meno/blocks"
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
func (lw *lineWrapper) Run(blockC chan blocks.Block) {
	lineC := make(chan visibleLine)

	var lines []visibleLine
	lastSubID := 0
	subsByID := make(map[int]*lineSubscription)

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
			if enableLogger {
				log.Printf("got line %v", line)
			}
			lines = append(lines, line)

			for _, sub := range subsByID {
				if !sub.lineWanted(line.number) {
					continue
				}
				sub.respC <- line
			}
		case <-lw.quitC:
			break outer
		case req := <-lw.reqC:
			if enableLogger {
				log.Printf("got req %v", req)
			}
			resp := chanResponse{}
			if req.lineCount {
				resp.lineCount = len(lines)
			} else if sub := req.newSub; sub != nil {
				id := lastSubID
				lastSubID++
				for i := sub.from; i < len(lines); i++ {
					if sub.to > -1 && i > sub.to {
						break
					}
					sub.respC <- lines[i]
				}
				subsByID[id] = sub
				resp.subID = id
			} else if id := req.cancelSub; id != nil {
				sub, ok := subsByID[*id]
				if !ok {
					resp.err = fmt.Errorf("Invalid subscription id %d", *id)
				} else {
					close(sub.respC)
					delete(subsByID, *id)
				}
			} else {
				resp.err = fmt.Errorf("Unhandled req %v", req)
			}
			req.respC <- resp
		}
	}
	// Drain lineC
	for range lineC {
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

	newSub    *lineSubscription
	cancelSub *int

	respC chan chanResponse
}

func (cr chanRequest) String() string {
	if cr.lineCount {
		return "line count"
	}
	if sub := cr.newSub; sub != nil {
		return fmt.Sprintf("subscription of lines %d:%d", sub.from, sub.to)
	}
	return "unknown"
}

type chanResponse struct {
	lineCount int
	subID     int
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

	if enableLogger {
		log.Printf("Starting range over blockC")
	}
	for block := range blockC {
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
		if enableLogger {
			log.Printf("reset start: %v, end: %v", start, end)
		}

		combined := append(leftOver, block.Bytes...)
		lines := bytes.Split(combined, lineSep)
		if enableLogger {
			var linesStr []string
			for _, line := range lines {
				linesStr = append(linesStr, string(line))
			}
			log.Printf("Block [%d] %q, have lines %q", block.ID, string(block.Bytes), linesStr)
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
			for len(line) > width {
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
				if enableLogger {
					log.Printf("line: %q, sending vl %v (wrapped)", string(line[:width]), vl)
				}
				lineC <- vl
				line = line[width:]
				end.Offset++
				start = end
				if enableLogger {
					log.Printf("start: %v, end: %v", start, end)
				}
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
				if enableLogger {
					log.Printf("line: %q, sending vl %v", string(line), vl)
				}
				lineC <- vl
				end.Offset++
				start = end
				if enableLogger {
					log.Printf("start: %v, end: %v", start, end)
				}
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
		if enableLogger {
			log.Printf("leftover line: %q, sending vl %v", string(leftOver), vl)
		}
		lineC <- vl
	}
	close(lineC)
}
