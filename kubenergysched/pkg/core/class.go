package core

import "strings"

const (
	ClassCPU = "cpu"
	ClassGPU = "gpu"
	ClassMem = "mem"
)

// NormaliseClass maps various aliases to the canonical accelerator classes.
func NormaliseClass(value string) string {
	v := strings.TrimSpace(strings.ToLower(value))
	switch v {
	case "", "none":
		return ""
	case "cpu", "general", "standard", "compute":
		return ClassCPU
	case "gpu", "accelerator":
		return ClassGPU
	case "mem", "memory", "memory-optimized", "memory_optimized":
		return ClassMem
	default:
		return ""
	}
}
