package wrapper

import (
	"log"

	"github.com/ewaters/meno/blocks"
)

type Wrapper struct {
	reader      *blocks.Reader
	lineWrap    *lineWrapper
	blockEventC chan blocks.Event
	quitC       chan bool
	doneC       chan bool
}

func New(reader *blocks.Reader, width int, lineSep []byte) (*Wrapper, error) {
	return &Wrapper{
		reader:      reader,
		lineWrap:    newLineWrapper(width, lineSep),
		blockEventC: make(chan blocks.Event, 1),
		quitC:       make(chan bool),
		doneC:       make(chan bool),
	}, nil
}

func (w *Wrapper) Run() {
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
				//log.Printf("Wrapper got line %v", line)
				buf, err := w.reader.GetBytes(line.loc)
				if err != nil {
					log.Fatalf("GetBytes(%v): %v", line.loc, err)
				}
				log.Printf("Line #%d %q", line.number, string(buf))
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
	w.doneC <- true
}

func (w *Wrapper) Stop() {
	w.quitC <- true
	<-w.doneC

	w.lineWrap.Stop()
}
