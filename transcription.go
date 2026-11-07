package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// TranscriptSegment uses offsets measured from the beginning of the uploaded
// recording. The browser adds its recording-start offset to the session clock.
type TranscriptSegment struct {
	StartMs         int64  `json:"startMs"`
	EndMs           int64  `json:"endMs"`
	Text            string `json:"text"`
	ProviderStartMs int64  `json:"providerStartMs,omitempty"`
	ProviderEndMs   int64  `json:"providerEndMs,omitempty"`
	TimingAdjusted  bool   `json:"timingAdjusted,omitempty"`
	TimingEstimated bool   `json:"timingEstimated,omitempty"`
}

type TranscriberConfig struct {
	BinaryPath string
	ModelPath  string
	JobsDir    string
	Language   string
	Timeout    time.Duration
	Threads    int
}

type TranscriptionStatus struct {
	Ready    bool   `json:"ready"`
	Engine   string `json:"engine"`
	Model    string `json:"model"`
	Language string `json:"language"`
	Error    string `json:"error,omitempty"`
}

type TranscriptionError struct {
	Kind string
	Err  error
}

func (e *TranscriptionError) Error() string { return e.Err.Error() }
func (e *TranscriptionError) Unwrap() error { return e.Err }

type Transcriber struct {
	config TranscriberConfig
	err    error
}

// The local engine is deliberately limited to one CPU job across all handlers.
var transcriptionCPU = make(chan struct{}, 1)

const MaxTranscriptionAudioDuration = 5 * time.Minute

// NewTranscriber does not download models or launch a process. Missing local
// prerequisites remain visible through Status and can be installed afterwards.
func NewTranscriber(config TranscriberConfig) *Transcriber {
	s := &Transcriber{config: config}
	cache, err := os.UserCacheDir()
	if err != nil && (config.BinaryPath == "" || config.ModelPath == "" || config.JobsDir == "") {
		s.err = fmt.Errorf("음성 전사 캐시 경로를 찾을 수 없습니다: %w", err)
	}
	root := filepath.Join(cache, "Cuevia", "prototype", "transcription")
	if s.config.BinaryPath == "" {
		s.config.BinaryPath = os.Getenv("CUEVIA_WHISPER_BIN")
		if s.config.BinaryPath == "" {
			s.config.BinaryPath = filepath.Join(root, "bin", "whisper-cli")
		}
	}
	if s.config.ModelPath == "" {
		s.config.ModelPath = os.Getenv("CUEVIA_WHISPER_MODEL")
		if s.config.ModelPath == "" {
			s.config.ModelPath = filepath.Join(root, "models", "ggml-base.bin")
		}
	}
	if s.config.JobsDir == "" {
		s.config.JobsDir = filepath.Join(root, "jobs")
	}
	if s.config.Language == "" {
		s.config.Language = "ko"
	}
	if s.config.Timeout <= 0 {
		s.config.Timeout = 2 * time.Minute
	}
	if s.config.Threads <= 0 {
		s.config.Threads = min(4, runtime.NumCPU())
	}
	s.config.Threads = min(s.config.Threads, 8)
	return s
}

func (s *Transcriber) Status() TranscriptionStatus {
	status := TranscriptionStatus{Engine: "whisper.cpp CPU", Model: filepath.Base(s.config.ModelPath), Language: s.config.Language}
	if err := s.checkReady(); err != nil {
		status.Error = err.Error()
		return status
	}
	status.Ready = true
	return status
}

func (s *Transcriber) checkReady() error {
	if s.err != nil {
		return s.err
	}
	binaryPath, err := exec.LookPath(s.config.BinaryPath)
	if err != nil {
		return errors.New("로컬 음성 전사 엔진이 없습니다. sh scripts/setup-transcription.sh를 실행하거나 CUEVIA_WHISPER_BIN을 설정해 주세요.")
	}
	info, err := os.Stat(binaryPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("로컬 음성 전사 엔진을 실행할 수 없습니다. sh scripts/setup-transcription.sh를 다시 실행해 주세요.")
	}
	model, err := os.Open(s.config.ModelPath)
	if err != nil {
		return errors.New("음성 전사 모델이 없습니다. sh scripts/setup-transcription.sh를 실행하거나 CUEVIA_WHISPER_MODEL을 설정해 주세요.")
	}
	defer model.Close()
	info, err = model.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("음성 전사 모델이 올바르지 않습니다. sh scripts/setup-transcription.sh를 다시 실행해 주세요.")
	}
	return nil
}

