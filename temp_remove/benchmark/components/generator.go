package benchmark

import (
    "fmt"
	"math/rand"
	"kube-scheduler/pkg/core"
)

func GenerateSyntheticWorkloads(num_jobs int) []core.Workload {
	workloads := []core.Workload{}
	for i := 0; i < num_jobs; i++ {
		w := core.Workload{
			ID: fmt.Sprintf("job-%d", i),
			CPURequirement: rand.Intn(32) + 1, // Between 1 and 32 CPU cores
			EnergyPriority: rand.Float64(), // Between 0.0 and 1.0
		}
		workloads = append(workloads, w)
	}
	return workloads
}

type Pattern string

const (
    Burst Pattern = "burst"
    Periodic Pattern = "periodic"
    LongRunning Pattern = "long"
    LatencySensitive Pattern = "latency"
    Mixed Pattern = "mixed"
)

func GeneratePatternedWorkloads(num int, pattern Pattern) []core.Workload {
    var workloads []core.Workload
    for i := 0; i < num; i++ {
        var cpu int
        var energy float64

        switch pattern {
        case Burst:
            cpu = rand.Intn(16) + 16        // 16–32 cores
            energy = 0.2 + rand.Float64()*0.3 // burst users often deprioritise energy
        case Periodic:
            cpu = rand.Intn(4) + 4         // 4–8 cores
            energy = 0.8 + rand.Float64()*0.2
        case LongRunning:
            cpu = rand.Intn(8) + 8         // 8–16 cores
            energy = 0.7
        case LatencySensitive:
            cpu = 1 + rand.Intn(2)         // 1–2 cores
            energy = 1.0
        case Mixed:
            sub := []Pattern{Burst, Periodic, LongRunning, LatencySensitive}
            return append(workloads, GeneratePatternedWorkloads(1, sub[rand.Intn(len(sub))])...)
        }

        workloads = append(workloads, core.Workload{
            ID: fmt.Sprintf("%s-job-%d", pattern, i),
            CPURequirement: cpu,
            EnergyPriority: energy,
        })
    }
    return workloads
}
