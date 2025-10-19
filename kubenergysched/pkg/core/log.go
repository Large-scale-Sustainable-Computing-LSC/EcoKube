package core

import "time"

type LogEntry struct {
    JobID  string
    Node   string
    Submit time.Time
    Start  time.Time
    End    time.Time
    WaitMS int64
    CICost float64
}