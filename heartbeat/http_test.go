package heartbeat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPReporter_PostJSON(t *testing.T) {
	var got Snapshot
	gotBody := make(chan Snapshot, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		gotBody <- got
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rep := NewHTTPReporter(srv.URL, WithTimeout(2*time.Second))
	defer rep.Close()

	snap := Snapshot{
		NodeID:        "node-1",
		Version:       "0.1.0",
		Status:        StatusOnline,
		CapacityBytes: 8192,
		ReportedAt:    time.Now(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := rep.Report(ctx, snap); err != nil {
		t.Fatalf("Report: %v", err)
	}

	select {
	case s := <-gotBody:
		if s.NodeID != "node-1" || s.CapacityBytes != 8192 {
			t.Fatalf("unexpected snapshot: %+v", s)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive body")
	}
}

func TestHTTPReporter_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	rep := NewHTTPReporter(srv.URL)
	defer rep.Close()
	err := rep.Report(context.Background(), Snapshot{})
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestHTTPReporter_ServerDown(t *testing.T) {
	rep := NewHTTPReporter("http://127.0.0.1:1", WithTimeout(time.Second))
	defer rep.Close()
	err := rep.Report(context.Background(), Snapshot{})
	if err == nil {
		t.Fatal("expected error when server unreachable")
	}
}

func TestHTTPReporter_WithHeader(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rep := NewHTTPReporter(srv.URL, WithHeader("Authorization", "Bearer tok"))
	defer rep.Close()
	_ = rep.Report(context.Background(), Snapshot{})
	if auth != "Bearer tok" {
		t.Fatalf("Authorization = %q, want Bearer tok", auth)
	}
}
