package wrapper

import (
	"log"

	"github.com/ewaters/meno/blocks"
)

type Wrapper struct {
	reader      *blocks.Reader
	wrapper     *lineWrapper
	blockEventC chan blocks.Event
	quitC       chan bool
	doneC       chan bool
}

func New(reader *blocks.Reader, width int) (*Wrapper, error) {
	return &Wrapper{
		reader:      reader,
		wrapper:     newLineWrapper(width),
		blockEventC: make(chan blocks.Event, 1),
		quitC:       make(chan bool),
		doneC:       make(chan bool),
	}, nil
}

func (w *Wrapper) Run() {
	go w.reader.Run(w.blockEventC)

outer:
	for {
		select {
		case <-w.quitC:
			break outer
		case event := <-w.blockEventC:
			log.Printf("Got event %v", event)
		}
	}
	w.doneC <- true
}

func (w *Wrapper) Stop() {
	w.quitC <- true
	<-w.doneC
}
