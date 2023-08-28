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

type Wrapper struct {
	reader   *blocks.Reader
	lineWrap *lineWrapper

	filter eventFilter

	blockEventC chan blocks.Event
	quitC       chan bool
	doneC       chan bool
}

func New(reader *blocks.Reader, width, height int, lineSep []byte) (*Wrapper, error) {
	return &Wrapper{
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
func (w *Wrapper) Run(eventC chan Event) {
	go w.reader.Run(w.blockEventC)

	blockC := make(chan blocks.Block)
	go w.lineWrap.Run(blockC, nil)

	{
		lineC := make(chan visibleLine)
		_, err := w.lineWrap.SubscribeLines(0, -1, lineC)
		if err != nil {
			log.Fatalf("SubscribeLines(): %v", err)
		}
		go func() {
			for line := range lineC {
				if !w.filter.wantLine(line.number) {
					continue
				}
				//log.Printf("Wrapper got line %v", line)
				buf, err := w.reader.GetBytes(line.loc)
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
		case <-w.quitC:
			if !blockClosed {
				close(blockC)
			}
			break outer
		case blockEvent := <-w.blockEventC:
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
	w.doneC <- true
}

func (w *Wrapper) Stop() {
	w.quitC <- true
	<-w.doneC

	w.lineWrap.Stop()
}

func (w *Wrapper) SetTopLineNumber(newTop int) {
	if newTop == w.filter.topLineNumber {
		return
	}
	if newTop < 0 {
		log.Fatalf("SetTopLineNumber(%d) invalid number", newTop)
	}
	w.filter.topLineNumber = newTop
}
