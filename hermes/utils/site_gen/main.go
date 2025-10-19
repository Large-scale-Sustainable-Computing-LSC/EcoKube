package main

import (
	"flag"
	"log"

	"github.com/g-uva/KubEnergySched/hermes/pkg/generator"
)

func main() {
	output := flag.String("out", "config/sites.csv", "path to write the generated site CSV")
	flag.Parse()

	if err := generator.GenerateSites(*output); err != nil {
		log.Fatalf("generate sites: %v", err)
	}
}
