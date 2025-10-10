package core

import "time"

type Job struct {
	ID                string
	CPUReq            float64
	MemReq            float64
	DeadlineMs        int64
	Tags              map[string]string
	EstimatedDuration float64
	Labels		   map[string]string
	SubmitAt		   time.Time
}