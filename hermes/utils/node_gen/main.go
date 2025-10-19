package main

import (
	"flag"
	"log"

	"github.com/g-uva/KubEnergySched/hermes/pkg/generator"
)

func main() {
	output := flag.String("out", "config/nodes.csv", "path to write the generated node CSV")
	flag.Parse()

	if err := generator.GenerateNodes(*output); err != nil {
		log.Fatalf("generate nodes: %v", err)
	}
}
