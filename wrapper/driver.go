package wrapper

import (
	"fmt"
	"log"
	"strings"

	"github.com/ewaters/meno/blocks"
)

const minSearchLength = 3

type eventFilter struct {
	topLineNumber  int
	windowHeight   int
	subscriptionID int
	doneC          chan bool
}

func (ef eventFilter) wantLine(num int) bool {
	if num < ef.topLineNumber || num > ef.topLineNumber+ef.windowHeight-1 {
		return false
	}
	return true
}

// The lineWrapCall encapsulates a lineWrapper connected to a block.Reader.
type lineWrapCall struct {
	d       *Driver
	width   int
	wrapper *lineWrapper
	quitC   chan bool
	// The doneC is passed the last block ID read from the blockeEventC (passed
	// to run). This is to permit another lineWrapCall to backfill up to that
	// point before resuming the read.
	doneC chan int
}

func (d *Driver) newLineWrapCall(width int) *lineWrapCall {
	return &lineWrapCall{
		d:       d,
		width:   width,
		wrapper: newLineWrapper(width, d.lineSep),
		quitC:   make(chan bool),
		doneC:   make(chan int),
	}
}

// run starts the lineWrapper and, if requested, backfills from the
// blocks.Reader up to `backfillToID`. It then subscribes to `d.blockEventC` and
// feeds new blocks into the lineWrapper. If `stop()` is called, we shutdown the
// lineWrapper and return the last block ID that was read from `blockEventC` so
// that a new lineWrapCall can be created (with a different width) that will
// backfill up to this point before resuming the read.
func (lwc *lineWrapCall) run(backfillToID int) {
	blockC := make(chan blocks.Block)
	go lwc.wrapper.Run(blockC, nil)

	if backfillToID != -1 {
		log.Printf("[lwc: %d] Backfilling to ID %d", lwc.width, backfillToID)
		blocks, err := lwc.d.reader.GetBlockRange(0, backfillToID)
		if err != nil {
			log.Fatalf("GetBlockRange(0, %d): %v", backfillToID, err)
		}
		for _, block := range blocks {
			log.Printf("[lwc: %d] Backfill block %v", lwc.width, block)
			blockC <- *block
		}
	}

	blockClosed := false
	lastID := -1
outer:
	for {
		select {
		case <-lwc.quitC:
			if !blockClosed {
				close(blockC)
			}
			break outer
		case blockEvent := <-lwc.d.blockEventC:
			log.Printf("[lwc: %d] Got block event %v", lwc.width, blockEvent)
			if blockEvent.NewBlock != nil {
				blockC <- *blockEvent.NewBlock
				lastID = blockEvent.NewBlock.ID
			}
			if blockEvent.Status.RemainingBytes == 0 {
				log.Printf("[lwc: %d]: Closing blockC since no remaining bytes", lwc.width)
				close(blockC)
				blockClosed = true
			}
		}
	}
	log.Printf("[lwc: %d]: done; last ID %d", lwc.width, lastID)
	lwc.doneC <- lastID
}

func (lwc *lineWrapCall) stop() int {
	log.Printf("[lwc: %d]: stop", lwc.width)
	lwc.quitC <- true
	lwc.wrapper.Stop()
	return <-lwc.doneC
}

type Driver struct {
	lineSep []byte
	reader  *blocks.Reader

	wrapCall *lineWrapCall
	filter   *eventFilter

	eventC      chan Event
	blockEventC chan blocks.Event
}

func NewDriver(reader *blocks.Reader, lineSep []byte) (*Driver, error) {
	return &Driver{
		lineSep:     lineSep,
		reader:      reader,
		eventC:      make(chan Event),
		blockEventC: make(chan blocks.Event, 1),
	}, nil
}

type VisibleLine struct {
	Number int
	Line   string
}

func (vl VisibleLine) String() string { return fmt.Sprintf("[%d] %q", vl.Number, vl.Line) }

type LineOffset struct {
	Line, Offset int
}

func (lo LineOffset) String() string { return fmt.Sprintf("line: %d, offset: %d", lo.Line, lo.Offset) }

type LineOffsetRange struct {
	From, To LineOffset
}

func (lor LineOffsetRange) String() string {
	return fmt.Sprintf("from { %v } to { %v }", lor.From, lor.To)
}

type SearchRequest struct {
	Query string
}

type SearchStatus struct {
	Request  SearchRequest
	Complete bool
	Results  []LineOffsetRange
}

func (ss SearchStatus) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "request { %v }", ss.Request)
	if ss.Complete {
		sb.WriteString(" -- complete")
	}
	fmt.Fprintf(&sb, "; %d results", len(ss.Results))
	if len(ss.Results) > 0 {
		fmt.Fprintf(&sb, ", first result { %v }", ss.Results[0])
	}
	return sb.String()
}

