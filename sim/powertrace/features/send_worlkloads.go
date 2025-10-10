package features

import (
    "bytes"
    "context"
    "encoding/csv"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "strconv"
    "time"
)

type Workload struct {
    ID         string        `json:"id"`
    SubmitTime time.Time     `json:"submit_time"`
    Duration   time.Duration `json:"duration"`
    CPU        float64       `json:"cpu"`
    Memory     float64       `json:"memory"`
}

func ScheduleFromCSV(ctx context.Context, csvPath string) error {
    file, err := os.Open(csvPath)
    if err != nil {
        return fmt.Errorf("open CSV: %w", err)
    }
    defer file.Close()

    reader := csv.NewReader(file)
    rows, err := reader.ReadAll()
    if err != nil {
        return fmt.Errorf("read CSV: %w", err)
    }

    startTime := time.Now()
    var workloads []Workload

    for i, row := range rows {
        if i == 0 {
            continue // skip header
        }

        timeVal, _ := strconv.ParseInt(row[0], 10, 64)
        productionPower, _ := strconv.ParseFloat(row[4], 64)

        offset := time.Duration(timeVal) * time.Microsecond
        submit := startTime.Add(offset)

        cpu := productionPower * 4.0
        mem := productionPower * 8192.0

        workloads = append(workloads, Workload{
            ID:         fmt.Sprintf("trace-%d", i),
            SubmitTime: submit,
            Duration:   5 * time.Minute,
            CPU:        cpu,
            Memory:     mem,
        })
    }

    return scheduleAndSend(workloads)
}

// Internal function to send all workloads to CentralUnit
func scheduleAndSend(workloads []Workload) error {
    for _, w := range workloads {
        planned := planSchedule(w)

        body, err := json.Marshal(planned)
        if err != nil {
            log.Printf("JSON error for %s: %v", w.ID, err)
            continue
        }

        resp, err := http.Post("http://centralunit:8080/workload-ingest", "application/json", bytes.NewReader(body))
        if err != nil {
            log.Printf("POST error for %s: %v", w.ID, err)
            continue
        }
        resp.Body.Close()

        log.Printf("Scheduled workload %s", w.ID)
    }
    return nil
}

// Optional: add scheduling logic
func planSchedule(w Workload) Workload {
    return w
}
