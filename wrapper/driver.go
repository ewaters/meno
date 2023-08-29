package wrapper

import (
	"fmt"
	"log"

	"github.com/ewaters/meno/blocks"
)

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

type Event struct {
	Line *VisibleLine
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
			//log.Printf("Driver got line %v", line)
			buf, err := d.reader.GetBytes(line.loc)
			if err != nil {
				log.Fatalf("GetBytes(%v): %v", line.loc, err)
			}
			d.eventC <- Event{
				Line: &VisibleLine{
					Number: line.number,
					Line:   string(buf),
				},
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
