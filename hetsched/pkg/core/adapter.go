package core

func JobView(w Workload) Job {
	return Job{
		ID:                w.ID,
		CPUReq:            w.CPU,
		MemReq:            w.Memory,
		EstimatedDuration: w.Duration.Seconds(),
		Labels:            w.Labels,
		SubmitAt:          w.SubmitTime,
	}
}