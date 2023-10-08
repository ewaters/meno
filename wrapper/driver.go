package wrapper

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ewaters/meno/blocks"
	"github.com/golang/glog"
)

const minSearchLength = 3

type eventFilter struct {
	topLineNumber  int
	windowHeight   int
	subscriptionID int
	doneC          chan bool
	quitC          chan bool
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

	lastWrapEventMu sync.Mutex
	lastWrapEvent   *wrapEvent
}

func (lwc *lineWrapCall) GetLastWrapEvent() *wrapEvent {
	lwc.lastWrapEventMu.Lock()
	defer lwc.lastWrapEventMu.Unlock()
	return lwc.lastWrapEvent
}

func (lwc *lineWrapCall) SetLastWrapEvent(event wrapEvent) {
	lwc.lastWrapEventMu.Lock()
	defer lwc.lastWrapEventMu.Unlock()
	lwc.lastWrapEvent = &event
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
func (lwc *lineWrapCall) run(lastID int) {
	blockC := make(chan blocks.Block)
	wrapEventC := make(chan wrapEvent)
	go lwc.wrapper.Run(blockC, wrapEventC)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for wrapEvent := range wrapEventC {
			glog.V(1).Infof("[lwc: %d] -> wrapEventC %v", lwc.width, wrapEvent)
			lwc.SetLastWrapEvent(wrapEvent)
		}
		wg.Done()
	}()

	if lastID != -1 {
		glog.Infof("[lwc: %d] Backfilling to ID %d", lwc.width, lastID)
		// TODO(waters): This feels wrong. I should stream the blocks instead of
		// fetching them all at once. However, since it's just a pointer to the
		// blocks, it's only making a copy of a slice of pointers.
		blocks, err := lwc.d.reader.GetBlockRange(0, lastID)
		if err != nil {
			glog.Fatalf("GetBlockRange(0, %d): %v", lastID, err)
		}
		glog.Infof("[lwc: %d] Sending %d blocks", lwc.width, len(blocks))

		for _, block := range blocks {
			glog.V(1).Infof("[lwc: %d] Backfill block %v", lwc.width, block)
			select {
			case <-lwc.quitC:
				glog.Infof("[lwc: %d] Backfill quit; aborting", lwc.width)
				close(blockC)
				wg.Wait()
				lwc.doneC <- lastID
				return
			case blockC <- *block:
			}
		}
		glog.Infof("[lwc: %d] Backfill to ID %d done", lwc.width, lastID)
	}

	blockClosed := false
outer:
	for {
		select {
		case <-lwc.quitC:
			if !blockClosed {
				close(blockC)
			}
			break outer
		case blockEvent := <-lwc.d.blockEventC:
			glog.V(1).Infof("[lwc: %d] -> blockEventC %v", lwc.width, blockEvent)
			if blockEvent.NewBlock != nil {
				glog.V(1).Infof("[lwc: %d] <- blockC %d", lwc.width, blockEvent.NewBlock.ID)
				blockC <- *blockEvent.NewBlock
				lastID = blockEvent.NewBlock.ID
			}
			if blockEvent.Status.RemainingBytes == 0 {
				glog.V(1).Infof("[lwc: %d] Closing blockC since no remaining bytes", lwc.width)
				close(blockC)
				blockClosed = true
			}
		}
	}
	glog.Infof("[lwc: %d] done; last ID %d", lwc.width, lastID)
	wg.Wait()
	lwc.doneC <- lastID
}

func (lwc *lineWrapCall) stop() int {
	glog.Infof("[lwc: %d] stop", lwc.width)
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

func (d *Driver) Run() {
	// Subscribe to block events.
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
	d.filter.quitC <- true
	<-d.filter.doneC
	d.filter = nil
	return nil
}

func (d *Driver) TotalLines() int {
	if d.wrapCall == nil {
		return 0
	}
	lastWrapEvent := d.wrapCall.GetLastWrapEvent()
	if lastWrapEvent == nil {
		return 0
	}
	return lastWrapEvent.lines
}

func (d *Driver) WatchLines(top, height int) error {
	// Close the previous filter first.
	if err := d.closeActiveFilter(); err != nil {
		return err
	}
	if d.wrapCall == nil {
		return fmt.Errorf("Cannot WatchLines() before ResizeWindow()")
	}
	// If we don't set lineC to buffered, we will cause lineWrapper.Run to get
	// stuck waiting to send to lineC.
	// TODO: I'm not sure if there's not a latent race condition here, though.
	// Try setting this back to 0 and debug it more fully.
	lineC := make(chan visibleLine, height)
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
		quitC:          make(chan bool, 1),
	}
	go func() {
		glog.Infof("WatchLines(%d, %d): starting range over lineC", top, height)
	outer:
		for line := range lineC {
			if !filter.wantLine(line.number) {
				//glog.Infof("WatchLines(%d, %d): ignoring line %d", top, height, line.number)
				continue
			}
			//glog.Infof("WatchLines(%d, %d): reading line %v", top, height, line)
			vl, err := d.readVisibleLine(line)
			if err != nil {
				glog.Fatalf("readVisibleLine(%v): %v", line.loc, err)
			}
			//glog.Infof("WatchLines(%d, %d): sending line %d to eventC", top, height, vl.Number)
			ev := Event{
				Line: vl,
			}
			select {
			case d.eventC <- ev:
			case <-filter.quitC:
				//glog.Infof("WatchLines(%d, %d): closed externally", top, height)
				break outer
			}
		}
		glog.Infof("WatchLines(%d, %d): done", top, height)
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
		glog.Infof("ResizeWindow width %d same as before; doing nothing", width)
		return nil
	}
	d.closeActiveFilter()

	backfillToID := -1
	if d.wrapCall != nil {
		glog.Infof("Stopping previous line wrapper (width: %d)", d.wrapCall.width)
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
			glog.Fatal(err)
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
	glog.Infof("Found block IDs %v", blockIDs)

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

		//glog.Infof("Query %q in block %d is in lines %v", req.Query, bio.BlockID, lineNumbers)
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
		//glog.Infof("Query %q starting at { %v } is at { %v }", req.Query, bio, lor)
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
	//glog.Infof("parts %#v", parts)
	//glog.Infof("lor %d  %#v", len(lorPerIndex), lorPerIndex)

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
			glog.Infof("Checking line %q for %q", line.Line, query)
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
					glog.Infof(" += suffix %q", line.Line)
					suffix.WriteString(line.Line)
					end.Offset = len(line.Line) - 1
				} else {
					remain := wantSuffixLen - suffix.Len()
					glog.Infof(" += suffix %q", line.Line[:remain])
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
				glog.Infof("%q + %q contains %q at %d (end %v, endOffset %d, leftover %d)", line.Line, suffix.String(), query, idx, end, endOffset, leftOver)
				end.Offset -= leftOver
				return &LineOffsetRange{
					From: LineOffset{
						Line:   line.Number,
						Offset: idx,
					},
					To: end,
				}
			} else {
				glog.Infof("%q + %q does not contain %q", line.Line, suffix.String(), query)
			}
		}
		return nil
	*/
}
