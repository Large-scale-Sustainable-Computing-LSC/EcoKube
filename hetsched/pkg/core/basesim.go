package core

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
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
	Policy Policy     // generic policy (hetsched, carbonscaler, etc.)
	CICalc func(n *SimulatedNode, w Workload, at time.Time) float64
	Tracer DecisionTracer
}

func (b *BaseSim) Init(nodes []*SimulatedNode, pol Policy) {
	b.Clock = time.Time{}
	b.Nodes = nodes
	b.Batch = 1
	b.Pending = nil
	b.LogsBuf = nil
	b.Policy = pol
	b.Tracer = nil
}

func (b *BaseSim) SetScheduleBatchSize(n int) {
	if n > 0 {
		b.Batch = n
	}
}
func (b *BaseSim) AddWorkload(j Workload) { b.Pending = append(b.Pending, j) }
func (b *BaseSim) Logs() []LogEntry       { return b.LogsBuf }

func (b *BaseSim) SetTracer(t DecisionTracer) { b.Tracer = t }

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

			siteID := ""
			if n.Site != nil {
				siteID = n.Site.ID
			} else if n.SiteID != "" {
				siteID = n.SiteID
			}

			start := b.Clock
			work := w
			work.Duration = adjustedDurationForNode(work, n)

			var ci float64
			if b.CICalc != nil {
				ci = b.CICalc(n, work, start)
			}

			n.Reserve(work, start)
			end := start.Add(work.Duration)

			b.LogsBuf = append(b.LogsBuf, LogEntry{
				JobID:  w.ID,
				Node:   n.Name,
				Site:   siteID,
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
			Class:             w.Class,
			Tags:              nil, // fill if you route tags
			DeadlineMs:        0,   // fill if relevant
		}

		if id, scores, err := SelectSiteAndNode(context.Background(), b.Policy, j, view); err == nil {
			for _, n := range b.Nodes {
				if n.Name == id && n.CanAccept(w) {
					b.recordDecisionTrace(j, view, scores, id)
					return n
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

func adjustedDurationForNode(w Workload, n *SimulatedNode) time.Duration {
	if n == nil || w.Duration <= 0 {
		return w.Duration
	}
	jobClass := ""
	if w.Class != "" {
		jobClass = strings.ToLower(strings.TrimSpace(w.Class))
	}
	if jobClass == "" && w.Labels != nil {
		if c := strings.ToLower(strings.TrimSpace(w.Labels["resource_class"])); c != "" {
			jobClass = c
		} else if strings.EqualFold(w.Labels["requires_gpu"], "true") {
			jobClass = "gpu"
		}
	}
	nodeClass := ""
	if n.DeviceClass != "" {
		nodeClass = strings.ToLower(strings.TrimSpace(n.DeviceClass))
	}
	if nodeClass == "" && n.Labels != nil {
		if c := strings.ToLower(strings.TrimSpace(n.Labels["resource_class"])); c != "" {
			nodeClass = c
		} else if strings.EqualFold(n.Labels["gpu"], "true") {
			nodeClass = "gpu"
		}
	}
	mult := 1.0
	if jobClass != "" && nodeClass != "" && jobClass != nodeClass {
		switch jobClass {
		case "memory":
			if nodeClass == "cpu" {
				mult = 3.2
			} else if nodeClass == "gpu" {
				mult = 2.4
			} else {
				mult = 2.2
			}
		case "gpu":
			if nodeClass == "cpu" || nodeClass == "memory" {
				mult = 2.9
			} else {
				mult = 2.2
			}
		case "cpu":
			if nodeClass == "gpu" {
				mult = 1.45
			} else if nodeClass == "memory" {
				mult = 1.25
			}
		default:
			mult = 1.5
		}
	}
	if w.Labels != nil {
		preferred := strings.TrimSpace(w.Labels["preferred_site"])
		if preferred != "" {
			nodeSite := ""
			if n.Site != nil {
				nodeSite = n.Site.ID
			}
			if nodeSite == "" {
				nodeSite = n.SiteID
			}
			if nodeSite != "" && !strings.EqualFold(nodeSite, preferred) {
				switch jobClass {
				case "memory":
					mult *= 1.8
				case "gpu":
					mult *= 1.4
				default:
					mult *= 1.2
				}
			}
		}
	}
	adjusted := time.Duration(float64(w.Duration) * mult)
	if adjusted < time.Second {
		adjusted = time.Second
	}
	return adjusted
}

func (b *BaseSim) recordDecisionTrace(job Job, nodes []SimulatedNode, scores Scores, selected string) {
	if b.Tracer == nil || b.Policy == nil {
		return
	}

	var chosen *SimulatedNode
	for i := range nodes {
		if nodes[i].ID == selected || nodes[i].Name == selected {
			chosen = &nodes[i]
			break
		}
	}

	trace := &DecisionTrace{
		JobID:      job.ID,
		Node:       selected,
		ResultType: "sim_result",
		ResultID:   fmt.Sprintf("sim_result_%s", job.ID),
		Source:     "simulation",
		QueuedAt:   job.SubmitAt,
		StartedAt:  b.Clock,
		EndedAt:    b.Clock,
	}
	if b.Policy != nil {
		trace.Scheduler = b.Policy.Name()
	}
	if chosen != nil {
		trace.Site = chosen.SiteID
		if trace.Site == "" && chosen.Site != nil {
			trace.Site = chosen.Site.ID
		}
		trace.Node = chosen.Name
	}
	if cost, ok := scores[selected]; ok {
		trace.Cost = cost
	}
	if tp, ok := b.Policy.(TraceablePolicy); ok {
		if custom := tp.Trace(job, nodes, scores, selected); custom != nil {
			trace = custom
			if trace.JobID == "" {
				trace.JobID = job.ID
			}
			if trace.Node == "" {
				trace.Node = selected
			}
			if trace.Site == "" && chosen != nil {
				trace.Site = chosen.SiteID
				if trace.Site == "" && chosen.Site != nil {
					trace.Site = chosen.Site.ID
				}
			}
			if trace.Cost == 0 {
				if cost, ok := scores[selected]; ok {
					trace.Cost = cost
				}
			}
			if trace.QueuedAt.IsZero() {
				trace.QueuedAt = job.SubmitAt
			}
			if trace.StartedAt.IsZero() {
				trace.StartedAt = b.Clock
			}
			if trace.EndedAt.IsZero() {
				trace.EndedAt = b.Clock
			}
			if trace.ResultType == "" {
				trace.ResultType = "sim_result"
			}
			if trace.ResultID == "" {
				trace.ResultID = fmt.Sprintf("sim_result_%s", trace.JobID)
			}
			if trace.Scheduler == "" && b.Policy != nil {
				trace.Scheduler = b.Policy.Name()
			}
			if trace.Source == "" {
				trace.Source = "simulation"
			}
		}
	}

	_ = b.Tracer.Record(*trace)
}
