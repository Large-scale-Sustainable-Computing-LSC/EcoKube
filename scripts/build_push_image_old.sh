#!/bin/bash

# Depending on where we're executing this script, we need it's "absolute" path.
SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
cd $SCRIPT_DIR/..

# Central Unit
docker build -f ./centralunit/Dockerfile -t gd1.lab.uvalight.net/centralunit .
docker push gd1.lab.uvalight.net/centralunit:latest

# Compute node
docker build -f ./computenode/Dockerfile -t gd1.lab.uvalight.net/computenode .
docker push gd1.lab.uvalight.net/computenode:latest

# Benchmark
docker build -f ./benchmark/Dockerfile -t gd1.lab.uvalight.net/benchmark .
docker push gd1.lab.uvalight.net/benchmark:latest

# Powertrace
docker build -f ./powertrace/Dockerfile -t gd1.lab.uvalight.net/powertrace:latest .
docker push gd1.lab.uvalight.net/powertrace:latest
