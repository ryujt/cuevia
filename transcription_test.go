package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testTranscriptionWAV(t *testing.T, audible bool) string {
	t.Helper()
	pcm := make([]byte, 32000)
	if audible {
		pcm[2] = 64
	}
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36+len(pcm)))
	wav.WriteString("WAVEfmt ")
	for _, value := range []any{uint32(16), uint16(1), uint16(1), uint32(16000), uint32(32000), uint16(2), uint16(16)} {
		_ = binary.Write(&wav, binary.LittleEndian, value)
	}
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	path := filepath.Join(t.TempDir(), "recording.wav")
	if err := os.WriteFile(path, wav.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTranscriptionWAVValidation(t *testing.T) {
	path := testTranscriptionWAV(t, true)
	duration, audible, err := validateTranscriptionWAV(path)
	if err != nil || duration != 1000 || !audible {
		t.Fatalf("valid WAV: duration=%d audible=%v err=%v", duration, audible, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"truncated", func(b []byte) []byte { return b[:len(b)-2] }},
		{"wrong sample rate", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[24:28], 44100); return b }},
		{"stereo", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[22:24], 2); return b }},
		{"float", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[20:22], 3); return b }},
		{"oversize chunk", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:44], 0xffffffff); return b }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := test.mutate(append([]byte(nil), data...))
			if err := os.WriteFile(path, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := validateTranscriptionWAV(path); err == nil {
				t.Fatal("accepted malformed or unsupported WAV")
			}
		})
	}
	if _, audible, err := validateTranscriptionWAV(testTranscriptionWAV(t, false)); err != nil || audible {
		t.Fatalf("silence audible=%v err=%v", audible, err)
	}
}

func TestParseWhisperJSONPreservesActualOffsets(t *testing.T) {
	const input = `{"transcription":[{"offsets":{"from":20,"to":750},"text":" First sentence "},{"timestamps":{"from":"00:00:00,750","to":"00:00:01,250"},"text":"Second sentence"}]}`
	segments, err := parseWhisperJSON(strings.NewReader(input), 1100)
	if err != nil || len(segments) != 2 {
		t.Fatalf("segments=%+v err=%v", segments, err)
	}
	if segments[0].StartMs != 20 || segments[0].EndMs != 750 || segments[0].Text != "First sentence" || segments[1].StartMs != 750 || segments[1].EndMs != 1100 {
		t.Fatalf("timestamps/text changed: %+v", segments)
	}
	for _, invalid := range []string{
		`{}`, `{"transcription":null}`, `{"transcription":[{"text":"missing time"}]}`,
		`{"transcription":[{"offsets":{"from":100,"to":20},"text":"bad time"}]}`,
		`{"transcription":[]} trailing`,
	} {
		if _, err := parseWhisperJSON(strings.NewReader(invalid), 1000); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	segments, err = parseWhisperJSON(strings.NewReader(`{"transcription":[]}`), 1000)
	if err != nil || segments == nil || len(segments) != 0 {
		t.Fatalf("empty speech must remain empty: %+v %v", segments, err)
	}
}

func TestTranscriberUnavailableAndCanceled(t *testing.T) {
	s := NewTranscriber(TranscriberConfig{BinaryPath: filepath.Join(t.TempDir(), "missing"), ModelPath: "missing"})
	if status := s.Status(); status.Ready || !strings.Contains(status.Error, "setup-transcription.sh") {
		t.Fatalf("missing engine status: %+v", status)
	}
	_, err := s.Transcribe(context.Background(), "unused.wav")
	var transcriptionErr *TranscriptionError
	if !errors.As(err, &transcriptionErr) || transcriptionErr.Kind != "unavailable" {
		t.Fatalf("error=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Transcribe(ctx, "unused.wav")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

// This checks subprocess orchestration and cleanup; it is not evidence of ASR.
func TestTranscriberProcessBoundary(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "model.bin")
	if err := os.WriteFile(model, []byte("test model"), 0600); err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(dir, "whisper-test")
	script := "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\nif [ \"$1\" = '--output-file' ]; then shift; output=$1; fi\nshift\ndone\nprintf '%s' '{\"transcription\":[{\"offsets\":{\"from\":0,\"to\":700},\"text\":\"Test\"}]}' > \"$output.json\"\n"
	if err := os.WriteFile(engine, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s := NewTranscriber(TranscriberConfig{BinaryPath: engine, ModelPath: model, JobsDir: filepath.Join(dir, "jobs")})
	segments, err := s.Transcribe(context.Background(), testTranscriptionWAV(t, true))
	if err != nil || len(segments) != 1 || segments[0].Text != "Test" || segments[0].EndMs != 700 {
		t.Fatalf("process result: %+v %v", segments, err)
	}
	if jobs, err := os.ReadDir(s.config.JobsDir); err != nil || len(jobs) != 0 {
		t.Fatalf("completed transcription retained private job data: %v %v", jobs, err)
	}
	if info, err := os.Stat(s.config.JobsDir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("transcription jobs directory is not private: %v %v", info, err)
	}
	transcriptionCPU <- struct{}{}
	_, err = s.Transcribe(context.Background(), testTranscriptionWAV(t, true))
	<-transcriptionCPU
	var transcriptionErr *TranscriptionError
	if !errors.As(err, &transcriptionErr) || transcriptionErr.Kind != "busy" {
		t.Fatalf("concurrent process was not rejected: %v", err)
	}
	if err := os.WriteFile(engine, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s.config.Timeout = 30 * time.Millisecond
	started := time.Now()
	_, err = s.Transcribe(context.Background(), testTranscriptionWAV(t, true))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("process timeout was not bounded: %v, elapsed %s", err, time.Since(started))
	}
	if jobs, err := os.ReadDir(s.config.JobsDir); err != nil || len(jobs) != 0 {
		t.Fatalf("canceled transcription retained job data: %v %v", jobs, err)
	}
	select {
	case transcriptionCPU <- struct{}{}:
		<-transcriptionCPU
	default:
		t.Fatal("CPU slot was not released after timeout")
	}
}

// Opt in with CUEVIA_TEST_REAL_WAV=/path/to/spoken-english.wav. This uses the
// installed engine and model, and deliberately fails if they are unavailable.
func TestRealTranscription(t *testing.T) {
	path := os.Getenv("CUEVIA_TEST_REAL_WAV")
	if path == "" {
		t.Skip("set CUEVIA_TEST_REAL_WAV to verify actual local ASR")
	}
	s := NewTranscriber(TranscriberConfig{})
	segments, err := s.Transcribe(context.Background(), path)
	if err != nil || len(segments) == 0 {
		t.Fatalf("real ASR: segments=%+v err=%v", segments, err)
	}
	for _, segment := range segments {
		if segment.Text == "" || segment.StartMs < 0 || segment.EndMs < segment.StartMs {
			t.Fatalf("invalid real segment: %+v", segment)
		}
	}
	t.Logf("real local ASR: %+v", segments)
}
