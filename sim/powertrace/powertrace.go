package main

import (
    "context"
    "log"
    "kube-scheduler/powertrace/features"
    "net/http"
)

func main() {
    http.HandleFunc("/send-workloads", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            http.Error(w, "Only POST supported", http.StatusMethodNotAllowed)
            return
        }

        go func() {
            err := features.ScheduleFromCSV(context.Background(), "/app/data/powertrace.csv")
            if err != nil {
                log.Printf("Error sending workloads: %v", err)
            } else {
                log.Println("Workloads sent successfully.")
            }
        }()

        w.WriteHeader(http.StatusAccepted)
        w.Write([]byte("Workload dispatch started.\n"))
    })

    log.Println("PowerTrace API running on :5000")
    log.Fatal(http.ListenAndServe(":5000", nil))
}