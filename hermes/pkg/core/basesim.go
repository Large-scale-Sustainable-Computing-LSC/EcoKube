package core

import (
	"context"
	"math"
	"sort"
	"time"
)

// Generic policy interface (must match your policies' Score signature).
type Policy interface {
	Name() string
	Score(ctx context.Context, j Job, nodes []SimulatedNode) (Scores, error)
	// Select is optional; if absent, BaseSim will ArgMin itself.
}

// Optional override for fully custom selection.
type SelectFunc func(w Workload, nodes []*SimulatedNode) *SimulatedNode

type BaseSim struct {
	Clock   time.Time
	Nodes   []*SimulatedNode
	Batch   int
	Pending []Workload
	LogsBuf []LogEntry

	Select SelectFunc // optional: if set, used first
	Policy Policy     // generic policy (cisched, carbonscaler, etc.)
	CICalc func(n *SimulatedNode, w Workload, at time.Time) float64
}

func (b *BaseSim) Init(nodes []*SimulatedNode, pol Policy) {
	b.Clock = time.Now()
	b.Nodes = nodes
	b.Batch = 1
	b.Pending = nil
	b.LogsBuf = nil
	b.Policy = pol
}

func (b *BaseSim) SetScheduleBatchSize(n int) {
	if n > 0 {
		b.Batch = n
	}
}
func (b *BaseSim) AddWorkload(j Workload) { b.Pending = append(b.Pending, j) }
func (b *BaseSim) Logs() []LogEntry       { return b.LogsBuf }

// simple eventless loop: process in submit-time order, greedy at current clock
func (b *BaseSim) Run() {
	sort.Slice(b.Pending, func(i, j int) bool { return b.Pending[i].SubmitTime.Before(b.Pending[j].SubmitTime) })
	queue := make([]Workload, 0, len(b.Pending))
	i := 0
	for i < len(b.Pending) || len(queue) > 0 {
		// advance time to next submit if idle
		if len(queue) == 0 && i < len(b.Pending) && b.Clock.Before(b.Pending[i].SubmitTime) {
			b.Clock = b.Pending[i].SubmitTime
		}
		// release resources at current time
		for _, n := range b.Nodes {
			n.Release(b.Clock)
		}
		// enqueue arrivals at/before now
		for i < len(b.Pending) && !b.Pending[i].SubmitTime.After(b.Clock) {
			queue = append(queue, b.Pending[i])
			i++
		}
		if len(queue) == 0 {
			continue
		}

		// schedule up to Batch
		next := queue[:0]
		scheduled := 0
		for _, w := range queue {
			if scheduled >= b.Batch {
				next = append(next, w)
				continue
			}
			n := b.selectNode(w)
			if n == nil {
				next = append(next, w)
				continue
			}

			start := b.Clock

			var ci float64
			if b.CICalc != nil {
				ci = b.CICalc(n,w,start)
			}


			n.Reserve(w, start)
			end := start.Add(w.Duration)

			b.LogsBuf = append(b.LogsBuf, LogEntry{
				JobID:  w.ID,
				Node:   n.Name,
				Submit: w.SubmitTime,
				Start:  start,
				End:    end,
				WaitMS: int64(start.Sub(w.SubmitTime) / time.Millisecond),
				CICost: ci,
			})

			scheduled++
		}
		queue = next

		// advance time to earliest reservation end
		earliest := time.Time{}
		for _, n := range b.Nodes {
			if t := n.NextReleaseAfter(b.Clock); !t.IsZero() {
				if earliest.IsZero() || t.Before(earliest) {
					earliest = t
				}
			}
		}
		if earliest.IsZero() {
			earliest = b.Clock.Add(1 * time.Second)
		}
		b.Clock = earliest
	}
}

// selection order: custom SelectFunc → policy.Score → least-loaded fallback
func (b *BaseSim) selectNode(w Workload) *SimulatedNode {
	// 1) explicit override
	if b.Select != nil {
		if n := b.Select(w, b.Nodes); n != nil {
			return n
		}
	}

	// 2) policy-driven selection via Score
	if b.Policy != nil {
		// Build []SimulatedNode view (by value) from []*SimulatedNode
		view := make([]SimulatedNode, 0, len(b.Nodes))
		for _, np := range b.Nodes {
			view = append(view, *np)
		}

		// Workload → Job wrapper for Score; keep CanAccept using Workload
		j := Job{
			ID:                w.ID,
			CPUReq:            w.CPU,
			MemReq:            w.Memory,
			EstimatedDuration: w.Duration.Seconds(),
			SubmitAt:          w.SubmitTime,
			Labels:            w.Labels,
			Tags:              nil, // fill if you route tags
			DeadlineMs:        0,   // fill if relevant
		}

		if scores, err := b.Policy.Score(context.Background(), j, view); err == nil && len(scores) > 0 {
			if id, ok := ArgMin(scores); ok {
				for _, n := range b.Nodes {
					if n.Name == id && n.CanAccept(w) {
						return n
					}
				}
			}
		}
	}

	// 3) least-loaded fallback
	var best *SimulatedNode
	bestScore := math.MaxFloat64
	for _, n := range b.Nodes {
		if !n.CanAccept(w) {
			continue
		}
		used := (n.TotalCPU-n.AvailableCPU)/n.TotalCPU + (n.TotalMemory-n.AvailableMemory)/n.TotalMemory
		if used < bestScore {
			bestScore, best = used, n
		}
	}
	return best
}
