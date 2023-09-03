package term

import (
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ewaters/meno/blocks"
	"github.com/gdamore/tcell/v2"
)

func init() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
}

func TestTerm(t *testing.T) {
	config := MenoConfig{
		Config: blocks.Config{
			Source: blocks.ConfigSource{
				Input: strings.NewReader("abcdefg\n12345\n"),
			},
			BlockSize:      10,
			IndexNextBytes: 2,
		},
		LineSeperator: []byte("\n"),
	}

	screen := tcell.NewSimulationScreen("")
	meno, err := NewMeno(config, screen)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		meno.Run()
		wg.Done()
	}()

	maxTimes := 5
	for {
		cells, w, h := screen.GetContents()
		t.Logf("Screen is size %d x %d", w, h)

		var lines []string
		for y := 0; y < h; y++ {
			var sb strings.Builder
			for x := 0; x < w; x++ {
				idx := x + (y * w)
				for _, r := range cells[idx].Runes {
					sb.WriteRune(r)
				}
			}
			if sb.Len() == 0 {
				continue
			}
			if strings.Count(sb.String(), " ") == w {
				continue
			}
			/*
				// Special case: first chekc.
				if strings.Count(sb.String(), "X") == w {
					continue
				}
			*/
			t.Logf("[%2d]: %q", y, sb.String())
			lines = append(lines, sb.String())
		}
		maxTimes--
		if maxTimes == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	screen.InjectKeyBytes([]byte("q"))

	wg.Wait()
}
