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
	go w.lineWrap.Run(blockC)

outer:
	for {
		select {
		case <-w.quitC:
			close(blockC)
			break outer
		case event := <-w.blockEventC:
			log.Printf("Got event %v", event)
			if event.NewBlock != nil {
				blockC <- *event.NewBlock
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
