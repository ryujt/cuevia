package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestCaptureSourceMetadataSurvivesServiceRestart(t *testing.T) {
	for _, source := range []CaptureSource{
		{ID: "program-12", Kind: "program", Name: "Google Chrome", PID: 4321, WindowID: 12, Title: "Product preview", Width: 1200, Height: 800, X: 120, Y: 40},
		{ID: "monitor-22", Kind: "monitor", Name: "External monitor 1", DisplayID: 22, DisplayIndex: 1, Width: 3840, Height: 2160, Scale: 2, Main: true, X: -1920},
	} {
		t.Run(source.Kind, func(t *testing.T) {
			service := testCaptureService(t)
			got, err := service.captureWith(context.Background(), func(_ context.Context, path string) ([]byte, error) { writeCaptureFixture(t, path); return nil, nil }, &source)
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := NewCaptureService(service.dir)
			if err != nil {
				t.Fatal(err)
			}
			restored, ok := restarted.Find(got.ID)
			if !ok || !reflect.DeepEqual(restored.Source, &source) {
				t.Fatalf("source lost after restart: %#v", restored.Source)
			}
			handler, err := newHandlerWithCapture(&captureBridge{service: restarted})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/capture/"+got.ID, nil))
			var fromHTTP Capture
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &fromHTTP) != nil || !reflect.DeepEqual(fromHTTP.Source, &source) {
				t.Fatalf("source lost through HTTP: %s", response.Body.String())
			}
		})
	}
}

func TestCaptureActivityRetainsTransitionsForDelayedRecorderClient(t *testing.T) {
	bridge := &captureBridge{}
	bridge.activity.add("selecting", "")
	bridge.activity.add("canceled", "")
	handler, err := newHandlerWithCapture(bridge)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/capture/activity?since=0", nil))
	var events []captureActivityEvent
	if json.Unmarshal(response.Body.Bytes(), &events) != nil || len(events) != 2 || events[0].State != "selecting" || events[1].State != "canceled" || events[0].AtMs == 0 {
		t.Fatalf("transitions = %s", response.Body.String())
	}
	if len(bridge.activity.since(events[0].Seq)) != 1 {
		t.Fatal("cursor repeated old transitions")
	}
}