type Event struct {
	Line   *VisibleLine
	Search *SearchStatus
}

func (d *Driver) Events() chan Event { return d.eventC }

// Run will do the following:
//
//  1. Start the passed `blocks.Reader`, reading from the input source.
//  2. The resulting blocks are passed to a `lineWrapper`, using the `width`
//     and `lineSep` from `New()` to wrap the blocks.
//  3. We subscribe to line events from the `lineWrapper` and will deliver
//     lines that range from [0, `height`) to the passed `eventC`.
func (d *Driver) Run() {
	go d.reader.Run(d.blockEventC)
}

func (d *Driver) Stop() {
	d.closeActiveFilter()
	if d.wrapCall != nil {
		d.wrapCall.stop()
	}
	close(d.eventC)
}

func (d *Driver) closeActiveFilter() error {
	if d.filter == nil || d.wrapCall == nil {
		return nil
	}
	err := d.wrapCall.wrapper.CancelSubscription(d.filter.subscriptionID)
	if err != nil {
		return fmt.Errorf("CancelSubscription(%d): %w", d.filter.subscriptionID, err)
	}
	<-d.filter.doneC
	d.filter = nil
	return nil
}

func (d *Driver) WatchLines(top, height int) error {
	// Close the previous filter first.
	if err := d.closeActiveFilter(); err != nil {
		return err
	}
	if d.wrapCall == nil {
		return fmt.Errorf("Cannot WatchLines() before ResizeWindow()")
	}
	lineC := make(chan visibleLine)
	firstLine, lastLine := top, top+height-1
	subID, err := d.wrapCall.wrapper.SubscribeLines(firstLine, lastLine, lineC)
	if err != nil {
		return fmt.Errorf("SubscribeLines(): %v", err)
	}

	filter := &eventFilter{
		subscriptionID: subID,
		topLineNumber:  top,
		windowHeight:   height,
		doneC:          make(chan bool),
	}
	go func() {
		log.Printf("WatchLines(%d, %d): starting range over lineC", top, height)
		for line := range lineC {
			if !filter.wantLine(line.number) {
				continue
			}
			vl, err := d.readVisibleLine(line)
			if err != nil {
				log.Fatalf("GetBytes(%v): %v", line.loc, err)
			}
			d.eventC <- Event{
				Line: vl,
			}
		}
		log.Printf("WatchLines(%d, %d): lineC was closed", top, height)
		filter.doneC <- true
	}()
	d.filter = filter
	return nil
}

// ResizeWindow will cancel any active WatchLines() calls.
func (d *Driver) ResizeWindow(width int) error {
	if width < 1 {
		return fmt.Errorf("Invalid width %d", width)
	}
	if d.wrapCall != nil && d.wrapCall.width == width {
		return nil
	}
	d.closeActiveFilter()

	backfillToID := -1
	if d.wrapCall != nil {
		log.Printf("Stopping previous line wrapper (width: %d)", d.wrapCall.width)
		backfillToID = d.wrapCall.stop()
	}

	d.wrapCall = d.newLineWrapCall(width)
	go d.wrapCall.run(backfillToID)
	return nil
}

func (d *Driver) Search(req SearchRequest) error {
	if d.wrapCall == nil {
		return fmt.Errorf("Can't run Search without ResizeWindow() being called")
	}
	if l := len(req.Query); l < minSearchLength {
		return fmt.Errorf("Query %q is shorter than min length %d", req.Query, minSearchLength)
	}
	go func() {
		d.eventC <- Event{
			Search: &SearchStatus{
				Request: req,
			},
		}
		lor, err := d.runSearch(req)
		if err != nil {
			log.Fatal(err)
		}
		d.eventC <- Event{
			Search: &SearchStatus{
				Request:  req,
				Complete: true,
				Results:  lor,
			},
		}

	}()
	return nil
}

