package vibes

import "errors"

func combineErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	msg := ""
	for _, err := range errs {
		if msg != "" {
			msg += "\n\n"
		}
		msg += err.Error()
	}
	return errors.New(msg)
}
