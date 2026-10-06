//go:build !cgo

package fmi

import "fmt"

const stubUnavailable = "fmi runner requires CGO and FMIL headers/libraries"

// Run reports that the FMIL-backed runner is unavailable without CGO.
func Run(cfg Config) (*Result, error) {
	if cfg.FMUPath == "" {
		return nil, fmt.Errorf("fmi: FMU path is required")
	}
	return nil, fmt.Errorf("%s", stubUnavailable)
}

// RunCoSim reports that the FMIL-backed co-simulation master is unavailable without CGO.
func RunCoSim(cfg CoSimConfig) (*Result, error) {
	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("fmi: co-simulation requires at least one model")
	}
	return nil, fmt.Errorf("%s", stubUnavailable)
}

// RequestCancel is a no-op without CGO.
func RequestCancel() {}

// ResetCancel is a no-op without CGO.
func ResetCancel() {}