func (s *Transcriber) Transcribe(parent context.Context, wavPath string) ([]TranscriptSegment, error) {
	if err := parent.Err(); err != nil {
		return nil, &TranscriptionError{Kind: "canceled", Err: err}
	}
	if err := s.checkReady(); err != nil {
		return nil, &TranscriptionError{Kind: "unavailable", Err: err}
	}
	durationMs, audible, err := validateTranscriptionWAV(wavPath)
	if err != nil {
		return nil, &TranscriptionError{Kind: "invalid_audio", Err: err}
	}
	if !audible {
		return []TranscriptSegment{}, nil
	}
	select {
	case transcriptionCPU <- struct{}{}:
		defer func() { <-transcriptionCPU }()
	default:
		return nil, &TranscriptionError{Kind: "busy", Err: errors.New("다른 음성 전사를 처리 중입니다. 잠시 후 다시 시도해 주세요.")}
	}
	ctx, cancel := context.WithTimeout(parent, s.config.Timeout)
	defer cancel()
	if err := os.MkdirAll(s.config.JobsDir, 0700); err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: fmt.Errorf("create transcription job cache: %w", err)}
	}
	info, err := os.Lstat(s.config.JobsDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, &TranscriptionError{Kind: "failed", Err: errors.New("transcription job cache must be a directory, not a symbolic link")}
	}
	if err := os.Chmod(s.config.JobsDir, 0700); err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: fmt.Errorf("make transcription job cache private: %w", err)}
	}
	workDir, err := os.MkdirTemp(s.config.JobsDir, "job-")
	if err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: fmt.Errorf("create transcription workspace: %w", err)}
	}
	defer os.RemoveAll(workDir)
	marker, err := json.Marshal(transcriptionJobMarker{Kind: "cuevia-transcription", CreatedAt: time.Now().UTC()})
	if err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: err}
	}
	if err := os.WriteFile(filepath.Join(workDir, "job.json"), marker, 0600); err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: fmt.Errorf("mark transcription workspace: %w", err)}
	}
	outputPath := filepath.Join(workDir, "transcript")
	cmd := exec.CommandContext(ctx, s.config.BinaryPath,
		"--model", s.config.ModelPath, "--file", wavPath,
		"--language", s.config.Language, "--threads", strconv.Itoa(s.config.Threads),
		"--no-gpu", "--output-json-full", "--output-file", outputPath, "--no-prints")
	cmd.WaitDelay = time.Second
	output := &transcriptionLog{limit: 16 * 1024}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, &TranscriptionError{Kind: "canceled", Err: ctx.Err()}
		}
		return nil, &TranscriptionError{Kind: "failed", Err: fmt.Errorf("로컬 음성 전사에 실패했습니다: %w; %s", err, strings.TrimSpace(string(output.data)))}
	}
	if err := ctx.Err(); err != nil {
		return nil, &TranscriptionError{Kind: "canceled", Err: err}
	}
	file, err := os.Open(outputPath + ".json")
	if err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: errors.New("음성 전사 엔진이 결과를 만들지 못했습니다. 엔진과 모델 설정을 확인해 주세요.")}
	}
	defer file.Close()
	segments, err := parseWhisperJSON(io.LimitReader(file, 8*1024*1024+1), durationMs)
	if err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: fmt.Errorf("음성 전사 결과를 읽을 수 없습니다: %w", err)}
	}
	return segments, nil
}

// Keep process diagnostics bounded even when an external engine is misconfigured.
type transcriptionLog struct {
	data  []byte
	limit int
}