func (d *Driver) runSearch(req SearchRequest) ([]LineOffsetRange, error) {
	blockIDs, err := d.reader.BlockIDsContaining(req.Query)
	if err != nil {
		return nil, err
	}
	log.Printf("Found block IDs %v", blockIDs)

	var results []LineOffsetRange
	dedupeLor := make(map[string]bool)
	for _, bio := range blockIDs {
		var lines []visibleLine
		var lineNumbers []int
		seenNumbers := make(map[int]bool)

		// We only know that the block started the query; we don't know if it
		// ended it, so we fetch the next block's lines as well.
		// This assumes that the block size > len(req.Query)
		for i := bio.BlockID; i <= bio.BlockID+1; i++ {
			tmpLines, err := d.wrapCall.wrapper.LinesInBlock(bio.BlockID)
			if err != nil {
				return nil, err
			}
			for _, line := range tmpLines {
				if _, ok := seenNumbers[line.number]; ok {
					continue
				}
				lines = append(lines, line)
				lineNumbers = append(lineNumbers, line.number)
				seenNumbers[line.number] = true
			}
		}

		vlines, err := d.readVisibleLines(lines)
		if err != nil {
			return nil, err
		}

		//log.Printf("Query %q in block %d is in lines %v", req.Query, bio.BlockID, lineNumbers)
		lors := lineOffsetRangeForQueryIn(vlines, req.Query)
		for _, lor := range lors {
			// We may see the same lor twice since we're loading the next block
			key := lor.String()
			if _, ok := dedupeLor[key]; ok {
				continue
			}
			dedupeLor[key] = true
			results = append(results, lor)
		}
		//log.Printf("Query %q starting at { %v } is at { %v }", req.Query, bio, lor)
	}
	return results, nil
}

func (d *Driver) readVisibleLine(line visibleLine) (*VisibleLine, error) {
	buf, err := d.reader.GetBytes(line.loc)
	if err != nil {
		return nil, fmt.Errorf("GetBytes(%v): %v", line, err)
	}
	return &VisibleLine{
		Number: line.number,
		Line:   string(buf),
	}, nil
}

func (d *Driver) readVisibleLines(lines []visibleLine) ([]*VisibleLine, error) {
	var vlines []*VisibleLine
	for _, line := range lines {
		vl, err := d.readVisibleLine(line)
		if err != nil {
			return nil, err
		}
		vlines = append(vlines, vl)
	}
	return vlines, nil
}

func lineOffsetRangeForQueryIn(lines []*VisibleLine, query string) []LineOffsetRange {
	var sb strings.Builder

	// This is very much a brute force method but I'm good with that.
	var lorPerIndex []LineOffset
	for _, line := range lines {
		sb.WriteString(line.Line)
		for i := range line.Line {
			lorPerIndex = append(lorPerIndex, LineOffset{
				Line:   line.Number,
				Offset: i,
			})
		}
	}
	combined := sb.String()
	parts := strings.Split(combined, query)
	if len(parts) == 1 {
		return nil
	}
	//log.Printf("parts %#v", parts)
	//log.Printf("lor %d  %#v", len(lorPerIndex), lorPerIndex)

	var result []LineOffsetRange
	index := 0
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		index += len(part)
		from := lorPerIndex[index]
		index += len(query) - 1
		to := lorPerIndex[index]
		index++
		result = append(result, LineOffsetRange{
			From: from,
			To:   to,
		})
	}
	return result

	/*
		for i := 0; i < len(lines); i++ {
			line := lines[i]
			log.Printf("Checking line %q for %q", line.Line, query)
			if idx := strings.Index(line.Line, query); idx != -1 {
				return &LineOffsetRange{
					From: LineOffset{
						Line:   line.Number,
						Offset: idx,
					},
					To: LineOffset{
						Line:   line.Number,
						Offset: idx + len(query) - 1,
					},
				}
			}

			// Build up a suffix from the following lines which is 1 less than the
			// length of the query, so that if the query started on line `i`, we
			// will know it.
			wantSuffixLen := len(query) - 1
			var suffix strings.Builder
			var end LineOffset
			for j := i + 1; j < len(lines); j++ {
				line := lines[j]
				end.Line = line.Number
				if suffix.Len()+len(line.Line) <= wantSuffixLen {
					log.Printf(" += suffix %q", line.Line)
					suffix.WriteString(line.Line)
					end.Offset = len(line.Line) - 1
				} else {
					remain := wantSuffixLen - suffix.Len()
					log.Printf(" += suffix %q", line.Line[:remain])
					suffix.WriteString(line.Line[:remain])
					end.Offset = remain - 1
				}
				if suffix.Len() == wantSuffixLen {
					break
				}
			}
			combined := line.Line + suffix.String()
			idx := strings.Index(combined, query)
			if idx != -1 {
				endOffset := idx + len(query)
				leftOver := len(combined) - endOffset
				log.Printf("%q + %q contains %q at %d (end %v, endOffset %d, leftover %d)", line.Line, suffix.String(), query, idx, end, endOffset, leftOver)
				end.Offset -= leftOver
				return &LineOffsetRange{
					From: LineOffset{
						Line:   line.Number,
						Offset: idx,
					},
					To: end,
				}
			} else {
				log.Printf("%q + %q does not contain %q", line.Line, suffix.String(), query)
			}
		}
		return nil
	*/
}
