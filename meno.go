package main

import (
	"flag"
	"log"
	"os"

	"github.com/ewaters/meno/blocks"
	"github.com/ewaters/meno/term"
	"github.com/gdamore/tcell/v2"
)

var (
	maxQuery = flag.Int("max_query", 10, "Limit the size of the index by supporting indexed queries only up to this length. Anything longer will resort to brute force searching.")
)

func main() {
	flag.Parse()
	path := flag.Arg(0)

	inFile, err := os.Open(path)
	if err != nil {
		log.Fatalf("Open(%q): %v", path, err)
	}
	defer inFile.Close()

	stat, err := os.Stat(path)
	if err != nil {
		log.Fatalf("Stat(%q): %v", path, err)
	}

	config := term.MenoConfig{
		Config: blocks.Config{
			Source: blocks.ConfigSource{
				Input: inFile,
				Size:  int(stat.Size()),
			},
			BlockSize:      1024,
			IndexNextBytes: *maxQuery - 1,
		},
		LineSeperator: []byte("\n"),
	}

	screen, err := tcell.NewScreen()
	if err != nil {
		log.Fatal(err)
	}

	m, err := term.NewMeno(config, screen)
	if err != nil {
		log.Fatal(err)
	}
	m.Run()
}
