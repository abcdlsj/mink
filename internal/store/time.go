package store

import "time"

func unixNano(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func timeFromUnixNano(value int64) time.Time {
	return time.Unix(0, value).UTC()
}
