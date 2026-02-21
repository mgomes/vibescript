package vibes

import (
	"fmt"
	"time"
)

func parseJobQueueEnqueueOptions(name string, kwargs map[string]Value) (JobQueueEnqueueOptions, error) {
	if len(kwargs) == 0 {
		return JobQueueEnqueueOptions{}, nil
	}

	var delay *time.Duration
	var key *string
	extra := make(map[string]Value)

	for k, v := range kwargs {
		switch k {
		case "delay":
			d, err := valueToTimeDuration(name, v)
			if err != nil {
				return JobQueueEnqueueOptions{}, err
			}
			if d < 0 {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue delay must be non-negative", name)
			}
			delay = &d
		case "key":
			if v.Kind() != KindString {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue key must be a string", name)
			}
			s := v.String()
			if s == "" {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue key must be non-empty", name)
			}
			key = &s
		default:
			extra[k] = deepCloneValue(v)
		}
	}

	opts := JobQueueEnqueueOptions{}
	opts.Delay = delay
	opts.Key = key
	if len(extra) > 0 {
		opts.Kwargs = extra
	}
	return opts, nil
}

func valueToTimeDuration(name string, val Value) (time.Duration, error) {
	switch val.Kind() {
	case KindDuration:
		secs := val.Duration().Seconds()
		return time.Duration(secs) * time.Second, nil
	case KindInt, KindFloat:
		secs, err := valueToInt64(val)
		if err != nil {
			return 0, err
		}
		return time.Duration(secs) * time.Second, nil
	default:
		return 0, fmt.Errorf("%s.enqueue delay must be duration or numeric seconds", name)
	}
}
