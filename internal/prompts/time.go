package prompts

import "time"

func nullableTime(ts time.Time) any {
	if ts.IsZero() {
		return nil
	}
	return ts
}
