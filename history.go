//go:build !linux
package embedr

// requires -lreadline on linux, not sure that is available elsewhere.
// see history_linux.go
func LastHistoryLine() string {
	return ""
}
