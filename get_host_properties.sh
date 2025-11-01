#!/bin/bash

# This is to get the host properties to copy/paste in the Thesis.

set +e

OUT="machine_report_$(date +%Y%m%d_%H%M).md"

echo "# Machine and Environment Report" > "$OUT"
echo "_Generated: $(date -Is)_  " >> "$OUT"
echo "" >> "$OUT"

sec () { echo -e "\n## $1\n" >> "$OUT"; }
kv  () { printf "- **%s:** %s\n" "$1" "$2" >> "$OUT"; }
run () {
  CMD="$1"
  HDR="$2"
  echo -e "\n### $HDR\n" >> "$OUT"
  echo -e "\\\$\ $CMD" >> "$OUT"
  (eval "$CMD") >> "$OUT" 2>&1 || echo "(not available)" >> "$OUT"
}

# Host OS and Kernel
sec "Host OS and Kernel"
run "lsb_release -a" "Distribution info"
run "cat /etc/os-release" "os-release"
run "uname -a" "Kernel"

# CPU
sec "CPU"
run "lscpu" "CPU summary"
run "grep -m1 \"model name\" /proc/cpuinfo" "CPU model (first entry)"

# Memory
sec "Memory"
run "free -h" "Free memory"
run "sudo dmidecode -t memory | egrep -i \"Size:|Type:|Speed:\" | sed \"/No Module Installed/d\" | head -n 40" "DIMM overview (requires sudo; best-effort)"

# Storage
sec "Storage"
run "lsblk -o NAME,MODEL,SIZE,TYPE,MOUNTPOINT" "Block devices"
run "df -h /" "Filesystem usage (root)"

# GPU (if present)
sec "GPU"
run "lspci | egrep -i \"vga|3d|display\"" "PCI display devices"
run "nvidia-smi" "NVIDIA driver and GPUs"

# Containers / Orchestration
sec "Containers and Orchestration"
run "docker --version" "Docker"
run "containerd --version" "containerd"
run "kubectl version --client --output=yaml" "kubectl (client)"
run "helm version --short" "Helm"

# Toolchain
sec "Toolchain"
run "go version" "Go"
run "python3 --version" "Python"
run "node --version" "Node.js"
run "npm --version" "npm"

# Monitoring stack (record presence or placeholder)
sec "Monitoring Stack"
if command -v prometheus >/dev/null 2>&1; then
  run "prometheus --version" "Prometheus"
else
  kv "Prometheus" "latest stable (declared; binary not present)"
fi
if command -v grafana-server >/dev/null 2>&1; then
  run "grafana-server -v" "Grafana server"
else
  kv "Grafana server" "latest stable (declared; binary not present)"
fi
if command -v scaphandre >/dev/null 2>&1; then
  run "scaphandre --version" "Scaphandre"
else
  kv "Scaphandre" "latest stable (declared; binary not present)"
fi

# Networking
sec "Networking"
run "ip -brief address" "IP addresses"
run "ip route" "Routing table"

# Environment notes
sec "Experiment Environment Notes (to fill if applicable)"
kv "Simulation seeds" "<add values>"
kv "Scrape interval" "<e.g. 5s>"
kv "Time sync" "<e.g. chrony/ntp enabled>"
kv "Container image pinning" "<yes/no>"
kv "Karmada/federation" "<enabled/disabled>"

echo -e "\nReport written to: $OUT"
echo "$OUT"