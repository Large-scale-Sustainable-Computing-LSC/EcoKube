package generator

import (
    "encoding/csv"
    "fmt"
    "math/rand"
    "os"
    "time"
    "path/filepath"
)

// NodeSpec represents one machine in the cluster
type NodeSpec struct {
    Name      string
    CPU       int
    Mem       int
    CIProfile string  // e.g. "static:100", "sine:150:50:3600"
}

// WorkloadSpec represents one job to submit
type WorkloadSpec struct {
    ID       string
    Submit   time.Time
    CPU      int
    Mem      int
    Duration time.Duration
    Tag      string
}

// GenerateNodes writes a CSV of {name,cpu,mem,ci_profile}
func GenerateNodes(path string) error {
//     file, _ := os.Create(path)
//     w := csv.NewWriter(file)
//     defer w.Flush()

//     w.Write([]string{"name","cpu","mem","ci_profile"})

    // 1) Ensure the parent directory exists
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        return fmt.Errorf("creating dirs for %s: %w", path, err)
    }

    // 2) Create the file (propagate errors!)
    f, err := os.Create(path)
    if err != nil {
        return fmt.Errorf("os.Create(%s): %w", path, err)
    }
    defer f.Close()

    w := csv.NewWriter(f)
    defer w.Flush()

    // 3) Write header and rows, checking each write
    if err := w.Write([]string{"name","cpu","mem","ci_profile"}); err != nil {
        return fmt.Errorf("writing header: %w", err)
    }

    // small: static low-CI
    for i:=0; i<5; i++ {
        w.Write([]string{
            fmt.Sprintf("small-%d",i),
            "4","8","static:100",
        })
    }
    // medium: volatile CI
    for i:=0; i<3; i++ {
        w.Write([]string{
            fmt.Sprintf("med-%d",i),
            "8","16","static:150",  // we can add variation
        })
    }
    // burstable: sine wave CI
    for i:=0; i<2; i++ {
        // sine:mean:amp:periodSec
        w.Write([]string{
            fmt.Sprintf("burst-%d",i),
            "16","32",fmt.Sprintf("sine:150:50:%d",3600),
        })
    }
    // gpu-heavy: random-walk
    w.Write([]string{"gpu-0","32","64","randwalk:100:200:300"})

    return nil
}

// GenerateWorkloads writes a CSV of {id,submit,cpu,mem,duration,tag}
func GenerateWorkloads(path string, seed int64) error {
//     rand.Seed(seed)
//     file, _ := os.Create(path)
//     w := csv.NewWriter(file)
//     defer w.Flush()

//     w.Write([]string{"id","submit","cpu","mem","duration","tag"})

    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        return fmt.Errorf("creating dirs for %s: %w", path, err)
    }
    f, err := os.Create(path)
    if err != nil {
        return fmt.Errorf("os.Create(%s): %w", path, err)
    }
    defer f.Close()

    w := csv.NewWriter(f)
    defer w.Flush()

    if err := w.Write([]string{"id","submit","cpu","mem","duration","tag"}); err != nil {
        return fmt.Errorf("writing header: %w", err)
    }

    rand.Seed(seed)
    now := time.Now()
    for i:=0; i<1000; i++ {
        // pick a type
        typ := rand.Intn(4)
        var cpu, mem, dur int
        var tag string
        switch typ {
        case 0: // tiny
            cpu, mem = 1,1
            dur = rand.Intn(30)+30
        case 1: // batch
            cpu = rand.Intn(5)+4
            mem = rand.Intn(13)+4
            dur = rand.Intn(301)+300
            tag = "A"
        case 2: // memory-heavy
            cpu, mem = 2, rand.Intn(17)+16
            dur = rand.Intn(61)+120
        default: // periodic mission
            cpu = rand.Intn(9)+8
            mem = rand.Intn(5)+8
            dur = rand.Intn(201)+200
            tag = "B"
        }
        // inter-arrival: Poisson λ=1/s
        delta := time.Duration(rand.ExpFloat64()*1e9) * time.Nanosecond
        now = now.Add(delta)
        w.Write([]string{
            fmt.Sprintf("job-%d",i),
            now.Format(time.RFC3339),
            fmt.Sprint(cpu), fmt.Sprint(mem),
            fmt.Sprint(dur),
            tag,
        })
    }
    return nil
}
