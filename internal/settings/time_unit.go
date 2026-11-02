package settings

import "time"

type TimeUnit string

const (
	TimeUnitSecond TimeUnit = "s"
	TimeUnitMinute TimeUnit = "m"
	TimeUnitHour   TimeUnit = "h"
)

func (u TimeUnit) ToDurationUnit() time.Duration {
	switch u {
	case TimeUnitSecond:
		return time.Second
	case TimeUnitMinute:
		return time.Minute
	case TimeUnitHour:
		return time.Hour
	}

	return time.Second
}
