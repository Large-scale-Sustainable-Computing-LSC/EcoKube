package core

import "time"

type Reservation struct {
	End time.Time
	CPU float64
	Mem float64
}