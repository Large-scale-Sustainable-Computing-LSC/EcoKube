#!/bin/bash
# go run cmd/run_sim.go \
#   --nodes-csv=config/nodes.csv \
#   --wl-csv=config/workloads.csv \
#   --outdir=results \
#   --ci-weights=0.05,0.2,0.8,1.2 \
#   --batch-sizes=32,128,256 \
#   --durations=30,60,120,300,900 \
#   --alpha-mass=1.0 \
#   --lookahead-min=15

go run ./cmd/run_sim.go \
  --nodes-csv=config/nodes.csv \
  --wl-csv=config/workloads.csv \
  --outdir=results

# To initialise the repository (from root)
# cd hermes
# go mod init github.com/themistack/hermes
# go mod init
# 
