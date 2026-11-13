package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const openAITranscriptionEndpoint = "https://api.openai.com/v1/audio/transcriptions"
const openAITranscriptionModel = "whisper-1"
const openAITranscriptionTimeout = 2 * time.Minute
const openAITranscriptionResponseLimit = 8 << 20
const openAITranscriptionUploadLimit = 25_000_000

type OpenAITranscriberConfig struct {
	APIKey   string
	Model    string
	Language string
	Client   *http.Client
	// Endpoint is a test seam. Production always uses the fixed OpenAI URL;
	// alternate endpoints are accepted only on a loopback host.
	Endpoint string
}

type OpenAITranscriber struct {
	key      string
	model    string
	language string
	endpoint string
	client   *http.Client
	err      error
	gate     chan struct{}
}

func NewOpenAITranscriber(config OpenAITranscriberConfig) *OpenAITranscriber {
	s := &OpenAITranscriber{key: strings.TrimSpace(config.APIKey), model: config.Model, language: config.Language, endpoint: config.Endpoint, gate: make(chan struct{}, 1)}
	if s.model == "" {
		s.model = openAITranscriptionModel
	}
	if s.language == "" {
		s.language = "en"
	}
	if s.endpoint == "" {
		s.endpoint = openAITranscriptionEndpoint
	}
	if !validTranscriptionModel(s.model) {
		s.err = errors.New("Choose a supported OpenAI transcription model in Settings.")
	}
	endpoint, err := url.Parse(s.endpoint)
	if err != nil || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.Host == "" {
		s.err = errors.New("The transcription API URL setting is invalid.")
	} else if s.endpoint != openAITranscriptionEndpoint {
		ip := net.ParseIP(endpoint.Hostname())
		if endpoint.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			s.err = errors.New("Transcription can only use the official OpenAI API URL.")
		}
	}
	client := http.Client{}
	if config.Client != nil {
		client = *config.Client
	}
	if client.Timeout <= 0 || client.Timeout > openAITranscriptionTimeout {
		client.Timeout = openAITranscriptionTimeout
	}
	// A redirect must never forward either the bearer key or recorded audio.
	// Override this even on an injected client, without mutating its owner.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s.client = &client
	return s
}

func (s *OpenAITranscriber) Status() TranscriptionStatus {
	status := TranscriptionStatus{Engine: "OpenAI", Model: s.model, Language: s.language}
	if s.err != nil {
		status.Error = s.err.Error()
	} else if s.key == "" || strings.ContainsAny(s.key, "\r\n") {
		status.Error = "Enter your OpenAI API key in Settings."
	} else {
		status.Ready = true
	}
	return status
}

