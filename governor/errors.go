package governor

import "errors"

var (
	// ErrInvalidPercent 使用率参数超出 0-100 范围。
	ErrInvalidPercent = errors.New("governor: used percentage out of range 0-100")
	// ErrInvalidHysteresis 迟滞参数无效（LowUsedPct 不小于 HighUsedPct）。
	ErrInvalidHysteresis = errors.New("governor: low threshold must be below high threshold")
)
