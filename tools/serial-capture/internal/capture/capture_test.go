package capture

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixedClock(t time.Time) Clock {
	return func() time.Time { return t }
}

var testTime = time.Date(2026, 7, 24, 14, 1, 12, 345_000_000, time.UTC)

func collect() (*[]string, func(string)) {
	var got []string
	return &got, func(l string) { got = append(got, l) }
}

func TestStreamEmitsTimestampedLinePerInputLine(t *testing.T) {
	got, emit := collect()

	if err := Stream(strings.NewReader("hello\nworld\n"), fixedClock(testTime), emit); err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	want := []string{
		"2026-07-24 14:01:12.345 hello",
		"2026-07-24 14:01:12.345 world",
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("got %#v, want %#v", *got, want)
	}
}

func TestStreamStripsTrailingCarriageReturn(t *testing.T) {
	got, emit := collect()
	Stream(strings.NewReader("a\r\nb\r\n"), fixedClock(testTime), emit)
	want := []string{"2026-07-24 14:01:12.345 a", "2026-07-24 14:01:12.345 b"}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("got %#v, want %#v", *got, want)
	}
}

func TestStreamEmitsFinalPartialLineOnEOF(t *testing.T) {
	// A board that resets mid-print leaves a line with no trailing newline; it
	// should still be captured, not lost.
	got, emit := collect()
	Stream(strings.NewReader("partial no newline"), fixedClock(testTime), emit)
	want := []string{"2026-07-24 14:01:12.345 partial no newline"}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("got %#v, want %#v", *got, want)
	}
}

// fakeOpener yields a fixed sequence of readers, then cancels the run so the
// loop terminates deterministically in tests.
type fakeOpener struct {
	readers []io.ReadCloser
	i       int
	cancel  context.CancelFunc
}

func (f *fakeOpener) Open(ctx context.Context) (io.ReadCloser, error) {
	if f.i >= len(f.readers) {
		f.cancel()
		return nil, context.Canceled
	}
	rc := f.readers[f.i]
	f.i++
	return rc, nil
}

func nopCloser(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

// scriptedOpener returns a fixed sequence of (reader, error) results, then
// cancels so the loop stops.
type scriptedOpener struct {
	steps []struct {
		rc  io.ReadCloser
		err error
	}
	i      int
	cancel context.CancelFunc
}

func (s *scriptedOpener) Open(ctx context.Context) (io.ReadCloser, error) {
	if s.i >= len(s.steps) {
		s.cancel()
		return nil, context.Canceled
	}
	st := s.steps[s.i]
	s.i++
	return st.rc, st.err
}

func TestRunRetriesWhenDeviceIsNotYetPresent(t *testing.T) {
	got, emit := collect()
	ctx, cancel := context.WithCancel(context.Background())
	o := &scriptedOpener{
		steps: []struct {
			rc  io.ReadCloser
			err error
		}{
			{nil, errors.New("no device")}, // board still resetting
			{nopCloser("boot\n"), nil},     // reappears
		},
		cancel: cancel,
	}

	Run(ctx, o, fixedClock(testTime), emit, 0)

	want := []string{
		"2026-07-24 14:01:12.345 [serial-capture] open failed: no device; retrying",
		"2026-07-24 14:01:12.345 [serial-capture] connected",
		"2026-07-24 14:01:12.345 boot",
		"2026-07-24 14:01:12.345 [serial-capture] disconnected (eof)",
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("got %#v\nwant %#v", *got, want)
	}
}

func TestRunReconnectsAfterStreamEndsAndMarksTransitions(t *testing.T) {
	got, emit := collect()
	ctx, cancel := context.WithCancel(context.Background())
	o := &fakeOpener{
		readers: []io.ReadCloser{nopCloser("boot1\n"), nopCloser("boot2\n")},
		cancel:  cancel,
	}

	Run(ctx, o, fixedClock(testTime), emit, 0)

	want := []string{
		"2026-07-24 14:01:12.345 [serial-capture] connected",
		"2026-07-24 14:01:12.345 boot1",
		"2026-07-24 14:01:12.345 [serial-capture] disconnected (eof)",
		"2026-07-24 14:01:12.345 [serial-capture] connected",
		"2026-07-24 14:01:12.345 boot2",
		"2026-07-24 14:01:12.345 [serial-capture] disconnected (eof)",
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("got %#v\nwant %#v", *got, want)
	}
}
