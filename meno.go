package main

import (
	"flag"
	"log"
	"os"

	"github.com/ewaters/meno/data"
)

func main() {
	flag.Parse()
	path := flag.Arg(0)

	inFile, err := os.Open(path)
	if err != nil {
		log.Fatalf("Open(%q): %v", path, err)
	}
	defer inFile.Close()

	m, err := data.NewMeno(inFile)
	if err != nil {
		log.Fatal(err)
	}
	if err := m.SetLogFile("/tmp/meno.log"); err != nil {
		log.Fatal(err)
	}
	defer m.Close()
	if err := m.Run(); err != nil {
		log.Fatal(err)
	}
}
