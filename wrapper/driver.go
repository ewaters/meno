package wrapper

import (
	"log"

	"github.com/ewaters/meno/blocks"
)

type eventFilter struct {
	topLineNumber int
	windowHeight  int
}

func (ef eventFilter) wantLine(num int) bool {
	if num < ef.topLineNumber || num > ef.topLineNumber+ef.windowHeight-1 {
		return false
	}
	return true
}

type Driver struct {
	reader   *blocks.Reader
	lineWrap *lineWrapper

	filter eventFilter

	blockEventC chan blocks.Event
	quitC       chan bool
	doneC       chan bool
}

func NewDriver(reader *blocks.Reader, width, height int, lineSep []byte) (*Driver, error) {
	return &Driver{
		reader:   reader,
		lineWrap: newLineWrapper(width, lineSep),
		filter: eventFilter{
			topLineNumber: 0,
			windowHeight:  height,
		},
		blockEventC: make(chan blocks.Event, 1),
		quitC:       make(chan bool),
		doneC:       make(chan bool),
	}, nil
}

type VisibleLine struct {
	Number int
	Line   string
}

type Event struct {
	Line *VisibleLine
}

// Run will do the following:
//
//  1. Start the passed `blocks.Reader`, reading from the input source.
//  2. The resulting blocks are passed to a `lineWrapper`, using the `width`
//     and `lineSep` from `New()` to wrap the blocks.
//  3. We subscribe to line events from the `lineWrapper` and will deliver
//     lines that range from [0, `height`) to the passed `eventC`.
func (d *Driver) Run(eventC chan Event) {
	go d.reader.Run(d.blockEventC)

	blockC := make(chan blocks.Block)
	go d.lineWrap.Run(blockC, nil)

	{
		lineC := make(chan visibleLine)
		_, err := d.lineWrap.SubscribeLines(0, -1, lineC)
		if err != nil {
			log.Fatalf("SubscribeLines(): %v", err)
		}
		go func() {
			for line := range lineC {
				if !d.filter.wantLine(line.number) {
					continue
				}
				//log.Printf("Driver got line %v", line)
				buf, err := d.reader.GetBytes(line.loc)
				if err != nil {
					log.Fatalf("GetBytes(%v): %v", line.loc, err)
				}
				eventC <- Event{
					Line: &VisibleLine{
						Number: line.number,
						Line:   string(buf),
					},
				}
			}
			log.Printf("lineC was closed")
		}()
	}

	blockClosed := false

outer:
	for {
		select {
		case <-d.quitC:
			if !blockClosed {
				close(blockC)
			}
			break outer
		case blockEvent := <-d.blockEventC:
			log.Printf("Got block event %v", blockEvent)
			if blockEvent.NewBlock != nil {
				blockC <- *blockEvent.NewBlock
			}
			if blockEvent.Status.RemainingBytes == 0 {
				log.Printf("Closing blockC since no remaining bytes")
				close(blockC)
				blockClosed = true
			}
		}
	}
	close(eventC)
	d.doneC <- true
}

func (d *Driver) Stop() {
	d.quitC <- true
	<-d.doneC

	d.lineWrap.Stop()
}

func (d *Driver) SetTopLineNumber(newTop int) {
	if newTop == d.filter.topLineNumber {
		return
	}
	if newTop < 0 {
		log.Fatalf("SetTopLineNumber(%d) invalid number", newTop)
	}
	d.filter.topLineNumber = newTop
}
