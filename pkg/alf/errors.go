package alf

import "errors"

var (
	ErrInvalidMagic = errors.New("invalid archive magic: expected S4 or S5 format")
	ErrNotSupported = errors.New("archive format not supported")
)
