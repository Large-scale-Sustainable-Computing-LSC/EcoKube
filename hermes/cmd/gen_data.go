package main

import (
    "encoding/csv"
    "encoding/json"
    "flag"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/g-uva/KubEnergySched/hermes/pkg/generator"
)

type siteJSON struct {
    PUE    float64 `json:"pue"`
    K      float64 `json:"k"`
    Region string  `json:"region"`
    CI     float64 `json:"ci"`
}

func main() {
    var (
        nodesOut      string
        workloadsOut  string
        sitesCSVOut   string
        sitesJSONOut  string
        seed          int64
    )

    flag.StringVar(&nodesOut, "nodes-out", "", "path to write nodes.csv (name,cpu,mem,ci_profile)")
    flag.StringVar(&workloadsOut, "workloads-out", "", "path to write workloads.csv (id,submit,cpu,mem,duration,tag)")
    flag.StringVar(&sitesCSVOut, "sites-csv-out", "", "path to write sites.csv (id,pue,k,region)")
    flag.StringVar(&sitesJSONOut, "sites-json-out", "", "path to write sites.json (object keyed by site id)")
    flag.Int64Var(&seed, "seed", 42, "random seed for workload generation")
    flag.Parse()

    if nodesOut == "" && workloadsOut == "" && sitesCSVOut == "" && sitesJSONOut == "" {
        fmt.Println("Nothing to do: provide at least one of --nodes-out, --workloads-out, --sites-csv-out, --sites-json-out")
        os.Exit(1)
    }

    if nodesOut != "" {
        mustMkdir(filepath.Dir(nodesOut))
        if err := generator.GenerateNodes(nodesOut); err != nil {
            fatalf("generate nodes: %v", err)
        }
        fmt.Printf("Wrote nodes CSV to %s\n", nodesOut)
    }

    if workloadsOut != "" {
        mustMkdir(filepath.Dir(workloadsOut))
        if err := generator.GenerateWorkloads(workloadsOut, seed); err != nil {
            fatalf("generate workloads: %v", err)
        }
        fmt.Printf("Wrote workloads CSV to %s\n", workloadsOut)
    }

    if sitesCSVOut != "" {
        mustMkdir(filepath.Dir(sitesCSVOut))
        if err := writeSitesCSV(sitesCSVOut); err != nil {
            fatalf("write sites.csv: %v", err)
        }
        fmt.Printf("Wrote sites CSV to %s\n", sitesCSVOut)
    }

    if sitesJSONOut != "" {
        mustMkdir(filepath.Dir(sitesJSONOut))
        if err := writeSitesJSON(sitesJSONOut); err != nil {
            fatalf("write sites.json: %v", err)
        }
        fmt.Printf("Wrote sites JSON to %s\n", sitesJSONOut)
    }
}

func writeSitesCSV(path string) error {
    f, err := os.Create(path)
    if err != nil { return err }
    defer f.Close()
    w := csv.NewWriter(f)
    defer w.Flush()
    if err := w.Write([]string{"id","pue","k","region"}); err != nil { return err }
    rows := [][]string{
        {"site-a", "1.18", "1.00", "NL"},
        {"site-b", "1.05", "0.95", "ON"},
        {"site-c", "1.60", "1.10", "CA"},
    }
    for _, r := range rows { if err := w.Write(r); err != nil { return err } }
    return nil
}

func writeSitesJSON(path string) error {
    // Minimal 3-site example aligned with controller expectations
    data := map[string]siteJSON{
        "A": {PUE: 1.18, K: 1.00, Region: "NL", CI: 410},
        "B": {PUE: 1.05, K: 0.95, Region: "ON", CI: 120},
        "C": {PUE: 1.60, K: 1.10, Region: "CA", CI: 520},
    }
    b, err := json.MarshalIndent(data, "", "  ")
    if err != nil { return err }
    return os.WriteFile(path, b, 0o644)
}

func mustMkdir(dir string) {
    if dir == "." || dir == "" { return }
    _ = os.MkdirAll(dir, 0o755)
}

func fatalf(f string, a ...any) {
    fmt.Fprintf(os.Stderr, f+"\n", a...)
    os.Exit(1)
}

// avoid import pruning of time when copying into workloads
var _ = time.Second

