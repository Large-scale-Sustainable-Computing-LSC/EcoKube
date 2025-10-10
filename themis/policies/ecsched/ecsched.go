package ecsched

import (
	"log"
	"math"
	"time"

	"kube-scheduler/pkg/core"
	"kube-scheduler/pkg/metrics"
)

// SchedulerType selects the algorithm for scheduling.
type SchedulerType int

const (
	Kubernetes SchedulerType = iota // least-loaded
	Swarm                            // most-loaded (simple backfill)
)

// EventType defines arrival or end.
type EventType int

const (
	JobArrival EventType = iota
	JobEnd
)

// Event drives the discrete-event simulation.
type Event struct {
	Time     time.Time
	Type     EventType
	Workload core.Workload
	Node     *core.SimulatedNode
}

// DiscreteEventScheduler drives simulation.
type DiscreteEventScheduler struct {
	Clock             time.Time
	Nodes             []*core.SimulatedNode
	Events            []Event
	Logs              []core.LogEntry
	SchedType         SchedulerType
	ScheduleBatchSize int
	Pending           []core.Workload
}

// NewScheduler initialises with nodes and defaults.
func NewScheduler(nodes []*core.SimulatedNode) *DiscreteEventScheduler {
	return &DiscreteEventScheduler{
		Clock:             time.Now(),
		Nodes:             nodes,
		Events:            []Event{},
		Logs:              []core.LogEntry{},
		SchedType:         Kubernetes,
		ScheduleBatchSize: 1,
		Pending:           []core.Workload{},
	}
}

// AddWorkload enqueues an arrival event.
func (s *DiscreteEventScheduler) AddWorkload(w core.Workload) {
	s.Events = append(s.Events, Event{Time: w.SubmitTime, Type: JobArrival, Workload: w})
}

// Run executes all events and flushes the final batch.
func (s *DiscreteEventScheduler) Run() {
	s.sortEvents()
	for len(s.Events) > 0 {
		e := s.Events[0]
		s.Events = s.Events[1:]
		s.Clock = e.Time
		s.processReleases(s.Clock)
		s.handleEvent(e)
	}
	// Flush any remaining pending jobs.
	s.scheduleBatch()
}

// sortEvents keeps events time-ordered.
func (s *DiscreteEventScheduler) sortEvents() {
	slice := s.Events
	for i := range slice {
		for j := i + 1; j < len(slice); j++ {
			if slice[j].Time.Before(slice[i].Time) {
				slice[i], slice[j] = slice[j], slice[i]
			}
		}
	}
}

// processReleases frees resources then backfills pending.
func (s *DiscreteEventScheduler) processReleases(t time.Time) {
	for _, n := range s.Nodes {
		n.Release(t)
	}
	var still []core.Workload
	for _, w := range s.Pending {
		if node := s.selectNode(w); node != nil {
			node.Reserve(w, t)
			s.Events = append(s.Events, Event{Time: t.Add(w.Duration), Type: JobEnd, Node: node, Workload: w})
			ciCost := metrics.ComputeCICost(node, w, t)
			s.Logs = append(s.Logs, core.LogEntry{
				JobID:  w.ID,
				Node:   node.Name,
				Submit: w.SubmitTime,
				Start:  t,
				End:    t.Add(w.Duration),
				WaitMS: int64(t.Sub(w.SubmitTime) / time.Millisecond),
				CICost: ciCost,
			})
		} else {
			still = append(still, w)
		}
	}
	s.Pending = still
}

// handleEvent schedules arrivals in batches or queues them.
func (s *DiscreteEventScheduler) handleEvent(e Event) {
	switch e.Type {
	case JobArrival:
		s.Pending = append(s.Pending, e.Workload)
		if len(s.Pending) >= s.ScheduleBatchSize {
			s.scheduleBatch()
		}
	case JobEnd:
		log.Printf("Job %s ended on %s at %v", e.Workload.ID, e.Node.Name, s.Clock)
	}
}

// scheduleBatch assigns as many pending jobs as possible using the selected policy.
func (s *DiscreteEventScheduler) scheduleBatch() {
	if len(s.Pending) == 0 {
		return
	}
	var next []core.Workload
	for _, w := range s.Pending {
		if node := s.selectNode(w); node != nil {
			t := s.Clock
			node.Reserve(w, t)
			s.Events = append(s.Events, Event{Time: t.Add(w.Duration), Type: JobEnd, Node: node, Workload: w})
			ciCost := metrics.ComputeCICost(node, w, t)
			s.Logs = append(s.Logs, core.LogEntry{
				JobID:  w.ID,
				Node:   node.Name,
				Submit: w.SubmitTime,
				Start:  t,
				End:    t.Add(w.Duration),
				WaitMS: int64(t.Sub(w.SubmitTime) / time.Millisecond),
				CICost: ciCost,
			})
		} else {
			next = append(next, w) // keep unscheduled jobs
		}
	}
	s.Pending = next
}

// selectNode dispatches to the configured algorithm.
func (s *DiscreteEventScheduler) selectNode(w core.Workload) *core.SimulatedNode {
	switch s.SchedType {
	case Kubernetes:
		return s.scheduleKubernetes(w)
	case Swarm:
		return s.scheduleSwarm(w)
	}
	return nil
}

// scheduleKubernetes: least-loaded heuristic.
func (s *DiscreteEventScheduler) scheduleKubernetes(w core.Workload) *core.SimulatedNode {
	var best *core.SimulatedNode
	bestScore := math.MaxFloat64
	for _, n := range s.Nodes {
		if !n.CanAccept(w) {
			continue
		}
		used := (n.TotalCPU-n.AvailableCPU)/n.TotalCPU + (n.TotalMemory-n.AvailableMemory)/n.TotalMemory
		if used < bestScore {
			bestScore = used
			best = n
		}
	}
	return best
}

// scheduleSwarm: most-loaded heuristic (simple backfill).
func (s *DiscreteEventScheduler) scheduleSwarm(w core.Workload) *core.SimulatedNode {
	var best *core.SimulatedNode
	bestScore := -1.0
	for _, n := range s.Nodes {
		if !n.CanAccept(w) {
			continue
		}
		load := (n.TotalCPU-n.AvailableCPU)/n.TotalCPU + (n.TotalMemory-n.AvailableMemory)/n.TotalMemory
		if load > bestScore {
			bestScore = load
			best = n
		}
	}
	return best
}