// Transcribe sends the stored WAV directly to the selected OpenAI model. No SDK, local model,
// automatic paid retry, or fallback provider participates in this request.
// Contract: https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create
func (s *OpenAITranscriber) Transcribe(parent context.Context, wavPath string) ([]TranscriptSegment, error) {
	if err := parent.Err(); err != nil {
		return nil, &TranscriptionError{Kind: "canceled", Err: err}
	}
	if status := s.Status(); !status.Ready {
		return nil, &TranscriptionError{Kind: "unavailable", Err: errors.New(status.Error)}
	}
	ctx, cancel := context.WithTimeout(parent, openAITranscriptionTimeout)
	defer cancel()
	durationMs, audible, err := validateTranscriptionWAV(wavPath)
	if err != nil {
		return nil, &TranscriptionError{Kind: "invalid_audio", Err: errors.New("Check that the audio is a 16 kHz mono PCM16 WAV file.")}
	}
	if !audible {
		return []TranscriptSegment{}, nil
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	default:
		return nil, &TranscriptionError{Kind: "busy", Err: errors.New("Transcription is in progress. Please try again when it finishes.")}
	}
	file, err := os.Open(wavPath)
	if err != nil {
		return nil, &TranscriptionError{Kind: "invalid_audio", Err: errors.New("Couldn't open the saved audio file.")}
	}
	defer file.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := [][2]string{{"model", s.model}, {"language", s.language}}
	if s.model == openAITranscriptionModel {
		fields = append(fields, [2]string{"response_format", "verbose_json"}, [2]string{"timestamp_granularities[]", "segment"})
	} else {
		fields = append(fields, [2]string{"response_format", "json"})
	}
	for _, field := range fields {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return nil, &TranscriptionError{Kind: "failed", Err: errors.New("Couldn't prepare the transcription request.")}
		}
	}
	part, err := writer.CreateFormFile("file", "recording.wav")
	if err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: errors.New("Couldn't prepare the audio file for transcription.")}
	}
	written, err := io.Copy(part, io.LimitReader(file, openAITranscriptionUploadLimit+1))
	if err != nil || written > openAITranscriptionUploadLimit {
		return nil, &TranscriptionError{Kind: "invalid_audio", Err: errors.New("The audio file couldn't be read or exceeds the 25 MB limit.")}
	}
	if err := writer.Close(); err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: errors.New("Couldn't finalize the transcription request.")}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, &body)
	if err != nil {
		return nil, &TranscriptionError{Kind: "failed", Err: errors.New("Couldn't create the transcription request.")}
	}
	request.Header.Set("Authorization", "Bearer "+s.key)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, openAITranscriptionRequestError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, openAITranscriptionHTTPError(response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, openAITranscriptionResponseLimit+1))
	if err != nil {
		return nil, openAITranscriptionRequestError(ctx, err)
	}
	if len(data) > openAITranscriptionResponseLimit {
		return nil, &TranscriptionError{Kind: "invalid_response", Err: errors.New("The transcription result exceeds the allowed size.")}
	}
	if err := ctx.Err(); err != nil {
		return nil, &TranscriptionError{Kind: "canceled", Err: err}
	}
	if s.model == openAITranscriptionModel {
		return parseOpenAITranscription(data, durationMs)
	}
	return parseOpenAITextTranscription(data, durationMs)
}

// GPT transcription models do not return segment timestamps. Preserve one
// recording-wide interval and mark it explicitly; never invent word timings.
func parseOpenAITextTranscription(data []byte, durationMs int64) ([]TranscriptSegment, error) {
	var result struct {
		Text *string `json:"text"`
	}
	if durationMs <= 0 || json.Unmarshal(data, &result) != nil || result.Text == nil {
		return nil, &TranscriptionError{Kind: "invalid_response", Err: errors.New("Couldn't read the OpenAI transcription result.")}
	}
	text := strings.TrimSpace(*result.Text)
	if text == "" {
		return []TranscriptSegment{}, nil
	}
	return []TranscriptSegment{{StartMs: 0, EndMs: durationMs, Text: text, TimingEstimated: true}}, nil
}

func openAITranscriptionRequestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &TranscriptionError{Kind: "canceled", Err: ctx.Err()}
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return &TranscriptionError{Kind: "timeout", Err: context.DeadlineExceeded}
	}
	// Never expose transport URLs, headers, raw provider responses, or secrets
	// embedded in errors returned by a transport or redirect callback.
	return &TranscriptionError{Kind: "unavailable", Err: errors.New("Couldn't connect to the OpenAI transcription API.")}
}

func openAITranscriptionHTTPError(status int) error {
	kind, message := "failed", "OpenAI transcription didn't complete. The original audio has been kept."
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind, message = "unauthorized", "Check your OpenAI API key and its access to transcription."
	case status == http.StatusTooManyRequests:
		kind, message = "rate_limited", "Check your OpenAI API usage limits or billing status, then try again."
	case status == http.StatusRequestEntityTooLarge:
		kind, message = "invalid_audio", "The audio file exceeds the OpenAI transcription upload limit."
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		kind, message = "invalid_request", "OpenAI couldn't process the transcription request. Check the recording format."
	case status == http.StatusNotFound || status >= 500 || (status >= 300 && status < 400):
		kind, message = "unavailable", "The OpenAI transcription API is unavailable. Please try again shortly."
	}
	return &TranscriptionError{Kind: kind, Err: errors.New(message)}
}

