package settings

import "time"

type TimeSettings struct {
	Value int64    `json:"value"`
	Unit  TimeUnit `json:"unit"`
}

func (s *TimeSettings) ToDuration() time.Duration {
	return time.Duration(s.Value) * s.Unit.ToDurationUnit()
}
