//go:build !linux

package tunnel

func ensureRSTDropRule() bool { return false }

func removeRSTDropRule() {}