func parseOpenAITranscription(data []byte, durationMs int64) ([]TranscriptSegment, error) {
	invalid := func(reason string) ([]TranscriptSegment, error) {
		return nil, &TranscriptionError{Kind: "invalid_response", Err: errors.New("Couldn't verify the segment timestamps in the OpenAI transcription result. " + openAITranscriptionNumericDiagnostic(data, durationMs, reason))}
	}
	var result struct {
		Text     string `json:"text"`
		Segments *[]struct {
			Start *float64 `json:"start"`
			End   *float64 `json:"end"`
			Text  *string  `json:"text"`
		} `json:"segments"`
	}
	if durationMs <= 0 {
		return invalid("invalid_audio_duration")
	}
	if json.Unmarshal(data, &result) != nil {
		return invalid("invalid_json")
	}
	if result.Segments == nil {
		return invalid("missing_segments")
	}
	segments := make([]TranscriptSegment, 0, len(*result.Segments))
	for _, segment := range *result.Segments {
		if segment.Start == nil || segment.End == nil || segment.Text == nil {
			return invalid("missing_segment_fields")
		}
		start, end := *segment.Start, *segment.End
		if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end < start || end >= float64(math.MaxInt64)/1000 {
			return invalid("invalid_segment_order_or_number")
		}
		if start*1000 >= float64(durationMs) {
			return invalid("segment_start_outside_audio")
		}
		text := strings.TrimSpace(*segment.Text)
		if text == "" {
			continue
		}
		providerStart, providerEnd := int64(math.Round(start*1000)), int64(math.Round(end*1000))
		segmentResult := TranscriptSegment{StartMs: providerStart, EndMs: min(providerEnd, durationMs), Text: text}
		if providerEnd > durationMs {
			// A real whisper-1 response ended at 5.56s for a 5.098s PCM file,
			// despite reporting duration=5.09s. Intersect with actual audio;
			// preserve the provider's interval so this adjustment is explicit.
			segmentResult.ProviderStartMs, segmentResult.ProviderEndMs = providerStart, providerEnd
			segmentResult.TimingAdjusted = true
		}
		segments = append(segments, segmentResult)
	}
	if strings.TrimSpace(result.Text) != "" && len(segments) == 0 {
		return invalid("text_without_timestamps") // Text without actual timing must never become fake timing.
	}
	return segments, nil
}

// Diagnostics deliberately decode an allowlist of numeric fields only. Text,
// unknown JSON fields, response headers, keys, and raw response bytes never
// enter this string, including on malformed provider responses.
func openAITranscriptionNumericDiagnostic(data []byte, durationMs int64, reason string) string {
	type timing struct {
		Start *float64 `json:"start"`
		End   *float64 `json:"end"`
	}
	var response struct {
		Duration *float64  `json:"duration"`
		Segments *[]timing `json:"segments"`
	}
	diagnostic := struct {
		Reason          string   `json:"reason"`
		AudioDurationMs int64    `json:"audioDurationMs"`
		DurationSeconds *float64 `json:"responseDurationSeconds,omitempty"`
		SegmentCount    int      `json:"segmentCount"`
		Timing          []timing `json:"timing,omitempty"`
	}{Reason: reason, AudioDurationMs: durationMs}
	if json.Unmarshal(data, &response) == nil {
		diagnostic.DurationSeconds = response.Duration
		if response.Segments != nil {
			diagnostic.SegmentCount = len(*response.Segments)
			diagnostic.Timing = (*response.Segments)[:min(16, len(*response.Segments))]
		}
	}
	encoded, err := json.Marshal(diagnostic)
	if err != nil {
		return `{"reason":"unavailable_numeric_diagnostic"}`
	}
	return string(encoded)
}

// API requests create no local temporary workspace. Stored recordings and
// completed transcripts belong to the existing session retention policy.
func (s *OpenAITranscriber) PruneExpired(time.Time) (int, error) { return 0, nil }