func (b *transcriptionLog) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
	} else {
		if excess := len(b.data) + len(p) - b.limit; excess > 0 {
			b.data = b.data[excess:]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func parseWhisperJSON(reader io.Reader, durationMs int64) ([]TranscriptSegment, error) {
	var result struct {
		Transcription *[]struct {
			Text    string `json:"text"`
			Offsets *struct {
				From *int64 `json:"from"`
				To   *int64 `json:"to"`
			} `json:"offsets"`
			Timestamps struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"timestamps"`
		} `json:"transcription"`
	}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after transcription JSON")
	}
	if result.Transcription == nil {
		return nil, errors.New("missing transcription array")
	}
	segments := make([]TranscriptSegment, 0, len(*result.Transcription))
	for _, row := range *result.Transcription {
		text := strings.TrimSpace(row.Text)
		if text == "" {
			continue
		}
		var start, end int64
		if row.Offsets != nil && row.Offsets.From != nil && row.Offsets.To != nil {
			start, end = *row.Offsets.From, *row.Offsets.To
		} else {
			var err error
			start, err = parseWhisperTimestamp(row.Timestamps.From)
			if err != nil {
				return nil, err
			}
			end, err = parseWhisperTimestamp(row.Timestamps.To)
			if err != nil {
				return nil, err
			}
		}
		if start < 0 || end < start {
			return nil, errors.New("invalid transcription offsets")
		}
		if start >= durationMs {
			continue
		}
		segments = append(segments, TranscriptSegment{StartMs: start, EndMs: min(end, durationMs), Text: text})
	}
	return segments, nil
}

func parseWhisperTimestamp(value string) (int64, error) {
	parts := strings.Split(strings.ReplaceAll(value, ",", "."), ":")
	if len(parts) != 3 || len(parts[2]) != 6 || parts[2][2] != '.' {
		return 0, errors.New("missing or invalid transcription timestamps")
	}
	hours, e1 := strconv.ParseInt(parts[0], 10, 32)
	minutes, e2 := strconv.ParseInt(parts[1], 10, 32)
	seconds, e3 := strconv.ParseInt(parts[2][:2], 10, 32)
	millis, e4 := strconv.ParseInt(parts[2][3:], 10, 32)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || hours < 0 || minutes < 0 || minutes > 59 || seconds < 0 || seconds > 59 || millis < 0 {
		return 0, errors.New("invalid transcription timestamps")
	}
	return ((hours*60+minutes)*60+seconds)*1000 + millis, nil
}

// The browser records uncompressed mono PCM16 at 16 kHz. Validate the container
// before passing it to native code, including padded/unknown RIFF chunks.
func validateTranscriptionWAV(path string) (durationMs int64, audible bool, resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false, fmt.Errorf("음성 파일을 열 수 없습니다: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, false, errors.New("올바른 WAV 음성 파일이 필요합니다.")
	}
	var header [12]byte
	if _, err := io.ReadFull(file, header[:]); err != nil || string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return 0, false, errors.New("16 kHz 모노 PCM16 WAV 음성만 전사할 수 있습니다.")
	}
	end := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
	if end != info.Size() || end < 44 || end > int64(MaxTranscriptionAudioDuration/time.Second)*32000+1024*1024 {
		return 0, false, errors.New("WAV 파일 크기가 올바르지 않거나 5분 제한을 초과했습니다.")
	}
	var foundFormat, foundData bool
	var totalData int64
	for position := int64(12); position < end; {
		var chunk [8]byte
		if _, err := io.ReadFull(file, chunk[:]); err != nil {
			return 0, false, errors.New("WAV 청크가 손상되었습니다.")
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:8]))
		position += 8
		if size > end-position {
			return 0, false, errors.New("WAV 음성 데이터가 잘렸습니다.")
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if foundFormat || size < 16 || size > 64 {
				return 0, false, errors.New("WAV 음성 형식이 올바르지 않습니다.")
			}
			format := make([]byte, size)
			if _, err := io.ReadFull(file, format); err != nil {
				return 0, false, err
			}
			if binary.LittleEndian.Uint16(format[0:2]) != 1 || binary.LittleEndian.Uint16(format[2:4]) != 1 || binary.LittleEndian.Uint32(format[4:8]) != 16000 || binary.LittleEndian.Uint32(format[8:12]) != 32000 || binary.LittleEndian.Uint16(format[12:14]) != 2 || binary.LittleEndian.Uint16(format[14:16]) != 16 {
				return 0, false, errors.New("16 kHz 모노 PCM16 WAV 음성만 전사할 수 있습니다.")
			}
			foundFormat = true
		case "data":
			if foundData || size == 0 || size%2 != 0 {
				return 0, false, errors.New("WAV 음성 데이터가 올바르지 않습니다.")
			}
			foundData, totalData = true, size
			buffer := make([]byte, 32*1024)
			for remaining := size; remaining > 0; {
				readSize := min(remaining, int64(len(buffer)))
				if _, err := io.ReadFull(file, buffer[:readSize]); err != nil {
					return 0, false, err
				}
				for _, sampleByte := range buffer[:readSize] {
					if sampleByte != 0 {
						audible = true
						break
					}
				}
				remaining -= readSize
			}
		default:
			if _, err := file.Seek(size, io.SeekCurrent); err != nil {
				return 0, false, err
			}
		}
		position += size
		if size%2 != 0 {
			if position >= end {
				return 0, false, errors.New("WAV 청크 패딩이 없습니다.")
			}
			if _, err := file.Seek(1, io.SeekCurrent); err != nil {
				return 0, false, err
			}
			position++
		}
	}
	if !foundFormat || !foundData || totalData > int64(MaxTranscriptionAudioDuration/time.Second)*32000 {
		return 0, false, errors.New("WAV 음성이 없거나 5분 제한을 초과했습니다.")
	}
	return totalData * 1000 / 32000, audible, nil
}
