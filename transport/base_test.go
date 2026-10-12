package transport

import (
	"errors"
	"testing"
)

func TestReportFatalIgnoredWhenNotRunning(t *testing.T) {
	b := NewBase(DefaultConfig())
	b.ReportFatal(errors.New("early"))
	select {
	case err := <-b.Fatal():
		t.Fatalf("unexpected fatal before start: %v", err)
	default:
	}
}

func TestReportFatalDeliversFirstErrorWithoutBlocking(t *testing.T) {
	b := NewBase(DefaultConfig())
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	first := errors.New("first")
	b.ReportFatal(first)
	b.ReportFatal(errors.New("second"))
	if err := <-b.Fatal(); !errors.Is(err, first) {
		t.Fatalf("got %v, want %v", err, first)
	}
}

func TestReportFatalIgnoredAfterStop(t *testing.T) {
	b := NewBase(DefaultConfig())
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	b.Stop()
	if b.IsRunning() {
		t.Fatal("still running after Stop")
	}
	b.ReportFatal(errors.New("late"))
	select {
	case err := <-b.Fatal():
		t.Fatalf("unexpected fatal after stop: %v", err)
	default:
	}
}
