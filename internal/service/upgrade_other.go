//go:build !linux && !darwin

package service

func installedArguments([]byte) ([]string, error) { return nil, ErrUnsupported }
